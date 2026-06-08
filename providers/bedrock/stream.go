package bedrock

import (
	"context"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// Stream implements provider.Provider. It opens a ConverseStream call and
// translates the SDK's event stream into galdor provider.Event values.
//
// Bedrock's stream surface gives us discrete typed events
// (MessageStart, ContentBlockStart, ContentBlockDelta, ContentBlockStop,
// MessageStop, Metadata) which map cleanly onto galdor's stream model.
// MessageStop is deferred until after the Metadata event so the
// terminal Event carries final Usage.
func (p *Provider) Stream(ctx context.Context, req provider.Request) (provider.StreamReader, error) {
	convIn, err := buildConverseInput(req)
	if err != nil {
		return nil, err
	}
	in := &bedrockruntime.ConverseStreamInput{
		ModelId:         convIn.ModelId,
		Messages:        convIn.Messages,
		System:          convIn.System,
		InferenceConfig: convIn.InferenceConfig,
		ToolConfig:      convIn.ToolConfig,
	}

	out, err := p.client.ConverseStream(ctx, in)
	if err != nil {
		return nil, normalizeAWSError(err)
	}

	return &streamReader{
		out:       out,
		events:    out.GetStream().Events(),
		toolByIdx: map[int32]*toolBlockState{},
	}, nil
}

// streamReader bridges the SDK's typed event channel to galdor's
// StreamReader iterator.
type streamReader struct {
	out          *bedrockruntime.ConverseStreamOutput
	events       <-chan brtypes.ConverseStreamOutput
	toolByIdx    map[int32]*toolBlockState
	model        string
	usage        schema.Usage
	stopReason   schema.StopReason
	stopBuffered *provider.Event // MessageStop, emitted after Metadata
	pending      []provider.Event
	started      bool
	stopped      bool
	closed       bool

	// reasoning accumulates streamed reasoningContent deltas so they can
	// ride the terminal MessageStop as a thinking part; the live stream
	// stays clean.
	reasoning strings.Builder
	signature strings.Builder
}

// stopMessage builds the terminal assistant Message carrying any
// accumulated reasoning, or nil when none was streamed.
func (r *streamReader) stopMessage() *schema.Message {
	if r.reasoning.Len() == 0 {
		return nil
	}
	return &schema.Message{
		Role: schema.RoleAssistant,
		Content: []schema.ContentPart{{
			Type:      schema.ContentTypeThinking,
			Text:      r.reasoning.String(),
			Signature: r.signature.String(),
		}},
	}
}

// toolBlockState tracks the id+name of a tool_use block across
// ContentBlockStart and subsequent input_json deltas, identified by
// the content block index.
type toolBlockState struct {
	id   string
	name string
}

// Recv implements provider.StreamReader.
func (r *streamReader) Recv(ctx context.Context) (provider.Event, error) {
	if r.closed {
		return provider.Event{}, io.EOF
	}
	for {
		if err := ctx.Err(); err != nil {
			return provider.Event{}, err
		}
		if len(r.pending) > 0 {
			ev := r.pending[0]
			r.pending = r.pending[1:]
			return ev, nil
		}

		select {
		case <-ctx.Done():
			return provider.Event{}, ctx.Err()
		case ev, ok := <-r.events:
			if !ok {
				// Channel closed. Surface any deferred MessageStop, then
				// any stream-level error, then EOF.
				if r.stopBuffered != nil && !r.stopped {
					r.stopped = true
					out := *r.stopBuffered
					r.stopBuffered = nil
					return out, nil
				}
				if r.out != nil {
					if streamErr := r.out.GetStream().Err(); streamErr != nil {
						return provider.Event{}, normalizeAWSError(streamErr)
					}
				}
				if !r.stopped {
					r.stopped = true
					return provider.Event{
						Type:       provider.EventMessageStop,
						StopReason: r.stopReason,
						Usage:      r.usage,
						Model:      r.model,
						Message:    r.stopMessage(),
					}, nil
				}
				return provider.Event{}, io.EOF
			}
			r.handleEvent(ev)
		}
	}
}

// Close implements provider.StreamReader.
func (r *streamReader) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	if r.out == nil {
		return nil
	}
	if s := r.out.GetStream(); s != nil {
		return s.Close()
	}
	return nil
}

// ensureStarted emits EventMessageStart once. Safe to call from every
// event branch so consumers always see a start event even if the
// upstream stream skipped it.
func (r *streamReader) ensureStarted() {
	if !r.started {
		r.started = true
		r.pending = append(r.pending, provider.Event{
			Type:  provider.EventMessageStart,
			Model: r.model,
		})
	}
}

// handleEvent maps a single SDK stream event into 0..N galdor events,
// appending them to r.pending and updating accumulated state.
func (r *streamReader) handleEvent(ev brtypes.ConverseStreamOutput) {
	// Any non-Metadata event implies the stream has begun. Metadata can
	// arrive late; if it does, treat it as terminal-only and don't
	// synthesize a start from it.
	if _, isMeta := ev.(*brtypes.ConverseStreamOutputMemberMetadata); !isMeta {
		r.ensureStarted()
	}

	switch e := ev.(type) {
	case *brtypes.ConverseStreamOutputMemberMessageStart:
		// ensureStarted already handled the emission.
		_ = e

	case *brtypes.ConverseStreamOutputMemberContentBlockStart:
		if e.Value.Start != nil {
			if tu, ok := e.Value.Start.(*brtypes.ContentBlockStartMemberToolUse); ok {
				idx := aws.ToInt32(e.Value.ContentBlockIndex)
				st := &toolBlockState{
					id:   aws.ToString(tu.Value.ToolUseId),
					name: aws.ToString(tu.Value.Name),
				}
				r.toolByIdx[idx] = st
				r.pending = append(r.pending, provider.Event{
					Type: provider.EventToolCallDelta,
					ToolCallDelta: &provider.ToolCallDelta{
						ID:   st.id,
						Name: st.name,
					},
				})
			}
		}

	case *brtypes.ConverseStreamOutputMemberContentBlockDelta:
		if e.Value.Delta == nil {
			return
		}
		switch d := e.Value.Delta.(type) {
		case *brtypes.ContentBlockDeltaMemberText:
			r.pending = append(r.pending, provider.Event{
				Type:         provider.EventContentDelta,
				ContentDelta: d.Value,
			})
		case *brtypes.ContentBlockDeltaMemberReasoningContent:
			// Accumulate reasoning; do not forward it on the live stream.
			switch rc := d.Value.(type) {
			case *brtypes.ReasoningContentBlockDeltaMemberText:
				r.reasoning.WriteString(rc.Value)
			case *brtypes.ReasoningContentBlockDeltaMemberSignature:
				r.signature.WriteString(rc.Value)
			}
		case *brtypes.ContentBlockDeltaMemberToolUse:
			idx := aws.ToInt32(e.Value.ContentBlockIndex)
			st := r.toolByIdx[idx]
			id := ""
			name := ""
			if st != nil {
				id = st.id
				name = st.name
			}
			r.pending = append(r.pending, provider.Event{
				Type: provider.EventToolCallDelta,
				ToolCallDelta: &provider.ToolCallDelta{
					ID:             id,
					Name:           name,
					ArgumentsDelta: aws.ToString(d.Value.Input),
				},
			})
		}

	case *brtypes.ConverseStreamOutputMemberContentBlockStop:
		// nothing to emit; SDK has no per-block close semantics for us

	case *brtypes.ConverseStreamOutputMemberMessageStop:
		// Buffer the stop event so that any Metadata event that arrives
		// after this still gets its Usage merged into the terminal stop.
		r.stopReason = normalizeStopReason(string(e.Value.StopReason))
		stop := provider.Event{
			Type:       provider.EventMessageStop,
			StopReason: r.stopReason,
			Usage:      r.usage,
			Model:      r.model,
			Message:    r.stopMessage(),
		}
		r.stopBuffered = &stop

	case *brtypes.ConverseStreamOutputMemberMetadata:
		if e.Value.Usage != nil {
			r.usage = schema.Usage{
				InputTokens:  int(aws.ToInt32(e.Value.Usage.InputTokens)),
				OutputTokens: int(aws.ToInt32(e.Value.Usage.OutputTokens)),
			}
			if e.Value.Usage.CacheReadInputTokens != nil {
				r.usage.CacheReadTokens = int(*e.Value.Usage.CacheReadInputTokens)
			}
			if e.Value.Usage.CacheWriteInputTokens != nil {
				r.usage.CacheCreationTokens = int(*e.Value.Usage.CacheWriteInputTokens)
			}
		}
		if r.stopBuffered != nil {
			r.stopBuffered.Usage = r.usage
		}
	}
}

// Unknown SDK event types are silently ignored by handleEvent's switch,
// which is the safe forward-compat behavior when the SDK adds new event
// variants.
