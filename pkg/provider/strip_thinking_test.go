package provider_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// stubProv returns a fixed Response from Generate. Stream is unused
// here; the streaming tests use stubStream directly.
type stubProv struct {
	resp     *provider.Response
	streamEv []provider.Event
}

func (*stubProv) Name() string                        { return "stub" }
func (*stubProv) Capabilities() provider.Capabilities { return provider.Capabilities{} }

func (p *stubProv) Generate(_ context.Context, _ provider.Request) (*provider.Response, error) {
	return p.resp, nil
}

func (p *stubProv) Stream(_ context.Context, _ provider.Request) (provider.StreamReader, error) {
	return &stubStream{events: p.streamEv}, nil
}

type stubStream struct {
	events []provider.Event
	closed bool
}

func (s *stubStream) Recv(_ context.Context) (provider.Event, error) {
	if len(s.events) == 0 {
		return provider.Event{}, io.EOF
	}
	ev := s.events[0]
	s.events = s.events[1:]
	return ev, nil
}

func (s *stubStream) Close() error { s.closed = true; return nil }

func TestStripThinkingBlocks_Generate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"single block", "<think>reasoning</think>answer", "answer"},
		{"passthrough", "no blocks here", "no blocks here"},
		{"mixed blocks", "<think>x</think>actual answer<think>y</think>more", "actual answermore"},
		{"uppercase tag", "<THINK>secret</THINK>shown", "shown"},
		{"mixed case", "<Thinking>secret</Thinking>visible", "visible"},
		{"multiline reasoning", "<think>line1\nline2\nline3</think>final", "final"},
		{"attrs on open", `<think foo="bar">x</think>ok`, "ok"},
		{"leading whitespace stripped", "<think>x</think>\n\nhello", "hello"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := provider.StripThinkingBlocks(&stubProv{
				resp: &provider.Response{Message: schema.AssistantMessage(tc.in)},
			})
			resp, err := p.Generate(context.Background(), provider.Request{})
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if got := resp.Message.Text(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStripThinkingBlocks_GenerateNilResponse(t *testing.T) {
	t.Parallel()
	p := provider.StripThinkingBlocks(&stubProv{resp: nil})
	resp, err := p.Generate(context.Background(), provider.Request{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if resp != nil {
		t.Errorf("resp = %+v, want nil", resp)
	}
}

func TestStripThinkingBlocks_NilInnerPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on nil inner")
		}
	}()
	_ = provider.StripThinkingBlocks(nil)
}

// collectDeltas drives a stream to EOF and concatenates every
// EventContentDelta payload. Returns the final text.
func collectDeltas(t *testing.T, sr provider.StreamReader) string {
	t.Helper()
	var got string
	for {
		ev, err := sr.Recv(context.Background())
		if errors.Is(err, io.EOF) {
			return got
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		if ev.Type == provider.EventContentDelta {
			got += ev.ContentDelta
		}
	}
}

func TestStripThinkingBlocks_StreamSingleDelta(t *testing.T) {
	t.Parallel()
	inner := &stubProv{streamEv: []provider.Event{
		{Type: provider.EventContentDelta, ContentDelta: "<think>x</think>hi"},
		{Type: provider.EventMessageStop},
	}}
	p := provider.StripThinkingBlocks(inner)
	sr, err := p.Stream(context.Background(), provider.Request{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer sr.Close()
	if got := collectDeltas(t, sr); got != "hi" {
		t.Errorf("got %q, want %q", got, "hi")
	}
}

func TestStripThinkingBlocks_StreamPassthrough(t *testing.T) {
	t.Parallel()
	inner := &stubProv{streamEv: []provider.Event{
		{Type: provider.EventContentDelta, ContentDelta: "hello "},
		{Type: provider.EventContentDelta, ContentDelta: "world"},
		{Type: provider.EventMessageStop},
	}}
	p := provider.StripThinkingBlocks(inner)
	sr, err := p.Stream(context.Background(), provider.Request{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer sr.Close()
	if got := collectDeltas(t, sr); got != "hello world" {
		t.Errorf("got %q", got)
	}
}

func TestStripThinkingBlocks_StreamCloseStraddlesDeltas(t *testing.T) {
	t.Parallel()
	inner := &stubProv{streamEv: []provider.Event{
		{Type: provider.EventContentDelta, ContentDelta: "<think>partial "},
		{Type: provider.EventContentDelta, ContentDelta: "</think>tail"},
		{Type: provider.EventMessageStop},
	}}
	p := provider.StripThinkingBlocks(inner)
	sr, err := p.Stream(context.Background(), provider.Request{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer sr.Close()
	if got := collectDeltas(t, sr); got != "tail" {
		t.Errorf("got %q, want %q", got, "tail")
	}
}

func TestStripThinkingBlocks_StreamCloseTagSplitAcrossDeltas(t *testing.T) {
	t.Parallel()
	// The closing `</think>` is itself split across two deltas.
	inner := &stubProv{streamEv: []provider.Event{
		{Type: provider.EventContentDelta, ContentDelta: "<think>secret</th"},
		{Type: provider.EventContentDelta, ContentDelta: "ink>done"},
		{Type: provider.EventMessageStop},
	}}
	p := provider.StripThinkingBlocks(inner)
	sr, err := p.Stream(context.Background(), provider.Request{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer sr.Close()
	if got := collectDeltas(t, sr); got != "done" {
		t.Errorf("got %q, want %q", got, "done")
	}
}

func TestStripThinkingBlocks_StreamUnclosedBlockDroppedAtStop(t *testing.T) {
	t.Parallel()
	inner := &stubProv{streamEv: []provider.Event{
		{Type: provider.EventContentDelta, ContentDelta: "<think>truncated reasoning never closed"},
		{Type: provider.EventMessageStop},
	}}
	p := provider.StripThinkingBlocks(inner)
	sr, err := p.Stream(context.Background(), provider.Request{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer sr.Close()
	if got := collectDeltas(t, sr); got != "" {
		t.Errorf("got %q, want empty (open block dropped)", got)
	}
}

func TestStripThinkingBlocks_StreamOpenTagSplitAcrossDeltas(t *testing.T) {
	t.Parallel()
	inner := &stubProv{streamEv: []provider.Event{
		{Type: provider.EventContentDelta, ContentDelta: "before<thi"},
		{Type: provider.EventContentDelta, ContentDelta: "nk>secret</think>after"},
		{Type: provider.EventMessageStop},
	}}
	p := provider.StripThinkingBlocks(inner)
	sr, err := p.Stream(context.Background(), provider.Request{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer sr.Close()
	if got := collectDeltas(t, sr); got != "beforeafter" {
		t.Errorf("got %q, want %q", got, "beforeafter")
	}
}

func TestStripThinkingBlocks_StreamPreTagBytesFlushedOnStop(t *testing.T) {
	t.Parallel()
	// A trailing partial that looks like it might be a thinking tag
	// (so the wrapper holds it back) but turns out to be just text;
	// must be flushed on EventMessageStop.
	inner := &stubProv{streamEv: []provider.Event{
		{Type: provider.EventContentDelta, ContentDelta: "answer<th"},
		{Type: provider.EventMessageStop},
	}}
	p := provider.StripThinkingBlocks(inner)
	sr, err := p.Stream(context.Background(), provider.Request{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer sr.Close()
	if got := collectDeltas(t, sr); got != "answer<th" {
		t.Errorf("got %q, want %q", got, "answer<th")
	}
}

func TestStripThinkingBlocks_StreamMultipleBlocks(t *testing.T) {
	t.Parallel()
	inner := &stubProv{streamEv: []provider.Event{
		{Type: provider.EventContentDelta, ContentDelta: "<think>a</think>real<thinking>b</thinking>more"},
		{Type: provider.EventMessageStop},
	}}
	p := provider.StripThinkingBlocks(inner)
	sr, err := p.Stream(context.Background(), provider.Request{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer sr.Close()
	if got := collectDeltas(t, sr); got != "realmore" {
		t.Errorf("got %q, want %q", got, "realmore")
	}
}

// thinkingParts returns the reasoning text of every thinking part on m.
func thinkingParts(m schema.Message) []string {
	var out []string
	for _, p := range m.Content {
		if p.Type == schema.ContentTypeThinking {
			out = append(out, p.Text)
		}
	}
	return out
}

func TestExtractThinkingBlocks_Generate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		in         string
		wantText   string
		wantThinks []string
	}{
		{"single block", "<think>reasoning</think>answer", "answer", []string{"reasoning"}},
		{"passthrough", "no blocks here", "no blocks here", nil},
		{"mixed blocks", "<think>x</think>actual<thinking>y</thinking>more", "actualmore", []string{"x", "y"}},
		{"multiline", "<think>line1\nline2</think>final", "final", []string{"line1\nline2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := provider.ExtractThinkingBlocks(&stubProv{
				resp: &provider.Response{Message: schema.AssistantMessage(tc.in)},
			})
			resp, err := p.Generate(context.Background(), provider.Request{})
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			// Text identical to what StripThinkingBlocks produces.
			if got := resp.Message.Text(); got != tc.wantText {
				t.Errorf("Text() = %q, want %q", got, tc.wantText)
			}
			got := thinkingParts(resp.Message)
			if len(got) != len(tc.wantThinks) {
				t.Fatalf("thinking parts = %v, want %v", got, tc.wantThinks)
			}
			for i := range got {
				if got[i] != tc.wantThinks[i] {
					t.Errorf("thinking[%d] = %q, want %q", i, got[i], tc.wantThinks[i])
				}
			}
		})
	}
}

// TestExtractThinkingBlocks_StreamFinalMessage verifies that when the
// provider emits a final Message on stop, its reasoning is moved into a
// thinking part while the live deltas stay clean.
func TestExtractThinkingBlocks_StreamFinalMessage(t *testing.T) {
	t.Parallel()
	inner := &stubProv{streamEv: []provider.Event{
		{Type: provider.EventContentDelta, ContentDelta: "<think>x</think>hi"},
		{Type: provider.EventMessageStop, Message: &schema.Message{
			Role:    schema.RoleAssistant,
			Content: []schema.ContentPart{schema.TextPart("<think>x</think>hi")},
		}},
	}}
	p := provider.ExtractThinkingBlocks(inner)
	sr, err := p.Stream(context.Background(), provider.Request{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer sr.Close()

	var deltas string
	var final *schema.Message
	for {
		ev, err := sr.Recv(context.Background())
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		switch ev.Type {
		case provider.EventContentDelta:
			deltas += ev.ContentDelta
		case provider.EventMessageStop:
			final = ev.Message
		}
	}
	if deltas != "hi" {
		t.Errorf("live deltas = %q, want %q", deltas, "hi")
	}
	if final == nil {
		t.Fatal("no final message")
	}
	if got := final.Text(); got != "hi" {
		t.Errorf("final Text() = %q, want %q", got, "hi")
	}
	if th := thinkingParts(*final); len(th) != 1 || th[0] != "x" {
		t.Errorf("final thinking = %v, want [x]", th)
	}
}

func TestStripThinkingBlocks_PassesNonTextPartsUntouched(t *testing.T) {
	t.Parallel()
	inner := &stubProv{resp: &provider.Response{Message: schema.Message{
		Role: schema.RoleAssistant,
		Content: []schema.ContentPart{
			schema.TextPart("<think>x</think>hello"),
			schema.ImagePartURL("http://example.com/img.png"),
		},
	}}}
	p := provider.StripThinkingBlocks(inner)
	resp, err := p.Generate(context.Background(), provider.Request{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(resp.Message.Content) != 2 {
		t.Fatalf("Content len = %d", len(resp.Message.Content))
	}
	if resp.Message.Content[0].Text != "hello" {
		t.Errorf("text = %q", resp.Message.Content[0].Text)
	}
	if resp.Message.Content[1].Image == nil ||
		resp.Message.Content[1].Image.URL != "http://example.com/img.png" {
		t.Errorf("image part mangled: %+v", resp.Message.Content[1])
	}
}

func TestStripThinkingBlocks_PreservesCJK(t *testing.T) {
	t.Parallel()
	// CJK content outside think blocks must not be touched.
	const in = "<think>internal reasoning</think>你好，世界"
	const want = "你好，世界"
	p := provider.StripThinkingBlocks(&stubProv{
		resp: &provider.Response{Message: schema.AssistantMessage(in)},
	})
	resp, err := p.Generate(context.Background(), provider.Request{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got := resp.Message.Text(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Regression (audit low): in strip mode the terminal MessageStop.Message must
// also have <think> blocks removed. A provider that populates a final Message
// used to leak the reasoning through it even though the live deltas were
// stripped.
func TestStripThinkingBlocks_StripsMessageStopMessage(t *testing.T) {
	t.Parallel()
	final := &schema.Message{
		Role:    schema.RoleAssistant,
		Content: []schema.ContentPart{schema.TextPart("<think>secret</think>visible answer")},
	}
	inner := &stubProv{streamEv: []provider.Event{
		{Type: provider.EventContentDelta, ContentDelta: "visible answer"},
		{Type: provider.EventMessageStop, Message: final},
	}}
	p := provider.StripThinkingBlocks(inner)
	sr, err := p.Stream(context.Background(), provider.Request{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer sr.Close()
	var stopMsg *schema.Message
	for {
		ev, err := sr.Recv(context.Background())
		if err != nil {
			break
		}
		if ev.Type == provider.EventMessageStop {
			stopMsg = ev.Message
		}
	}
	if stopMsg == nil {
		t.Fatal("no MessageStop with Message observed")
	}
	if got := stopMsg.Text(); got != "visible answer" {
		t.Errorf("MessageStop.Message.Text() = %q, want %q (reasoning must be stripped)", got, "visible answer")
	}
	// The provider's original message must not be mutated.
	if final.Text() != "<think>secret</think>visible answer" {
		t.Errorf("the inner provider's Message was mutated: %q", final.Text())
	}
}
