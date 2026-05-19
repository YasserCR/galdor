package memory_test

import (
	"context"
	"strings"
	"testing"

	"github.com/YasserCR/galdor/pkg/memory"
	"github.com/YasserCR/galdor/pkg/schema"
)

func TestWindow_NoCapNoTrimming(t *testing.T) {
	t.Parallel()
	w := &memory.Window{}
	w.AppendAll([]schema.Message{
		schema.SystemMessage("you are helpful"),
		schema.UserMessage("a"),
		schema.AssistantMessage("b"),
		schema.UserMessage("c"),
	})
	out, err := w.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 4 {
		t.Fatalf("Snapshot len = %d, want 4 (no caps)", len(out))
	}
}

func TestWindow_TrimsToMaxMessagesKeepingSystem(t *testing.T) {
	t.Parallel()
	w := &memory.Window{MaxMessages: 3}
	w.AppendAll([]schema.Message{
		schema.SystemMessage("you are helpful"),
		schema.UserMessage("a"),
		schema.AssistantMessage("b"),
		schema.UserMessage("c"),
		schema.AssistantMessage("d"),
	})
	out, err := w.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("Snapshot len = %d, want 3", len(out))
	}
	if out[0].Role != schema.RoleSystem {
		t.Errorf("first message should be the system prompt, got %s", out[0].Role)
	}
	// The two most-recent non-system messages must survive.
	if out[1].Text() != "c" || out[2].Text() != "d" {
		t.Errorf("kept messages wrong: %+v", out)
	}
}

func TestWindow_TrimsToTokenBudget(t *testing.T) {
	t.Parallel()
	// Each message body is 60 chars => 15 tokens at 4 chars/token.
	// MaxTokens=30 forces dropping the oldest non-system turn until
	// only the most-recent message (plus the small system prompt)
	// fits.
	w := &memory.Window{MaxTokens: 30}
	big := strings.Repeat("x", 60)
	w.AppendAll([]schema.Message{
		schema.SystemMessage("sys"),
		schema.UserMessage(big),
		schema.AssistantMessage(big),
		schema.UserMessage(big),
	})
	out, err := w.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(out) >= 4 {
		t.Fatalf("Snapshot len = %d, want fewer than 4 (token-budget should evict)", len(out))
	}
	if out[0].Role != schema.RoleSystem {
		t.Errorf("system must be preserved, got %s", out[0].Role)
	}
}

func TestWindow_SummarizerPreservesEvictedContext(t *testing.T) {
	t.Parallel()
	w := &memory.Window{
		MaxMessages: 3,
		Summarizer: memory.SummarizerFunc(func(_ context.Context, ms []schema.Message) (string, error) {
			return "summary of " + countText(ms) + " messages", nil
		}),
	}
	w.AppendAll([]schema.Message{
		schema.SystemMessage("sys"),
		schema.UserMessage("a"),
		schema.AssistantMessage("b"),
		schema.UserMessage("c"),
		schema.AssistantMessage("d"),
	})
	out, err := w.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Expect: sys + summary + 1 kept message = 3 total.
	if len(out) != 3 {
		t.Fatalf("Snapshot len = %d, want 3", len(out))
	}
	if out[1].Name != "summary" {
		t.Errorf("expected summary at index 1, got Name=%q Text=%q", out[1].Name, out[1].Text())
	}
	if !strings.Contains(out[1].Text(), "summary of") {
		t.Errorf("summary text not used: %q", out[1].Text())
	}
}

func TestWindow_SnapshotIsIndependentSlice(t *testing.T) {
	t.Parallel()
	w := &memory.Window{}
	w.Append(schema.UserMessage("a"))
	out, _ := w.Snapshot(context.Background())
	out[0] = schema.UserMessage("MUTATED")
	if w.Len() != 1 {
		t.Fatalf("Len = %d", w.Len())
	}
	again, _ := w.Snapshot(context.Background())
	if again[0].Text() != "a" {
		t.Errorf("internal storage was aliased: got %q", again[0].Text())
	}
}

func countText(ms []schema.Message) string {
	switch len(ms) {
	case 1:
		return "1"
	case 2:
		return "2"
	case 3:
		return "3"
	default:
		return "many"
	}
}
