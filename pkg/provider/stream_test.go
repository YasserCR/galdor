package provider

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/YasserCR/galdor/pkg/schema"
)

// fakeStream is a deterministic StreamReader used for tests.
type fakeStream struct {
	events []Event
	err    error // returned after the last event
	closed bool
	pos    int
}

func (f *fakeStream) Recv(ctx context.Context) (Event, error) {
	if ctx.Err() != nil {
		return Event{}, ctx.Err()
	}
	if f.pos >= len(f.events) {
		if f.err != nil {
			return Event{}, f.err
		}
		return Event{}, io.EOF
	}
	ev := f.events[f.pos]
	f.pos++
	return ev, nil
}

func (f *fakeStream) Close() error { f.closed = true; return nil }

func TestCollectStream_TextOnly(t *testing.T) {
	t.Parallel()
	r := &fakeStream{events: []Event{
		{Type: EventMessageStart, Model: "test-model"},
		{Type: EventContentDelta, ContentDelta: "Hello, "},
		{Type: EventContentDelta, ContentDelta: "world!"},
		{Type: EventMessageStop, StopReason: schema.StopReasonEndTurn, Usage: schema.Usage{InputTokens: 10, OutputTokens: 4}},
	}}
	resp, err := CollectStream(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if !r.closed {
		t.Error("CollectStream must Close the reader")
	}
	if resp.Message.Text() != "Hello, world!" {
		t.Errorf("Text = %q", resp.Message.Text())
	}
	if resp.StopReason != schema.StopReasonEndTurn {
		t.Errorf("StopReason = %q", resp.StopReason)
	}
	if resp.Usage.Total() != 14 {
		t.Errorf("Usage.Total = %d", resp.Usage.Total())
	}
	if resp.Model != "test-model" {
		t.Errorf("Model = %q", resp.Model)
	}
}

func TestCollectStream_ToolCallsJoined(t *testing.T) {
	t.Parallel()
	r := &fakeStream{events: []Event{
		{Type: EventMessageStart},
		{Type: EventToolCallDelta, ToolCallDelta: &ToolCallDelta{ID: "c1", Name: "weather"}},
		{Type: EventToolCallDelta, ToolCallDelta: &ToolCallDelta{ID: "c1", ArgumentsDelta: `{"city":`}},
		{Type: EventToolCallDelta, ToolCallDelta: &ToolCallDelta{ID: "c2", Name: "time"}},
		{Type: EventToolCallDelta, ToolCallDelta: &ToolCallDelta{ID: "c1", ArgumentsDelta: `"Quito"}`}},
		{Type: EventToolCallDelta, ToolCallDelta: &ToolCallDelta{ID: "c2", ArgumentsDelta: `{}`}},
		{Type: EventMessageStop, StopReason: schema.StopReasonToolUse},
	}}
	resp, err := CollectStream(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(resp.Message.ToolCalls); got != 2 {
		t.Fatalf("ToolCalls = %d, want 2", got)
	}
	// Order is preserved by first-seen ID.
	if resp.Message.ToolCalls[0].ID != "c1" || resp.Message.ToolCalls[0].Name != "weather" {
		t.Errorf("first call = %+v", resp.Message.ToolCalls[0])
	}
	if string(resp.Message.ToolCalls[0].Arguments) != `{"city":"Quito"}` {
		t.Errorf("c1 args = %s", resp.Message.ToolCalls[0].Arguments)
	}
	if resp.Message.ToolCalls[1].ID != "c2" || string(resp.Message.ToolCalls[1].Arguments) != `{}` {
		t.Errorf("second call = %+v", resp.Message.ToolCalls[1])
	}
	if resp.StopReason != schema.StopReasonToolUse {
		t.Errorf("StopReason = %q", resp.StopReason)
	}
}

// Regression for audit M9: streamed reasoning rides the terminal
// MessageStop.Message as a thinking part (with signature). CollectStream
// must preserve it — before the fix it read only StopReason/Usage/Model
// and silently dropped the reasoning, the v0.6.0 headline capability.
func TestCollectStream_PreservesReasoning(t *testing.T) {
	t.Parallel()
	r := &fakeStream{events: []Event{
		{Type: EventMessageStart, Model: "test-model"},
		{Type: EventContentDelta, ContentDelta: "The answer is 391."},
		{Type: EventMessageStop, StopReason: schema.StopReasonEndTurn, Message: &schema.Message{
			Role: schema.RoleAssistant,
			Content: []schema.ContentPart{{
				Type:      schema.ContentTypeThinking,
				Text:      "17 * 23 = 391",
				Signature: "sig-abc",
			}},
		}},
	}}
	resp, err := CollectStream(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	// The text answer is preserved unchanged.
	if resp.Message.Text() != "The answer is 391." {
		t.Errorf("Text = %q", resp.Message.Text())
	}
	// The thinking part survives, with its signature, ordered before text.
	var think *schema.ContentPart
	for i := range resp.Message.Content {
		if resp.Message.Content[i].Type == schema.ContentTypeThinking {
			think = &resp.Message.Content[i]
			break
		}
	}
	if think == nil {
		t.Fatal("reasoning dropped by CollectStream (regression of M9)")
	}
	if think.Text != "17 * 23 = 391" || think.Signature != "sig-abc" {
		t.Errorf("thinking part = %+v", *think)
	}
	if resp.Message.Content[0].Type != schema.ContentTypeThinking {
		t.Error("thinking part should precede the text part")
	}
}

func TestCollectStream_NilReader(t *testing.T) {
	t.Parallel()
	if _, err := CollectStream(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil reader")
	}
}

func TestCollectStream_ErrorPropagated(t *testing.T) {
	t.Parallel()
	want := errors.New("boom")
	r := &fakeStream{
		events: []Event{{Type: EventMessageStart}},
		err:    want,
	}
	if _, err := CollectStream(context.Background(), r); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if !r.closed {
		t.Error("reader not closed on error")
	}
}

func TestCollectStream_ContextCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &fakeStream{events: []Event{{Type: EventContentDelta, ContentDelta: "x"}}}
	if _, err := CollectStream(ctx, r); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestCollectStream_IgnoresMalformedToolDelta(t *testing.T) {
	t.Parallel()
	r := &fakeStream{events: []Event{
		{Type: EventToolCallDelta, ToolCallDelta: nil},
		{Type: EventToolCallDelta, ToolCallDelta: &ToolCallDelta{}}, // empty ID
		{Type: EventMessageStop, StopReason: schema.StopReasonEndTurn},
	}}
	resp, err := CollectStream(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.ToolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(resp.Message.ToolCalls))
	}
}
