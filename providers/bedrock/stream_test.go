package bedrock

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// newTestStreamReader builds a streamReader wired to a caller-controlled
// channel. The Bedrock SDK's binary Event Stream encoding makes
// end-to-end stream testing via httptest impractical; instead we drive
// the iterator with synthetic typed events, which is what the SDK would
// deliver after parsing the wire format.
func newTestStreamReader(events chan brtypes.ConverseStreamOutput) *streamReader {
	return &streamReader{
		events:    events,
		toolByIdx: map[int32]*toolBlockState{},
	}
}

func drain(t *testing.T, r *streamReader) []provider.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var out []provider.Event
	for {
		ev, err := r.Recv(ctx)
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		out = append(out, ev)
	}
}

func TestStream_TextHappyPath(t *testing.T) {
	t.Parallel()
	ch := make(chan brtypes.ConverseStreamOutput, 8)
	ch <- &brtypes.ConverseStreamOutputMemberMessageStart{
		Value: brtypes.MessageStartEvent{Role: brtypes.ConversationRoleAssistant},
	}
	ch <- &brtypes.ConverseStreamOutputMemberContentBlockDelta{
		Value: brtypes.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(0),
			Delta:             &brtypes.ContentBlockDeltaMemberText{Value: "Hello, "},
		},
	}
	ch <- &brtypes.ConverseStreamOutputMemberContentBlockDelta{
		Value: brtypes.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(0),
			Delta:             &brtypes.ContentBlockDeltaMemberText{Value: "world!"},
		},
	}
	ch <- &brtypes.ConverseStreamOutputMemberMessageStop{
		Value: brtypes.MessageStopEvent{StopReason: brtypes.StopReasonEndTurn},
	}
	ch <- &brtypes.ConverseStreamOutputMemberMetadata{
		Value: brtypes.ConverseStreamMetadataEvent{
			Usage: &brtypes.TokenUsage{InputTokens: aws.Int32(10), OutputTokens: aws.Int32(5)},
		},
	}
	close(ch)

	r := newTestStreamReader(ch)
	defer r.Close()
	events := drain(t, r)

	if len(events) < 4 {
		t.Fatalf("want >= 4 events, got %d: %+v", len(events), events)
	}
	if events[0].Type != provider.EventMessageStart {
		t.Errorf("first event = %q, want MessageStart", events[0].Type)
	}

	var text string
	var stop *provider.Event
	for i := range events {
		switch events[i].Type {
		case provider.EventContentDelta:
			text += events[i].ContentDelta
		case provider.EventMessageStop:
			stop = &events[i]
		}
	}
	if text != "Hello, world!" {
		t.Errorf("text = %q", text)
	}
	if stop == nil {
		t.Fatal("no MessageStop emitted")
	}
	if stop.StopReason != schema.StopReasonEndTurn {
		t.Errorf("StopReason = %q", stop.StopReason)
	}
	if stop.Usage.InputTokens != 10 || stop.Usage.OutputTokens != 5 {
		t.Errorf("Usage = %+v (expected usage merged from Metadata event)", stop.Usage)
	}
}

func TestStream_ToolUseAssembled(t *testing.T) {
	t.Parallel()
	ch := make(chan brtypes.ConverseStreamOutput, 8)
	ch <- &brtypes.ConverseStreamOutputMemberMessageStart{
		Value: brtypes.MessageStartEvent{Role: brtypes.ConversationRoleAssistant},
	}
	ch <- &brtypes.ConverseStreamOutputMemberContentBlockStart{
		Value: brtypes.ContentBlockStartEvent{
			ContentBlockIndex: aws.Int32(0),
			Start: &brtypes.ContentBlockStartMemberToolUse{
				Value: brtypes.ToolUseBlockStart{
					ToolUseId: aws.String("tu_1"),
					Name:      aws.String("weather"),
				},
			},
		},
	}
	ch <- &brtypes.ConverseStreamOutputMemberContentBlockDelta{
		Value: brtypes.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(0),
			Delta: &brtypes.ContentBlockDeltaMemberToolUse{
				Value: brtypes.ToolUseBlockDelta{Input: aws.String(`{"city":`)},
			},
		},
	}
	ch <- &brtypes.ConverseStreamOutputMemberContentBlockDelta{
		Value: brtypes.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(0),
			Delta: &brtypes.ContentBlockDeltaMemberToolUse{
				Value: brtypes.ToolUseBlockDelta{Input: aws.String(`"Quito"}`)},
			},
		},
	}
	ch <- &brtypes.ConverseStreamOutputMemberContentBlockStop{
		Value: brtypes.ContentBlockStopEvent{ContentBlockIndex: aws.Int32(0)},
	}
	ch <- &brtypes.ConverseStreamOutputMemberMessageStop{
		Value: brtypes.MessageStopEvent{StopReason: brtypes.StopReasonToolUse},
	}
	close(ch)

	r := newTestStreamReader(ch)
	defer r.Close()
	resp, err := provider.CollectStream(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d", len(resp.Message.ToolCalls))
	}
	tc := resp.Message.ToolCalls[0]
	if tc.ID != "tu_1" || tc.Name != "weather" {
		t.Errorf("tool call = %+v", tc)
	}
	if string(tc.Arguments) != `{"city":"Quito"}` {
		t.Errorf("Arguments = %s", tc.Arguments)
	}
	if resp.StopReason != schema.StopReasonToolUse {
		t.Errorf("StopReason = %q", resp.StopReason)
	}
}

func TestStream_ContextCanceled(t *testing.T) {
	t.Parallel()
	ch := make(chan brtypes.ConverseStreamOutput) // never closed
	r := newTestStreamReader(ch)
	defer r.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := r.Recv(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestStream_CloseIsIdempotent(t *testing.T) {
	t.Parallel()
	ch := make(chan brtypes.ConverseStreamOutput)
	r := newTestStreamReader(ch)
	if err := r.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	// Recv after Close must return io.EOF immediately.
	_, err := r.Recv(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Errorf("Recv after Close = %v, want io.EOF", err)
	}
}

func TestStream_MessageStartSynthesizedFromContentBlock(t *testing.T) {
	t.Parallel()
	// Some Bedrock-served models start with a ContentBlockStart (text)
	// without a separate MessageStart frame. The adapter should still
	// synthesize a MessageStart so consumers see a consistent prefix.
	ch := make(chan brtypes.ConverseStreamOutput, 4)
	ch <- &brtypes.ConverseStreamOutputMemberContentBlockDelta{
		Value: brtypes.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(0),
			Delta:             &brtypes.ContentBlockDeltaMemberText{Value: "x"},
		},
	}
	ch <- &brtypes.ConverseStreamOutputMemberMessageStop{
		Value: brtypes.MessageStopEvent{StopReason: brtypes.StopReasonEndTurn},
	}
	close(ch)

	r := newTestStreamReader(ch)
	defer r.Close()
	events := drain(t, r)
	if len(events) == 0 || events[0].Type != provider.EventMessageStart {
		t.Errorf("first event should be MessageStart, got %+v", events)
	}
}

// TestStream_SurfacesReasoning verifies streamed reasoningContent deltas
// are kept off the live content stream and delivered as a thinking part
// (with signature) on the terminal MessageStop.
func TestStream_SurfacesReasoning(t *testing.T) {
	t.Parallel()
	ch := make(chan brtypes.ConverseStreamOutput, 8)
	ch <- &brtypes.ConverseStreamOutputMemberMessageStart{
		Value: brtypes.MessageStartEvent{Role: brtypes.ConversationRoleAssistant},
	}
	ch <- &brtypes.ConverseStreamOutputMemberContentBlockDelta{
		Value: brtypes.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(0),
			Delta: &brtypes.ContentBlockDeltaMemberReasoningContent{
				Value: &brtypes.ReasoningContentBlockDeltaMemberText{Value: "let me "},
			},
		},
	}
	ch <- &brtypes.ConverseStreamOutputMemberContentBlockDelta{
		Value: brtypes.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(0),
			Delta: &brtypes.ContentBlockDeltaMemberReasoningContent{
				Value: &brtypes.ReasoningContentBlockDeltaMemberText{Value: "reason"},
			},
		},
	}
	ch <- &brtypes.ConverseStreamOutputMemberContentBlockDelta{
		Value: brtypes.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(0),
			Delta: &brtypes.ContentBlockDeltaMemberReasoningContent{
				Value: &brtypes.ReasoningContentBlockDeltaMemberSignature{Value: "sig123"},
			},
		},
	}
	ch <- &brtypes.ConverseStreamOutputMemberContentBlockDelta{
		Value: brtypes.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(1),
			Delta:             &brtypes.ContentBlockDeltaMemberText{Value: "answer"},
		},
	}
	ch <- &brtypes.ConverseStreamOutputMemberMessageStop{
		Value: brtypes.MessageStopEvent{StopReason: brtypes.StopReasonEndTurn},
	}
	close(ch)

	r := newTestStreamReader(ch)
	defer r.Close()
	events := drain(t, r)

	var live string
	var stopMsg *schema.Message
	for i := range events {
		switch events[i].Type {
		case provider.EventContentDelta:
			live += events[i].ContentDelta
		case provider.EventMessageStop:
			stopMsg = events[i].Message
		}
	}
	if live != "answer" {
		t.Errorf("live stream = %q, want clean %q", live, "answer")
	}
	if stopMsg == nil || len(stopMsg.Content) != 1 ||
		stopMsg.Content[0].Type != schema.ContentTypeThinking ||
		stopMsg.Content[0].Text != "let me reason" || stopMsg.Content[0].Signature != "sig123" {
		t.Fatalf("reasoning part wrong: %+v", stopMsg)
	}
}
