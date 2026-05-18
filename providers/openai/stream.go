package openai

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
// connection to /v1/chat/completions and decodes each `data: {...}` frame
// into galdor provider.Event values. The terminal sentinel `data: [DONE]`
// is converted into an EventMessageStop and then io.EOF on the next Recv.
func (p *Provider) Stream(ctx context.Context, req provider.Request) (provider.StreamReader, error) {
	wire, err := buildRequest(req, true)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}

	httpReq, err := p.newRequest(ctx, http.MethodPost, "/chat/completions", bytes.NewReader(body))
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
		body:      resp.Body,
		scanner:   newSSEScanner(resp.Body),
		toolByIdx: map[int]*toolState{},
	}, nil
}

// streamReader is the StreamReader returned by Stream.
//
// pending is a small FIFO that holds events produced by a single chunk.
// A chunk can fan out into more than one Event (e.g., MessageStart +
// ToolCallDelta if the first chunk carries tool data), so Recv drains
// pending before parsing the next chunk.
type streamReader struct {
	body       io.ReadCloser
	scanner    *sseScanner
	toolByIdx  map[int]*toolState // by tool_calls[i].index
	model      string
	usage      schema.Usage
	stopReason schema.StopReason
	started    bool
	stopped    bool
	closed     bool
	pending    []provider.Event
}

// toolState tracks the resolved ID and name for a tool_calls[].index across
// streaming deltas.
type toolState struct {
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

		// Drain any events buffered from the previous chunk first.
		if len(r.pending) > 0 {
			ev := r.pending[0]
			r.pending = r.pending[1:]
			return ev, nil
		}

		line, ok, err := r.scanner.nextLine()
		if err != nil {
			return provider.Event{}, err
		}
		if !ok {
			// EOF before [DONE]. OpenAI itself terminates the stream
			// with `data: [DONE]`, but several OpenAI-compatible
			// providers (MiniMax among them) just close the connection
			// once the usage chunk has been sent. Synthesize the
			// terminal MessageStop from the accumulated state so
			// downstream consumers see a consistent end-of-stream.
			if !r.stopped {
				r.stopped = true
				return provider.Event{
					Type:       provider.EventMessageStop,
					StopReason: r.stopReason,
					Usage:      r.usage,
					Model:      r.model,
				}, nil
			}
			return provider.Event{}, io.EOF
		}

		// SSE terminator: data: [DONE]
		//
		// We always defer MessageStop to here: with
		// stream_options.include_usage=true the final usage chunk arrives
		// AFTER the chunk that carries finish_reason, so emitting on
		// finish_reason would surface zero usage on the terminal event.
		if line == "[DONE]" {
			if r.stopped {
				return provider.Event{}, io.EOF
			}
			r.stopped = true
			return provider.Event{
				Type:       provider.EventMessageStop,
				StopReason: r.stopReason,
				Usage:      r.usage,
				Model:      r.model,
			}, nil
		}

		var chunk chatChunk
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			// Skip lines that fail to parse — be permissive about transport
			// hiccups but log nothing (the package has no logger).
			continue
		}

		r.handleChunk(&chunk)
		// Loop back: any events produced are now in r.pending.
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

// handleChunk maps a single decoded chat completion chunk into zero or
// more galdor provider.Events, appending them to r.pending in emission
// order. The first chunk with a non-empty model produces
// EventMessageStart; content/tool deltas produce their corresponding
// events; finish_reason is recorded but not emitted (see Recv).
func (r *streamReader) handleChunk(c *chatChunk) {
	if c.Model != "" {
		r.model = c.Model
	}
	if c.Usage != nil {
		r.usage = usageFromWire(*c.Usage)
	}

	// First chunk: synthesize MessageStart. OpenAI's stream has no
	// dedicated start frame.
	if !r.started && (r.model != "" || len(c.Choices) > 0) {
		r.started = true
		r.pending = append(r.pending, provider.Event{
			Type:  provider.EventMessageStart,
			Model: r.model,
		})
	}

	if len(c.Choices) == 0 {
		return
	}
	ch := c.Choices[0]

	if ch.Delta.Content != "" {
		r.pending = append(r.pending, provider.Event{
			Type:         provider.EventContentDelta,
			ContentDelta: ch.Delta.Content,
		})
	}

	for i := range ch.Delta.ToolCalls {
		td := ch.Delta.ToolCalls[i]
		ts := r.touchToolState(&td)
		r.pending = append(r.pending, provider.Event{
			Type: provider.EventToolCallDelta,
			ToolCallDelta: &provider.ToolCallDelta{
				ID:             ts.id,
				Name:           td.Function.Name,
				ArgumentsDelta: td.Function.Arguments,
			},
		})
	}

	if ch.FinishReason != "" {
		r.stopReason = normalizeFinishReason(ch.FinishReason)
	}
}

// touchToolState ensures a toolState exists for td.Index (defaulting to 0)
// and updates it with any new ID or Name from this delta. Returns the
// current state.
func (r *streamReader) touchToolState(td *wireToolCall) *toolState {
	idx := 0
	if td.Index != nil {
		idx = *td.Index
	}
	ts, ok := r.toolByIdx[idx]
	if !ok {
		ts = &toolState{}
		r.toolByIdx[idx] = ts
	}
	if td.ID != "" {
		ts.id = td.ID
	}
	if td.Function.Name != "" {
		ts.name = td.Function.Name
	}
	return ts
}

// sseScanner is a minimal SSE parser tailored to OpenAI's stream shape,
// which emits one `data: <json>` line per chunk and uses a blank line as
// a frame separator. event: headers are not used by OpenAI but are
// silently accepted if present (for compatible providers that do).
type sseScanner struct {
	s *bufio.Scanner
}

func newSSEScanner(r io.Reader) *sseScanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	return &sseScanner{s: sc}
}

// nextLine returns the JSON payload of the next `data:` frame as a string.
// Multi-line data: payloads are joined with '\n'. Returns ok=false with
// err=nil at EOF.
func (s *sseScanner) nextLine() (string, bool, error) {
	var buf bytes.Buffer
	collected := false
	for s.s.Scan() {
		line := s.s.Text()
		if line == "" {
			if collected {
				return buf.String(), true, nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		// event: headers are accepted but ignored — galdor doesn't need
		// them for OpenAI-shaped streams.
		if strings.HasPrefix(line, "event:") {
			continue
		}
		if strings.HasPrefix(line, "data: ") {
			if collected {
				buf.WriteByte('\n')
			}
			buf.WriteString(line[len("data: "):])
			collected = true
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if collected {
				buf.WriteByte('\n')
			}
			buf.WriteString(strings.TrimPrefix(line, "data:"))
			collected = true
			continue
		}
	}
	if err := s.s.Err(); err != nil {
		return "", false, fmt.Errorf("sse scan: %w", err)
	}
	if collected {
		return buf.String(), true, nil
	}
	return "", false, nil
}
