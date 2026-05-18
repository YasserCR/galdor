package google

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

// Stream implements provider.Provider. It calls streamGenerateContent
// with alt=sse so the response is server-sent events; each frame is a
// generateResponse-shaped JSON object describing the latest candidate
// delta plus (on the terminal frame) finishReason and usageMetadata.
func (p *Provider) Stream(ctx context.Context, req provider.Request) (provider.StreamReader, error) {
	wire, err := buildRequest(req)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}

	httpReq, err := p.newRequest(ctx, http.MethodPost, modelPath(req.Model, "streamGenerateContent")+"?alt=sse", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("accept", "text/event-stream")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, normalizeHTTPError(resp)
	}

	return &streamReader{
		body:    resp.Body,
		scanner: newSSEScanner(resp.Body),
		toolIdx: map[string]int{},
	}, nil
}

// streamReader is the StreamReader returned by Stream.
//
// pending is a small FIFO drained before the next SSE frame is read; a
// single frame can fan out into MessageStart + one or more content/tool
// deltas.
type streamReader struct {
	body       io.ReadCloser
	scanner    *sseScanner
	toolIdx    map[string]int // function name -> running part index for ID synthesis
	model      string
	usage      schema.Usage
	stopReason schema.StopReason
	started    bool
	stopped    bool
	closed     bool
	pending    []provider.Event
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

		line, ok, err := r.scanner.nextLine()
		if err != nil {
			return provider.Event{}, err
		}
		if !ok {
			// Gemini's SSE stream has no [DONE] sentinel; the connection
			// just closes once the final frame is sent. Synthesize the
			// terminal MessageStop from accumulated state if we haven't
			// already.
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

		var frame generateResponse
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			// Skip malformed lines without surfacing transport hiccups.
			continue
		}
		r.handleFrame(&frame)
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

// handleFrame maps a single decoded Gemini streaming frame into 0..N
// events appended to r.pending.
func (r *streamReader) handleFrame(f *generateResponse) {
	if f.ModelVersion != "" {
		r.model = f.ModelVersion
	}
	if f.UsageMetadata.PromptTokenCount > 0 || f.UsageMetadata.CandidatesTokenCount > 0 ||
		f.UsageMetadata.CachedContentTokenCount > 0 || f.UsageMetadata.ThoughtsTokenCount > 0 {
		r.usage = usageFromWire(f.UsageMetadata)
	}

	if !r.started {
		r.started = true
		r.pending = append(r.pending, provider.Event{
			Type:  provider.EventMessageStart,
			Model: r.model,
		})
	}

	if len(f.Candidates) == 0 {
		return
	}
	c := f.Candidates[0]
	for _, p := range c.Content.Parts {
		switch {
		case p.FunctionCall != nil:
			idx := r.toolIdx[p.FunctionCall.Name]
			r.toolIdx[p.FunctionCall.Name] = idx + 1
			id := synthToolID(p.FunctionCall.Name, idx)
			r.pending = append(r.pending, provider.Event{
				Type: provider.EventToolCallDelta,
				ToolCallDelta: &provider.ToolCallDelta{
					ID:             id,
					Name:           p.FunctionCall.Name,
					ArgumentsDelta: string(p.FunctionCall.Args),
				},
			})
		case p.Text != "" && !p.Thought:
			r.pending = append(r.pending, provider.Event{
				Type:         provider.EventContentDelta,
				ContentDelta: p.Text,
			})
		}
	}
	if c.FinishReason != "" {
		r.stopReason = normalizeFinishReason(c.FinishReason)
	}
}

// sseScanner is a minimal SSE parser; Gemini uses one `data: <json>`
// frame per chunk separated by blank lines. event: headers are not used
// by Gemini but are accepted defensively.
type sseScanner struct {
	s *bufio.Scanner
}

func newSSEScanner(r io.Reader) *sseScanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	return &sseScanner{s: sc}
}

// nextLine returns the next `data:` payload as a string. Multi-line
// data: payloads are joined with '\n'. Returns ok=false with err=nil at
// EOF.
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
