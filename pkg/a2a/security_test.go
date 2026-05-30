package a2a_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YasserCR/galdor/pkg/a2a"
)

// TestConcurrentSameIDSend exercises the data race fixed in A2: many
// goroutines fire tasks/send with the SAME client-supplied id while
// others fire tasks/get. The handler mutates History/Status. Run with
// -race; it must be race-clean and must not panic on concurrent slice
// append.
func TestConcurrentSameIDSend(t *testing.T) {
	t.Parallel()
	c, cleanup := newTestServer(t, a2a.HandlerFunc(func(_ context.Context, task *a2a.Task) error {
		task.Append(a2a.AgentText("ack"))
		task.Status.State = a2a.TaskInputRequired // keep it non-terminal so reuse stays valid
		return nil
	}))
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const id = "shared-id"
	const n = 16
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, err := c.SendTask(ctx, a2a.UserText("hi"), a2a.WithTaskID(id)); err != nil {
				t.Errorf("SendTask: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			// May 404 before the first send lands; only real errors fail.
			_, _ = c.GetTask(ctx, id, 0)
		}()
	}
	wg.Wait()

	got, err := c.GetTask(ctx, id, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Each send appends a user + agent turn: 2*n messages.
	if len(got.History) != 2*n {
		t.Errorf("history = %d, want %d", len(got.History), 2*n)
	}
}

// TestFetchAgentCard_NoCrossHostRedirect covers A3: a card host that
// 302-redirects to a different host must NOT be followed.
func TestFetchAgentCard_NoCrossHostRedirect(t *testing.T) {
	t.Parallel()

	var otherHit atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		otherHit.Store(true)
		_, _ = w.Write([]byte(`{"name":"evil","url":"http://evil"}`))
	}))
	defer other.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusFound)
	}))
	defer redirector.Close()

	c := a2a.NewClient(redirector.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := c.FetchAgentCard(ctx)
	if err == nil {
		t.Fatal("expected error: cross-host redirect must not be followed")
	}
	if otherHit.Load() {
		t.Fatal("client followed redirect to a different host (SSRF)")
	}
}

// TestFetchAgentCard_ResponseSizeCap covers A4: a card larger than the
// cap must be rejected rather than fully decoded.
func TestFetchAgentCard_ResponseSizeCap(t *testing.T) {
	t.Parallel()

	huge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Emit a syntactically-open JSON object far larger than the cap.
		_, _ = w.Write([]byte(`{"name":"`))
		blob := strings.Repeat("a", 1<<20)
		for i := 0; i < 8; i++ { // 8 MiB > 4 MiB cap
			_, _ = w.Write([]byte(blob))
		}
		_, _ = w.Write([]byte(`"}`))
	}))
	defer huge.Close()

	c := a2a.NewClient("")
	c.AgentCardURL = huge.URL
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := c.FetchAgentCard(ctx)
	if err == nil {
		t.Fatal("expected error: oversized agent card must be rejected")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("error = %v, want size-cap rejection", err)
	}
}

// TestCall_ResponseSizeCap covers A4 for the JSON-RPC call path: an
// oversized tasks/send reply must be rejected.
func TestCall_ResponseSizeCap(t *testing.T) {
	t.Parallel()

	huge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":{"id":"x","status":{"state":"completed"},"history":[{"role":"agent","parts":[{"type":"text","text":"`)
		blob := strings.Repeat("a", 1<<20)
		for i := 0; i < 8; i++ {
			_, _ = w.Write([]byte(blob))
		}
		_, _ = w.Write([]byte(`"}]}]}}`))
	}))
	defer huge.Close()

	c := a2a.NewClient(huge.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := c.SendTask(ctx, a2a.UserText("hi"))
	if err == nil {
		t.Fatal("expected error: oversized RPC reply must be rejected")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("error = %v, want size-cap rejection", err)
	}
}
