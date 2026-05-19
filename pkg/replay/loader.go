package replay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/YasserCR/galdor/internal/store"
	"github.com/YasserCR/galdor/pkg/observability"
	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// SaveToFile writes a Recording to path as indented JSON. The
// fixture format is the Recording struct verbatim, so files written
// here can be hand-edited and re-loaded.
func SaveToFile(rec Recording, path string) error {
	if rec.Version == 0 {
		rec.Version = CurrentFixtureVersion
	}
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("replay: marshal: %w", err)
	}
	return os.WriteFile(path, raw, 0o644) // #nosec G306 -- 0644 is correct; replay fixtures are repo-committed regression artifacts, not secrets
}

// LoadFromFile reads a Recording fixture from disk.
func LoadFromFile(path string) (Recording, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- caller-supplied fixture path; fixtures are non-secret regression artifacts
	if err != nil {
		return Recording{}, fmt.Errorf("replay: read %s: %w", path, err)
	}
	var rec Recording
	if err := json.Unmarshal(raw, &rec); err != nil {
		return Recording{}, fmt.Errorf("replay: decode %s: %w", path, err)
	}
	if rec.Version != CurrentFixtureVersion {
		return Recording{}, fmt.Errorf("replay: fixture version %d unsupported (want %d)",
			rec.Version, CurrentFixtureVersion)
	}
	return rec, nil
}

// LoadFromStore reads recorded Generate calls for runID from a
// SQLite trace store, returning them in the order they were
// recorded (sorted by span start time).
//
// The store must contain spans recorded with
// observability.WithCaptureContent(true) — without prompt /
// completion bodies in the span attributes, replay is impossible.
// An ErrNoContent-wrapped error is returned in that case so callers
// can give a useful message.
func LoadFromStore(ctx context.Context, dbPath, runID string) (Recording, error) {
	if dbPath == "" {
		return Recording{}, errors.New("replay: dbPath is empty")
	}
	if runID == "" {
		return Recording{}, errors.New("replay: runID is empty")
	}
	s, err := store.Open(ctx, dbPath)
	if err != nil {
		return Recording{}, fmt.Errorf("replay: open store: %w", err)
	}
	defer func() { _ = s.Close() }()

	spans, err := s.SpansForRun(ctx, runID)
	if err != nil {
		return Recording{}, fmt.Errorf("replay: load spans: %w", err)
	}
	if len(spans) == 0 {
		return Recording{}, fmt.Errorf("replay: no spans found for run %q", runID)
	}

	rec := Recording{Version: CurrentFixtureVersion, RunID: runID}
	var providerSpans []store.Span
	for _, sp := range spans {
		if sp.Name == observability.SpanProviderGenerate {
			providerSpans = append(providerSpans, sp)
		}
	}
	if len(providerSpans) == 0 {
		return Recording{}, fmt.Errorf("replay: run %q has no provider.generate spans", runID)
	}
	sort.SliceStable(providerSpans, func(i, j int) bool {
		return providerSpans[i].StartTimeUnixNano < providerSpans[j].StartTimeUnixNano
	})

	rec.Calls = make([]RecordedCall, 0, len(providerSpans))
	for _, sp := range providerSpans {
		call, err := callFromSpan(sp)
		if err != nil {
			return Recording{}, err
		}
		rec.Calls = append(rec.Calls, call)
	}
	return rec, nil
}

// ErrNoContent indicates a span lacked the captured prompt /
// completion attributes that replay needs.
var ErrNoContent = errors.New("replay: span has no captured content (run with observability.WithCaptureContent(true))")

// callFromSpan decodes one provider.generate span into a
// RecordedCall. Returns ErrNoContent (wrapped) when the span lacks
// the prompt / completion attributes.
func callFromSpan(sp store.Span) (RecordedCall, error) {
	promptRaw := stringAttr(sp.Attributes, observability.AttrGenAIPrompt)
	completionRaw := stringAttr(sp.Attributes, observability.AttrGenAICompletion)
	if promptRaw == "" || completionRaw == "" {
		return RecordedCall{}, fmt.Errorf("%w: span %s", ErrNoContent, sp.SpanID)
	}
	var prompt []schema.Message
	if err := json.Unmarshal([]byte(promptRaw), &prompt); err != nil {
		return RecordedCall{}, fmt.Errorf("replay: decode prompt for span %s: %w", sp.SpanID, err)
	}
	var completion schema.Message
	if err := json.Unmarshal([]byte(completionRaw), &completion); err != nil {
		return RecordedCall{}, fmt.Errorf("replay: decode completion for span %s: %w", sp.SpanID, err)
	}
	resp := &provider.Response{
		Message:    completion,
		StopReason: schema.StopReason(stringAttr(sp.Attributes, observability.AttrGenAIResponseFinish)),
		Model:      stringAttr(sp.Attributes, observability.AttrGenAIResponseModel),
		Usage: schema.Usage{
			InputTokens:  intAttr(sp.Attributes, observability.AttrGenAIUsageInputTokens),
			OutputTokens: intAttr(sp.Attributes, observability.AttrGenAIUsageOutputTokens),
		},
	}
	return RecordedCall{
		SpanID:   sp.SpanID,
		Model:    stringAttr(sp.Attributes, observability.AttrGenAIRequestModel),
		Prompt:   prompt,
		Response: resp,
	}, nil
}

// stringAttr safely fetches a string attribute by key. Returns ""
// when missing or not a string.
func stringAttr(attrs map[string]any, key string) string {
	v, ok := attrs[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// intAttr safely fetches an int-ish attribute. JSON numbers decode
// as float64; we tolerate both.
func intAttr(attrs map[string]any, key string) int {
	v, ok := attrs[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}
