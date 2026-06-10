// Package testprovider provides an in-process provider.Provider
// implementation for unit-testing code that depends on an LLM call
// without hitting a real network or burning quota.
//
// Typical use:
//
//	p := testprovider.New(
//	    testprovider.Responses(`{"intent":"buy","amount":42}`),
//	    testprovider.Errors(&provider.APIError{Kind: provider.ErrRateLimited}),
//	)
//	resp, err := myInterpreter(ctx, p, "buy 42 widgets")
//
// The provider is goroutine-safe and supports both Generate and Stream
// against the same scripted sequence. When the script is exhausted, the
// next call returns ErrScriptExhausted so unintended extra calls show
// up as test failures instead of silently wrapping around.
//
// Replaces the inlined stub formerly in examples/provider-interface
// (kept there for didactic purposes; this package is what tests
// should import).
package testprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// ErrScriptExhausted is returned (wrapped) by Generate and Stream
// after the scripted sequence runs out. Match with errors.Is.
var ErrScriptExhausted = errors.New("testprovider: script exhausted")

// Provider is a scripted in-memory provider.Provider implementation.
// Construct with New; the zero value is not useful.
type Provider struct {
	name string
	caps provider.Capabilities

	mu     sync.Mutex
	script []step
	cursor int
	seen   []provider.Request
}

// step is one element of the scripted sequence: exactly one of
// response or err is non-nil.
type step struct {
	response *provider.Response
	err      error
}

// Option configures a Provider during New.
type Option func(*Provider)

// New constructs a Provider with the given options. The default
// capabilities advertise streaming and tool-calling so most tests
// don't need to override them; pass Capabilities(...) when a specific
// capability gate matters to the code under test.
func New(opts ...Option) *Provider {
	p := &Provider{
		name: "test",
		caps: provider.Capabilities{
			Streaming:        true,
			ToolCalling:      true,
			StructuredOutput: true,
			MaxContextTokens: 8192,
		},
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Name sets the provider name returned by Name(). Default "test".
func Name(name string) Option { return func(p *Provider) { p.name = name } }

// Capabilities overrides the default capability set.
func Capabilities(c provider.Capabilities) Option {
	return func(p *Provider) { p.caps = c }
}

// Responses appends scripted text responses. Each entry becomes a
// *provider.Response whose Message.Text() returns the given string,
// StopReason is schema.StopReasonEndTurn, and Usage tracks input and
// output character counts as a rough token proxy.
func Responses(texts ...string) Option {
	return func(p *Provider) {
		for _, t := range texts {
			p.script = append(p.script, step{response: textResponse(t)})
		}
	}
}

// JSONResponses appends scripted responses by JSON-encoding each
// value. Convenient when exercising schema.ParseJSON[T] or future
// JSONOf[T] paths. Panics if a value cannot be marshaled — tests
// should fail loudly on a malformed fixture.
func JSONResponses(values ...any) Option {
	return func(p *Provider) {
		for _, v := range values {
			b, err := json.Marshal(v)
			if err != nil {
				panic(fmt.Sprintf("testprovider: cannot marshal scripted JSON: %v", err))
			}
			p.script = append(p.script, step{response: textResponse(string(b))})
		}
	}
}

// Errors appends scripted errors. Errors are interleaved with
// responses in declaration order, so
//
//	New(Responses("a"), Errors(rl), Responses("b"))
//
// returns "a", then rl, then "b" across three calls.
func Errors(errs ...error) Option {
	return func(p *Provider) {
		for _, e := range errs {
			p.script = append(p.script, step{err: e})
		}
	}
}

// Name implements provider.Provider.
func (p *Provider) Name() string { return p.name }

// Capabilities implements provider.Provider.
func (p *Provider) Capabilities() provider.Capabilities { return p.caps }

// Generate returns the next scripted response or error. The request
// is recorded in the order received and can be retrieved with
// Requests() for assertions.
func (p *Provider) Generate(ctx context.Context, req provider.Request) (*provider.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.seen = append(p.seen, req)
	if p.cursor >= len(p.script) {
		p.mu.Unlock()
		return nil, fmt.Errorf("%w at call %d", ErrScriptExhausted, p.cursor+1)
	}
	s := p.script[p.cursor]
	p.cursor++
	p.mu.Unlock()

	if s.err != nil {
		return nil, s.err
	}
	// Defensive DEEP copy so callers mutating the response — including its
	// Message.Content / ToolCalls slices or ProviderRaw — don't poison later
	// replays of the same script. A shallow `*s.response` shared those slices.
	return cloneResponse(s.response), nil
}

// cloneResponse returns an independent copy of r whose slice-typed fields
// don't alias the original.
func cloneResponse(r *provider.Response) *provider.Response {
	cp := *r
	cp.Message.Content = append([]schema.ContentPart(nil), r.Message.Content...)
	cp.Message.ToolCalls = append([]schema.ToolCall(nil), r.Message.ToolCalls...)
	if r.ProviderRaw != nil {
		cp.ProviderRaw = append([]byte(nil), r.ProviderRaw...)
	}
	return &cp
}

// Stream implements provider.Provider by replaying the next scripted
// step as a synthetic event sequence: MessageStart, one ContentDelta
// per scripted text, MessageStop. Scripted errors are surfaced at the
// Stream call boundary (not mid-stream), matching how most real
// adapters fail.
func (p *Provider) Stream(ctx context.Context, req provider.Request) (provider.StreamReader, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.seen = append(p.seen, req)
	if p.cursor >= len(p.script) {
		p.mu.Unlock()
		return nil, fmt.Errorf("%w at call %d", ErrScriptExhausted, p.cursor+1)
	}
	s := p.script[p.cursor]
	p.cursor++
	p.mu.Unlock()

	if s.err != nil {
		return nil, s.err
	}
	return newSliceStream(s.response, req.Model), nil
}

// Requests returns a snapshot of the requests received so far, in
// order. The returned slice is a copy; the caller may modify it
// without affecting the provider's internal log.
func (p *Provider) Requests() []provider.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]provider.Request, len(p.seen))
	copy(out, p.seen)
	return out
}

// Reset rewinds the script cursor to the beginning and clears the
// recorded request log. Useful when the same provider is reused
// across subtests.
func (p *Provider) Reset() {
	p.mu.Lock()
	p.cursor = 0
	p.seen = nil
	p.mu.Unlock()
}

// Remaining reports how many scripted steps have not yet been
// consumed. Tests can assert this is zero to ensure all canned
// responses were used.
func (p *Provider) Remaining() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.script) - p.cursor
}

func textResponse(text string) *provider.Response {
	return &provider.Response{
		Message: schema.AssistantMessage(text),
		Model:   "test",
		Usage: schema.Usage{
			InputTokens:  0,
			OutputTokens: len(text),
		},
		StopReason: schema.StopReasonEndTurn,
	}
}

// sliceStream replays a Response as a small fixed event sequence.
type sliceStream struct {
	events []provider.Event
	pos    int
}

func newSliceStream(resp *provider.Response, model string) *sliceStream {
	if model == "" {
		model = "test"
	}
	text := resp.Message.Text()
	return &sliceStream{
		events: []provider.Event{
			{Type: provider.EventMessageStart, Model: model},
			{Type: provider.EventContentDelta, ContentDelta: text},
			{
				Type:       provider.EventMessageStop,
				StopReason: resp.StopReason,
				Usage:      resp.Usage,
			},
		},
	}
}

func (s *sliceStream) Recv(ctx context.Context) (provider.Event, error) {
	if err := ctx.Err(); err != nil {
		return provider.Event{}, err
	}
	if s.pos >= len(s.events) {
		return provider.Event{}, io.EOF
	}
	ev := s.events[s.pos]
	s.pos++
	return ev, nil
}

func (s *sliceStream) Close() error { return nil }

// Compile-time interface assertion.
var _ provider.Provider = (*Provider)(nil)
