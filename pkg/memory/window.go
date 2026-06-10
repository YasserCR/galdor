package memory

import (
	"context"
	"sync"

	"github.com/YasserCR/galdor/pkg/schema"
)

// Window is a bounded short-term memory: a list of messages capped by
// either message count or estimated token count (or both). Callers
// drive the conversation through Append, then Snapshot to obtain the
// trimmed slice that should be fed to the next LLM call.
//
// Trimming preserves the first system message (if any) so the agent's
// instructions are never dropped, and prefers to evict the oldest
// non-system turns. When a Summarizer is configured, evicted turns
// are compressed into a single system message instead of discarded —
// useful for long-running agents that need to remember earlier
// context cheaply.
//
// Window is goroutine-safe.
type Window struct {
	// MaxMessages, when > 0, caps how many messages the snapshot may
	// contain (including the system message and the summary).
	MaxMessages int

	// MaxTokens, when > 0, caps the estimated total token count of
	// the snapshot. Estimation uses a 4-chars-per-token heuristic
	// over Message.Text(); accurate enough for budgeting, not for
	// billing.
	MaxTokens int

	// Summarizer, when non-nil, is called with the messages about to
	// be evicted. The returned text replaces them as a single system
	// message tagged with Name="summary". When nil, evicted messages
	// are simply dropped.
	Summarizer Summarizer

	// snapMu serializes Snapshot so two concurrent snapshots don't
	// double-summarize the same evicted prefix. It is held across the
	// (unlocked-w.mu) Summarizer call; mu guards the data and is released
	// during that call so Append/Len stay responsive.
	snapMu   sync.Mutex
	mu       sync.Mutex
	messages []schema.Message
	summary  string // accumulated summary; prepended on Snapshot
}

// Summarizer compresses a slice of messages into a short paragraph.
// Implementations typically call an LLM; see SummarizerFunc for a
// trivial wrapper. Errors fall back to dropping the messages.
type Summarizer interface {
	Summarize(ctx context.Context, messages []schema.Message) (string, error)
}

// SummarizerFunc adapts a plain function to the Summarizer interface.
type SummarizerFunc func(ctx context.Context, messages []schema.Message) (string, error)

// Summarize implements Summarizer.
func (f SummarizerFunc) Summarize(ctx context.Context, m []schema.Message) (string, error) {
	return f(ctx, m)
}

// Append adds m to the window. It does not trim; trimming runs in
// Snapshot so that the configured Summarizer can be invoked with a
// caller-provided context.
func (w *Window) Append(m schema.Message) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.messages = append(w.messages, m)
}

// AppendAll is a convenience for adding many messages at once.
func (w *Window) AppendAll(ms []schema.Message) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.messages = append(w.messages, ms...)
}

// Snapshot returns the trimmed slice of messages to feed to the next
// LLM call. The returned slice is safe to mutate; the window's
// internal storage is not aliased.
//
// When a Summarizer is configured and the window exceeds its caps,
// the oldest non-system messages are summarized into a single system
// message and prepended to the snapshot.
func (w *Window) Snapshot(ctx context.Context) ([]schema.Message, error) {
	// Only one summarizing Snapshot at a time, so two callers can't
	// double-summarize the same evicted prefix.
	w.snapMu.Lock()
	defer w.snapMu.Unlock()

	// Phase 1 (data lock): decide the eviction prefix and copy the
	// messages to be summarized. The prefix is the oldest non-system
	// messages, which stay at the front regardless of concurrent Appends.
	w.mu.Lock()
	sys, start := w.evictionPlanLocked()
	var evictedCopy []schema.Message
	if start > 0 && w.Summarizer != nil {
		body := w.messages
		if sys != nil {
			body = body[1:]
		}
		evictedCopy = append([]schema.Message(nil), body[:start]...)
	}
	w.mu.Unlock()

	// Phase 2 (NO data lock): run the Summarizer (an LLM). Append/Len
	// stay responsive while this round-trips.
	var newSummary string
	if len(evictedCopy) > 0 {
		if s, err := w.Summarizer.Summarize(ctx, evictedCopy); err == nil {
			newSummary = s
		}
	}

	// Phase 3 (data lock): fold the new summary, drop the evicted prefix
	// (still at the front), and build the snapshot from the current state.
	w.mu.Lock()
	defer w.mu.Unlock()
	if newSummary != "" {
		if w.summary != "" {
			w.summary = w.summary + "\n\n" + newSummary
		} else {
			w.summary = newSummary
		}
	}
	if start > 0 {
		curSys := sys
		body := w.messages
		if len(body) > 0 && body[0].Role == schema.RoleSystem {
			s := body[0]
			curSys = &s
			body = body[1:]
		}
		drop := start
		if drop > len(body) {
			drop = len(body) // defensive: prefix shrank (shouldn't happen)
		}
		newMsgs := make([]schema.Message, 0, 1+len(body)-drop)
		if curSys != nil {
			newMsgs = append(newMsgs, *curSys)
		}
		newMsgs = append(newMsgs, body[drop:]...)
		w.messages = newMsgs
	}
	return w.buildSnapshotLocked(), nil
}

// evictionPlanLocked returns the leading system message (if any) and how
// many of the remaining (oldest-first) messages must be evicted to fit
// the caps. Caller must hold w.mu.
func (w *Window) evictionPlanLocked() (sys *schema.Message, start int) {
	body := w.messages
	if len(body) > 0 && body[0].Role == schema.RoleSystem {
		s := body[0]
		sys = &s
		body = body[1:]
	}
	for start < len(body) && !w.fits(sys, body[start:]) {
		start++
	}
	return sys, start
}

// buildSnapshotLocked assembles the output snapshot from the current
// state: leading system message, the accumulated summary, then the
// remaining messages. Caller must hold w.mu.
func (w *Window) buildSnapshotLocked() []schema.Message {
	body := w.messages
	var sys *schema.Message
	if len(body) > 0 && body[0].Role == schema.RoleSystem {
		s := body[0]
		sys = &s
		body = body[1:]
	}
	out := make([]schema.Message, 0, 2+len(body))
	if sys != nil {
		out = append(out, *sys)
	}
	if w.summary != "" {
		summaryMsg := schema.SystemMessage("Conversation summary so far:\n" + w.summary)
		summaryMsg.Name = "summary"
		out = append(out, summaryMsg)
	}
	out = append(out, body...)
	return out
}

// Len returns the number of messages currently stored in the window
// (including any leading system message). Useful for tests.
func (w *Window) Len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.messages)
}

// fits reports whether the proposed snapshot (system + summary +
// kept) fits under both configured caps. A cap of 0 means "no limit".
// When a Summarizer is configured, a slot is reserved for the
// summary message even if it has not been produced yet, so that
// eviction does not push the final snapshot past MaxMessages.
func (w *Window) fits(sys *schema.Message, kept []schema.Message) bool {
	hasSummarySlot := w.summary != "" || w.Summarizer != nil
	count := len(kept)
	if sys != nil {
		count++
	}
	if hasSummarySlot {
		count++
	}
	if w.MaxMessages > 0 && count > w.MaxMessages {
		return false
	}
	if w.MaxTokens > 0 {
		tokens := 0
		if sys != nil {
			tokens += estimateTokens(sys.Text())
		}
		if w.summary != "" {
			tokens += estimateTokens(w.summary)
		}
		for _, m := range kept {
			tokens += estimateTokens(m.Text())
		}
		if tokens > w.MaxTokens {
			return false
		}
	}
	return true
}

// estimateTokens approximates an LLM tokenizer with a 4-chars-per-token
// heuristic. This is intentionally rough; for accurate counts use a
// provider-specific tokenizer.
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	n := (len(s) + 3) / 4
	if n < 1 {
		return 1
	}
	return n
}
