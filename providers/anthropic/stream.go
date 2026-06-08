package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// Stream implements provider.Provider. It opens a server-sent-events
// connection to /v1/messages and returns a StreamReader that decodes
// Anthropic event types into galdor provider.Event values.
func (p *Provider) Stream(ctx context.Context, req provider.Request) (provider.StreamReader, error) {
	wire, err := buildRequest(req, true)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}

	httpReq, err := p.newRequest(ctx, http.MethodPost, "/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("accept", "text/event-stream")

	// Body ownership transfers to streamReader, whose Close() closes it.
	// bodyclose can't trace that, so the linter is suppressed here.
	resp, err := p.httpClient.Do(httpReq) //nolint:bodyclose // closed by streamReader.Close
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, normalizeHTTPError(resp)
	}

	return &streamReader{
		body:    resp.Body,
		scanner: newSSEScanner(resp.Body),
		blocks:  map[int]*blockState{},
	}, nil
}

// streamReader is the StreamReader returned by Stream.
type streamReader struct {
	body    io.ReadCloser
	scanner *sseScanner
	blocks  map[int]*blockState // by content_block index
	model   string
	usage   schema.Usage
	stopped bool
	closed  bool

	// reasoning accumulates extended-thinking deltas so they can be
	// attached to the terminal MessageStop as a thinking part (the live
	// stream stays clean — thinking deltas are not forwarded).
	reasoning strings.Builder
	signature strings.Builder
}

// stopMessage builds the terminal assistant Message carrying any
// accumulated reasoning, or nil when none was streamed. The text itself
// rides the content deltas, so this message holds only the thinking part.
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

// blockState tracks the partial state of a single content block as deltas
// arrive. Anthropic streams one block at a time but the index makes the
// association explicit.
type blockState struct {
	kind     string // "text" or "tool_use"
	toolID   string
	toolName string
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
		ev, ok, err := r.scanner.next(ctx)
		if err != nil {
			return provider.Event{}, err
		}
		if !ok {
			// EOF before a message_delta/message_stop frame — the
			// connection dropped or the response was truncated mid-stream.
			// Synthesize the terminal MessageStop from the accumulated
			// state so consumers that key off EventMessageStop still see
			// the partial usage/model, matching the other adapters.
			if !r.stopped {
				r.stopped = true
				return provider.Event{
					Type:    provider.EventMessageStop,
					Usage:   r.usage,
					Model:   r.model,
					Message: r.stopMessage(),
				}, nil
			}
			return provider.Event{}, io.EOF
		}
		out, emit, err := r.handleEvent(ev)
		if err != nil {
			return provider.Event{}, err
		}
		if emit {
			return out, nil
		}
	}
}

// Close implements provider.StreamReader.
func (r *streamReader) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	return r.body.Close()
}

// handleEvent maps a single decoded SSE frame into either zero or one
// galdor provider.Event. Returning emit=false means "internal state
// updated; ask for the next frame".
func (r *streamReader) handleEvent(ev sseEvent) (provider.Event, bool, error) {
	switch ev.Type {
	case "message_start":
		var p struct {
			Message struct {
				Model string    `json:"model"`
				Usage wireUsage `json:"usage"`
			} `json:"message"`
		}
		if err := json.Unmarshal(ev.Data, &p); err != nil {
			return provider.Event{}, false, err
		}
		r.model = p.Message.Model
		r.usage = usageFromWire(p.Message.Usage)
		return provider.Event{
			Type:  provider.EventMessageStart,
			Model: r.model,
			Usage: r.usage,
		}, true, nil

	case "content_block_start":
		var p struct {
			Index        int `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id,omitempty"`
				Name string `json:"name,omitempty"`
			} `json:"content_block"`
		}
		if err := json.Unmarshal(ev.Data, &p); err != nil {
			return provider.Event{}, false, err
		}
		r.blocks[p.Index] = &blockState{
			kind:     p.ContentBlock.Type,
			toolID:   p.ContentBlock.ID,
			toolName: p.ContentBlock.Name,
		}
		if p.ContentBlock.Type == "tool_use" {
			// Surface the first delta so consumers see the tool name early.
			return provider.Event{
				Type: provider.EventToolCallDelta,
				ToolCallDelta: &provider.ToolCallDelta{
					ID:   p.ContentBlock.ID,
					Name: p.ContentBlock.Name,
				},
			}, true, nil
		}
		return provider.Event{}, false, nil

	case "content_block_delta":
		var p struct {
			Index int `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text,omitempty"`
				PartialJSON string `json:"partial_json,omitempty"`
				Thinking    string `json:"thinking,omitempty"`
				Signature   string `json:"signature,omitempty"`
			} `json:"delta"`
		}
		if err := json.Unmarshal(ev.Data, &p); err != nil {
			return provider.Event{}, false, err
		}
		b := r.blocks[p.Index]
		switch p.Delta.Type {
		case "text_delta":
			return provider.Event{Type: provider.EventContentDelta, ContentDelta: p.Delta.Text}, true, nil
		case "thinking_delta":
			// Accumulate reasoning; do not forward it on the live stream.
			r.reasoning.WriteString(p.Delta.Thinking)
			return provider.Event{}, false, nil
		case "signature_delta":
			r.signature.WriteString(p.Delta.Signature)
			return provider.Event{}, false, nil
		case "input_json_delta":
			if b == nil {
				return provider.Event{}, false, nil
			}
			return provider.Event{
				Type: provider.EventToolCallDelta,
				ToolCallDelta: &provider.ToolCallDelta{
					ID:             b.toolID,
					ArgumentsDelta: p.Delta.PartialJSON,
				},
			}, true, nil
		}
		return provider.Event{}, false, nil

	case "content_block_stop":
		return provider.Event{}, false, nil

	case "message_delta":
		var p struct {
			Delta struct {
				StopReason   string `json:"stop_reason,omitempty"`
				StopSequence string `json:"stop_sequence,omitempty"`
			} `json:"delta"`
			Usage wireUsage `json:"usage"`
		}
		if err := json.Unmarshal(ev.Data, &p); err != nil {
			return provider.Event{}, false, err
		}
		// Anthropic reports output tokens here, in addition to the final
		// message_stop. Track the latest so the terminal event has them.
		u := usageFromWire(p.Usage)
		if u.OutputTokens > 0 {
			r.usage.OutputTokens = u.OutputTokens
		}
		if u.InputTokens > 0 {
			r.usage.InputTokens = u.InputTokens
		}
		if u.CacheCreationTokens > 0 {
			r.usage.CacheCreationTokens = u.CacheCreationTokens
		}
		if u.CacheReadTokens > 0 {
			r.usage.CacheReadTokens = u.CacheReadTokens
		}
		if r.stopped || p.Delta.StopReason == "" {
			return provider.Event{}, false, nil
		}
		r.stopped = true
		return provider.Event{
			Type:       provider.EventMessageStop,
			StopReason: normalizeStopReason(p.Delta.StopReason),
			Usage:      r.usage,
			Model:      r.model,
			Message:    r.stopMessage(),
		}, true, nil

	case "message_stop":
		// Terminal sentinel; if message_delta already emitted MessageStop
		// suppress this. Otherwise emit a minimal stop event.
		if r.stopped {
			return provider.Event{}, false, nil
		}
		r.stopped = true
		return provider.Event{
			Type:    provider.EventMessageStop,
			Usage:   r.usage,
			Model:   r.model,
			Message: r.stopMessage(),
		}, true, nil

	case "ping":
		return provider.Event{}, false, nil

	case "error":
		var p struct {
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(ev.Data, &p)
		kind := kindForType(p.Error.Type)
		if kind == nil {
			kind = provider.ErrServer
		}
		return provider.Event{}, false, provider.Classify(&provider.APIError{
			Provider: providerName,
			Kind:     kind,
			Message:  p.Error.Message,
		})

	default:
		// Unknown events are ignored to stay forward-compatible.
		return provider.Event{}, false, nil
	}
}

// sseEvent is a single Server-Sent-Events frame after parsing.
type sseEvent struct {
	Type string
	Data json.RawMessage
}

// sseScanner is a minimal SSE parser. It joins multi-line "data:" payloads
// until a blank line terminates the event and returns one sseEvent at a
// time.
type sseScanner struct {
	s *bufio.Scanner
}

func newSSEScanner(r io.Reader) *sseScanner {
	sc := bufio.NewScanner(r)
	// Anthropic events fit comfortably in 1 MiB; bump the default buffer
	// to handle long content_block_delta lines.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	return &sseScanner{s: sc}
}

// next reads the next event. The returned ok=false with err=nil means EOF.
func (s *sseScanner) next(_ context.Context) (sseEvent, bool, error) {
	var (
		eventType string
		data      bytes.Buffer
		started   bool
	)
	for s.s.Scan() {
		line := s.s.Text()
		started = true
		if line == "" {
			if eventType == "" && data.Len() == 0 {
				continue
			}
			return sseEvent{Type: eventType, Data: json.RawMessage(data.Bytes())}, true, nil
		}
		if strings.HasPrefix(line, ":") { // SSE comment / heartbeat
			continue
		}
		switch {
		case strings.HasPrefix(line, "event: "):
			eventType = strings.TrimSpace(line[len("event: "):])
		case strings.HasPrefix(line, "data: "):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(line[len("data: "):])
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(line[len("event:"):])
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(line, "data:"))
		}
	}
	if err := s.s.Err(); err != nil {
		return sseEvent{}, false, fmt.Errorf("sse scan: %w", err)
	}
	if started && (eventType != "" || data.Len() > 0) {
		return sseEvent{Type: eventType, Data: json.RawMessage(data.Bytes())}, true, nil
	}
	return sseEvent{}, false, nil
}
