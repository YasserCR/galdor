package a2a_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/YasserCR/galdor/pkg/a2a"
)

// Regression for audit M33: tasks/get must return promptly with the
// "working" state while a long handler runs — it must NOT block until the
// handler completes. The handler here blocks until we release it; if the
// data lock were held across the handler (the old behavior), GetTask would
// hang and the test would fail on -timeout.
func TestTasksGet_NotBlockedByRunningHandler(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	entered := make(chan struct{})
	c, cleanup := newTestServer(t, a2a.HandlerFunc(func(_ context.Context, task *a2a.Task) error {
		close(entered)
		<-release // block here, holding the task "working"
		task.Status.State = a2a.TaskCompleted
		return nil
	}))
	defer cleanup()
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _, _ = c.SendTask(ctx, a2a.UserText("hi"), a2a.WithTaskID("t1")) }()
	<-entered // handler is now blocked; the task is stored as "working"

	// This must return immediately with state=working, not hang.
	got, err := c.GetTask(ctx, "t1", 0)
	if err != nil {
		t.Fatalf("GetTask during a running handler errored: %v", err)
	}
	if got.Status.State != a2a.TaskWorking {
		t.Errorf("state = %q, want working (poll must see in-progress state)", got.Status.State)
	}
}

// Regression for audit M34: a tasks/send against a task already in a
// terminal state must be rejected, not silently re-opened and re-run.
func TestTasksSend_RejectsTerminalTask(t *testing.T) {
	t.Parallel()
	c, cleanup := newTestServer(t, a2a.HandlerFunc(func(_ context.Context, task *a2a.Task) error {
		task.Append(a2a.AgentText("done"))
		task.Status.State = a2a.TaskCompleted
		return nil
	}))
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, err := c.SendTask(ctx, a2a.UserText("one"))
	if err != nil {
		t.Fatal(err)
	}
	if first.Status.State != a2a.TaskCompleted {
		t.Fatalf("first send state = %q, want completed", first.Status.State)
	}
	// Re-sending to the now-completed task must error.
	_, err = c.SendTask(ctx, a2a.UserText("two"), a2a.WithTaskID(first.ID))
	if err == nil {
		t.Fatal("expected an error sending to a terminal task (regression of M34)")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "terminal") {
		t.Errorf("error should mention the terminal state, got: %v", err)
	}
}

// Regression for audit H16: the JSON-RPC body must be size-capped so an
// unauthenticated peer can't drive unbounded allocation. A body past the
// cap is rejected rather than fully buffered.
func TestServeJSONRPC_BodySizeCapped(t *testing.T) {
	t.Parallel()
	srv := a2a.NewServer(a2a.AgentCard{Name: "t", Version: "1"}, a2a.HandlerFunc(
		func(_ context.Context, task *a2a.Task) error { task.Status.State = a2a.TaskCompleted; return nil }))
	ts := httptest.NewServer(srv)
	defer ts.Close()

	// A ~5 MiB body, past the 4 MiB cap. Shape it as a JSON-RPC request
	// with a giant text part so it parses up to the cap.
	big := strings.Repeat("x", 5<<20)
	body := `{"jsonrpc":"2.0","id":1,"method":"tasks/send","params":{"message":{"role":"user","parts":[{"type":"text","text":"` + big + `"}]}}}`

	resp, err := http.Post(ts.URL, "application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		// A connection reset / truncated response is also acceptable
		// evidence the oversized body was refused, not buffered.
		return
	}
	if out.Error == nil {
		t.Fatal("oversized body was accepted (regression of H16): expected a JSON-RPC error")
	}
}
