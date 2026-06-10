package a2a_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/YasserCR/galdor/pkg/a2a"
)

func newTestServer(t *testing.T, handler a2a.Handler) (*a2a.Client, func()) {
	t.Helper()
	card := a2a.AgentCard{
		Name:        "test-agent",
		Description: "test fixture",
		Version:     "0.1",
		Skills: []a2a.AgentSkill{
			{ID: "echo", Name: "Echo", Description: "Repeats whatever the user said"},
		},
	}
	srv := a2a.NewServer(card, handler)
	ts := httptest.NewServer(srv)
	c := a2a.NewClient(ts.URL)
	return c, func() { ts.Close() }
}

func TestFetchAgentCard(t *testing.T) {
	t.Parallel()
	c, cleanup := newTestServer(t, a2a.HandlerFunc(func(_ context.Context, _ *a2a.Task) error { return nil }))
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	card, err := c.FetchAgentCard(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if card.Name != "test-agent" || card.Version != "0.1" {
		t.Errorf("card = %+v", card)
	}
	if len(card.Skills) != 1 || card.Skills[0].ID != "echo" {
		t.Errorf("skills = %+v", card.Skills)
	}
}

func TestSendTask_EchoHandler(t *testing.T) {
	t.Parallel()
	c, cleanup := newTestServer(t, a2a.HandlerFunc(func(_ context.Context, task *a2a.Task) error {
		// Echo the latest user message back.
		var userText string
		for _, m := range task.History {
			if m.Role == a2a.RoleUser {
				userText = m.Text()
			}
		}
		task.Append(a2a.AgentText("echo: " + userText))
		task.Status.State = a2a.TaskCompleted
		return nil
	}))
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	task, err := c.SendTask(ctx, a2a.UserText("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if task.ID == "" {
		t.Error("server must assign an ID when client didn't provide one")
	}
	if task.Status.State != a2a.TaskCompleted {
		t.Errorf("state = %q", task.Status.State)
	}
	// History should include user + agent turn.
	if len(task.History) != 2 {
		t.Fatalf("history = %d, want 2", len(task.History))
	}
	if task.History[1].Role != a2a.RoleAgent || !strings.Contains(task.History[1].Text(), "echo: hello") {
		t.Errorf("agent turn wrong: %+v", task.History[1])
	}
}

func TestSendTask_HandlerErrorMarksFailed(t *testing.T) {
	t.Parallel()
	c, cleanup := newTestServer(t, a2a.HandlerFunc(func(_ context.Context, _ *a2a.Task) error {
		return contextErr("planet exploded")
	}))
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	task, err := c.SendTask(ctx, a2a.UserText("oops"))
	if err != nil {
		// Handler errors are reported via Status.State, not as RPC
		// errors — SendTask should succeed at the protocol level.
		t.Fatalf("SendTask returned error: %v", err)
	}
	if task.Status.State != a2a.TaskFailed {
		t.Errorf("state = %q, want failed", task.Status.State)
	}
	if task.Status.Message == nil || !strings.Contains(task.Status.Message.Text(), "planet exploded") {
		t.Errorf("status message should carry handler error: %+v", task.Status.Message)
	}
}

func TestSendTask_MultiTurn(t *testing.T) {
	t.Parallel()
	// Handler: on first turn, ask for more input. On second turn, complete.
	c, cleanup := newTestServer(t, a2a.HandlerFunc(func(_ context.Context, task *a2a.Task) error {
		userTurns := 0
		for _, m := range task.History {
			if m.Role == a2a.RoleUser {
				userTurns++
			}
		}
		if userTurns == 1 {
			task.Append(a2a.AgentText("please clarify"))
			task.Status.State = a2a.TaskInputRequired
			return nil
		}
		task.Append(a2a.AgentText("ok done"))
		task.Status.State = a2a.TaskCompleted
		return nil
	}))
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, err := c.SendTask(ctx, a2a.UserText("vague request"))
	if err != nil {
		t.Fatal(err)
	}
	if first.Status.State != a2a.TaskInputRequired {
		t.Fatalf("first turn state = %q, want input-required", first.Status.State)
	}
	second, err := c.SendTask(ctx, a2a.UserText("here is the detail"), a2a.WithTaskID(first.ID))
	if err != nil {
		t.Fatal(err)
	}
	if second.Status.State != a2a.TaskCompleted {
		t.Errorf("second turn state = %q", second.Status.State)
	}
	if second.ID != first.ID {
		t.Errorf("multi-turn must reuse the same task ID: %q vs %q", second.ID, first.ID)
	}
	// History grew across turns.
	if len(second.History) != 4 {
		t.Errorf("history = %d, want 4 (user1, agent1, user2, agent2)", len(second.History))
	}
}

func TestGetTask(t *testing.T) {
	t.Parallel()
	c, cleanup := newTestServer(t, a2a.HandlerFunc(func(_ context.Context, task *a2a.Task) error {
		task.Append(a2a.AgentText("ok"))
		task.Status.State = a2a.TaskCompleted
		return nil
	}))
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	created, err := c.SendTask(ctx, a2a.UserText("hi"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.GetTask(ctx, created.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID {
		t.Errorf("ID mismatch: %q vs %q", got.ID, created.ID)
	}
}

func TestGetTask_HistoryLengthTruncates(t *testing.T) {
	t.Parallel()
	c, cleanup := newTestServer(t, a2a.HandlerFunc(func(_ context.Context, task *a2a.Task) error {
		task.Append(a2a.AgentText("a"))
		task.Append(a2a.AgentText("b"))
		task.Append(a2a.AgentText("c"))
		task.Status.State = a2a.TaskCompleted
		return nil
	}))
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	created, _ := c.SendTask(ctx, a2a.UserText("go"))
	// Full history = 1 user + 3 agent = 4 messages.
	got, err := c.GetTask(ctx, created.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.History) != 2 {
		t.Errorf("history len = %d, want 2 (truncated)", len(got.History))
	}
}

func TestGetTask_UnknownIDReturnsError(t *testing.T) {
	t.Parallel()
	c, cleanup := newTestServer(t, a2a.HandlerFunc(func(_ context.Context, _ *a2a.Task) error { return nil }))
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := c.GetTask(ctx, "ghost", 0)
	if err == nil {
		t.Fatal("expected error for unknown task id")
	}
}

func TestSendTask_AutoCompletesWhenHandlerForgets(t *testing.T) {
	t.Parallel()
	// Handler returns clean but leaves state at "working" — server
	// must auto-promote to TaskCompleted so clients don't poll forever.
	c, cleanup := newTestServer(t, a2a.HandlerFunc(func(_ context.Context, task *a2a.Task) error {
		task.Append(a2a.AgentText("done but forgot the flag"))
		// Intentionally don't set Status.State to terminal.
		return nil
	}))
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	task, err := c.SendTask(ctx, a2a.UserText("hi"))
	if err != nil {
		t.Fatal(err)
	}
	if task.Status.State != a2a.TaskCompleted {
		t.Errorf("state = %q, want completed (auto-promoted)", task.Status.State)
	}
}

// contextErr is a tiny error type used to keep the test file
// stdlib-only (we don't want to pull errors.New into every assertion).
type contextErr string

func (e contextErr) Error() string { return string(e) }

// Regression (audit low): Metadata and SessionID sent with a CONTINUING
// message (task reuse) must be applied, not dropped — only the creation-time
// values used to survive.
func TestSendTask_ReuseAppliesMetadataAndSession(t *testing.T) {
	t.Parallel()
	c, cleanup := newTestServer(t, a2a.HandlerFunc(func(_ context.Context, task *a2a.Task) error {
		userTurns := 0
		for _, m := range task.History {
			if m.Role == a2a.RoleUser {
				userTurns++
			}
		}
		if userTurns == 1 {
			task.Status.State = a2a.TaskInputRequired
			return nil
		}
		task.Status.State = a2a.TaskCompleted
		return nil
	}))
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, err := c.SendTask(ctx, a2a.UserText("turn one"),
		a2a.WithMetadata(map[string]any{"a": "1"}))
	if err != nil {
		t.Fatal(err)
	}
	// Continue the task with NEW metadata + a session id.
	second, err := c.SendTask(ctx, a2a.UserText("turn two"),
		a2a.WithTaskID(first.ID),
		a2a.WithSessionID("sess-42"),
		a2a.WithMetadata(map[string]any{"b": "2"}))
	if err != nil {
		t.Fatal(err)
	}
	if second.SessionID != "sess-42" {
		t.Errorf("SessionID = %q, want sess-42 (reuse must apply it)", second.SessionID)
	}
	// Both the original and the continuing metadata must be present (merge).
	if second.Metadata["a"] != "1" || second.Metadata["b"] != "2" {
		t.Errorf("Metadata = %+v, want merged {a:1, b:2}", second.Metadata)
	}
}
