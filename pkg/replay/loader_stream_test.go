package replay_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/YasserCR/galdor/internal/store"
	"github.com/YasserCR/galdor/pkg/observability"
	"github.com/YasserCR/galdor/pkg/replay"
	"github.com/YasserCR/galdor/pkg/schema"
)

// Regression for audit M25: LoadFromStore must include streaming provider
// spans (galdor.provider.stream), which carry the same captured
// prompt/completion as non-streaming ones. Previously only generate spans
// were collected, so a fully-streaming run was unreplayable.
func TestLoadFromStore_IncludesStreamSpans(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "traces.db")
	s, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	prompt, _ := json.Marshal([]schema.Message{schema.UserMessage("hi")})
	completion, _ := json.Marshal(schema.AssistantMessage("hello"))
	if err = s.InsertSpans(ctx, []store.Span{{
		SpanID: "s1", TraceID: "t1", Name: observability.SpanProviderStream,
		StartTimeUnixNano: 1, EndTimeUnixNano: 2, RunID: "run1", StatusCode: "ok",
		Attributes: map[string]any{
			observability.AttrGenAIRequestModel: "m",
			observability.AttrGenAIPrompt:       string(prompt),
			observability.AttrGenAICompletion:   string(completion),
		},
	}}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	rec, err := replay.LoadFromStore(ctx, dbPath, "run1")
	if err != nil {
		t.Fatalf("LoadFromStore on a streaming run must succeed (regression of M25): %v", err)
	}
	if len(rec.Calls) != 1 {
		t.Fatalf("expected 1 recorded call from the stream span, got %d", len(rec.Calls))
	}
}
