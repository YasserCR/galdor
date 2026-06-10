package provider

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/YasserCR/galdor/pkg/schema"
)

// StreamReader iterates events produced by a streaming generation call.
//
// Usage:
//
//	for {
//	    ev, err := r.Recv(ctx)
//	    if errors.Is(err, io.EOF) {
//	        break
//	    }
//	    if err != nil {
//	        return err
//	    }
//	    // handle ev
//	}
//
// Recv returns io.EOF after the terminal MessageStop event has been
// delivered. Close releases adapter resources and is safe to call after
// Recv has returned io.EOF or any error.
type StreamReader interface {
	Recv(ctx context.Context) (Event, error)
	Close() error
}

// EventType discriminates the variants of a stream Event.
type EventType string

const (
	// EventMessageStart is emitted exactly once at the start of a stream
	// and carries the initial model and (often empty) Usage. Adapters
	// that cannot fill these fields may still emit the event with zero
	// values to mark the start.
	EventMessageStart EventType = "message_start"

	// EventContentDelta is emitted for each text fragment.
	EventContentDelta EventType = "content_delta"

	// EventToolCallDelta is emitted as a tool call is built up. Adapters
	// MUST set ToolCall.ID consistently across all deltas belonging to the
	// same call so consumers can join them.
	EventToolCallDelta EventType = "tool_call_delta"

	// EventMessageStop is emitted exactly once at the end of a stream and
	// carries the final StopReason, Usage and (if surfaced) the assembled
	// assistant Message.
	EventMessageStop EventType = "message_stop"
)

// Event is a single item produced by a StreamReader. The active fields
// depend on Type.
type Event struct {
	Type EventType

	// ContentDelta is the next text fragment, when Type == EventContentDelta.
	ContentDelta string

	// ToolCallDelta is the next tool-call fragment, when
	// Type == EventToolCallDelta. ID identifies the tool call; Name is
	// set on the first delta for that call; ArgumentsDelta is appended to
	// the running raw-JSON arguments by the consumer.
	ToolCallDelta *ToolCallDelta

	// StopReason is set on EventMessageStop.
	StopReason schema.StopReason

	// Usage is set on EventMessageStart (initial estimate, may be empty)
	// and EventMessageStop (final reported usage).
	Usage schema.Usage

	// Message is the assembled assistant Message, set on EventMessageStop
	// when the adapter surfaces it. Consumers that build their own buffer
	// (e.g., via CollectStream) may ignore this and rely on the
	// accumulated deltas.
	Message *schema.Message

	// Model is set on EventMessageStart.
	Model string
}

// ToolCallDelta is the streaming counterpart of schema.ToolCall: each delta
// extends the call identified by ID.
type ToolCallDelta struct {
	ID             string
	Name           string
	ArgumentsDelta string
}

// CollectStream consumes r to completion and assembles a single Response.
// It is the canonical bridge between streaming and non-streaming consumers
// and is what Provider.Generate is allowed to call internally when the
// underlying transport is stream-only.
//
// The returned Response has Message.Content set to a single text part with
// the concatenated content deltas, plus any tool calls assembled from
// EventToolCallDelta events. Errors from Recv are returned verbatim.
// CollectStream always invokes r.Close before returning.
func CollectStream(ctx context.Context, r StreamReader) (*Response, error) {
	if r == nil {
		return nil, errors.New("CollectStream: nil StreamReader")
	}
	defer func() { _ = r.Close() }()

	var (
		text       strings.Builder
		toolByID   = map[string]*toolBuilder{}
		toolOrder  []string
		stopReason schema.StopReason
		usage      schema.Usage
		model      string
		thinking   []schema.ContentPart
	)

	for {
		ev, err := r.Recv(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}

		switch ev.Type {
		case EventMessageStart:
			if ev.Model != "" {
				model = ev.Model
			}
			if ev.Usage != (schema.Usage{}) {
				usage = ev.Usage
			}
		case EventContentDelta:
			text.WriteString(ev.ContentDelta)
		case EventToolCallDelta:
			if ev.ToolCallDelta == nil || ev.ToolCallDelta.ID == "" {
				continue
			}
			d := ev.ToolCallDelta
			tb, ok := toolByID[d.ID]
			if !ok {
				tb = &toolBuilder{id: d.ID}
				toolByID[d.ID] = tb
				toolOrder = append(toolOrder, d.ID)
			}
			if d.Name != "" {
				tb.name = d.Name
			}
			if d.ArgumentsDelta != "" {
				tb.args.WriteString(d.ArgumentsDelta)
			}
		case EventMessageStop:
			stopReason = ev.StopReason
			if ev.Usage != (schema.Usage{}) {
				usage = ev.Usage
			}
			if ev.Model != "" {
				model = ev.Model
			}
			// Streamed reasoning rides the terminal Message as thinking
			// part(s) carrying the signature; the content deltas carry
			// only the answer text. Preserve them so CollectStream — the
			// canonical bridge to non-streaming consumers — doesn't
			// silently drop the model's reasoning (v0.6.0's headline
			// capability).
			if ev.Message != nil {
				for _, part := range ev.Message.Content {
					if part.Type == schema.ContentTypeThinking {
						thinking = append(thinking, part)
					}
				}
			}
		}
	}

	msg := schema.Message{Role: schema.RoleAssistant}
	// Thinking parts precede the text answer, matching how providers
	// order an assistant turn that carries both.
	msg.Content = append(msg.Content, thinking...)
	if s := text.String(); s != "" {
		msg.Content = append(msg.Content, schema.TextPart(s))
	}
	for _, id := range toolOrder {
		tb := toolByID[id]
		msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
			ID:        tb.id,
			Name:      tb.name,
			Arguments: []byte(tb.args.String()),
		})
	}

	return &Response{
		Message:    msg,
		StopReason: stopReason,
		Usage:      usage,
		Model:      model,
	}, nil
}

type toolBuilder struct {
	id   string
	name string
	args strings.Builder
}
