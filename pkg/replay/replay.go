package replay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// Mode discriminates how a ReplayProvider matches an incoming
// request against its recorded calls.
type Mode int

const (
	// ModeStrict requires the Nth incoming call to match the Nth
	// recorded call. The prompt must be byte-identical (after
	// canonical JSON encoding); otherwise Generate returns
	// ErrPromptMismatch.
	ModeStrict Mode = iota

	// ModeLenient looks up the matching recorded response by a
	// fingerprint (SHA-256 of the canonicalized prompt). Order
	// doesn't matter; works across graph restructurings as long
	// as the same prompts surface.
	ModeLenient
)

// String implements fmt.Stringer.
func (m Mode) String() string {
	switch m {
	case ModeStrict:
		return "strict"
	case ModeLenient:
		return "lenient"
	default:
		return fmt.Sprintf("Mode(%d)", int(m))
	}
}

// RecordedCall is one observation from a prior run: the messages
// sent to the provider plus the response that came back. The JSON
// tags make it round-trip cleanly through fixture files.
type RecordedCall struct {
	// SpanID, when set, identifies the source span the recording
	// came from. Informational; not used for matching.
	SpanID string `json:"span_id,omitempty"`

	// Model is the model ID the original call targeted. Carried
	// through so the replay can populate Response.Model, and folded
	// into the fingerprint so a replay against a different model
	// does not silently match.
	Model string `json:"model,omitempty"`

	// Prompt is the messages the original Request carried.
	Prompt []schema.Message `json:"prompt"`

	// Tools is the tool set the original Request advertised. Folded
	// into the fingerprint: a replay with a different tool set must
	// not match a recording made with another one.
	Tools []schema.ToolDef `json:"tools,omitempty"`

	// ToolChoice is the tool-choice constraint the original Request
	// carried. Also folded into the fingerprint.
	ToolChoice provider.ToolChoice `json:"tool_choice,omitempty"`

	// Response is the answer the real provider returned.
	Response *provider.Response `json:"response"`
}

// Fingerprint returns a stable hash of the matching-relevant parts of
// the recorded call: the prompt, the tool set, the tool-choice
// constraint and the target model. Two RecordedCall values with the
// same matching surface produce the same fingerprint; small
// reorderings inside maps don't change it because the canonical JSON
// encoder sorts keys.
//
// The error is non-nil only when the matching surface cannot be
// JSON-encoded, which propagates out as a mismatch rather than
// collapsing distinct prompts onto a shared empty value.
func (r RecordedCall) Fingerprint() (string, error) {
	return fingerprint(r.Model, r.Prompt, r.Tools, r.ToolChoice)
}

// Recording is a versioned bundle of recorded calls, ready to be
// serialized to a fixture file. The fields beyond Calls are
// metadata: useful for diffing fixture files across model / dataset
// versions.
type Recording struct {
	// Version of the fixture schema. Bumped on breaking changes.
	Version int `json:"version"`

	// RunID is the source run, when the recording was loaded from
	// a trace store.
	RunID string `json:"run_id,omitempty"`

	// Note is free-form text the user can attach (e.g., the
	// dataset version or the agent version that produced this).
	Note string `json:"note,omitempty"`

	// Calls is the ordered list of observed Generate calls.
	Calls []RecordedCall `json:"calls"`
}

// CurrentFixtureVersion is the version tag stamped onto Recording
// values written by this package.
//
// Bumped to 2 when the fingerprint started folding in Tools,
// ToolChoice and Model: recordings written by older versions hash
// their prompts differently, so the loader rejects them.
const CurrentFixtureVersion = 2

// ErrPromptMismatch is returned by Generate in ModeStrict when the
// incoming prompt doesn't match the next recorded call.
var ErrPromptMismatch = errors.New("replay: prompt does not match next recorded call")

// ErrUnknownPrompt is returned by Generate in ModeLenient when no
// recorded call has a matching fingerprint.
var ErrUnknownPrompt = errors.New("replay: no recorded call matches this prompt")

// ErrExhausted is returned by Generate when the recording has no
// more entries to serve (strict mode after the last call).
var ErrExhausted = errors.New("replay: recording exhausted")

// ErrNilResponse is returned by Generate when the matched recorded
// call carries a nil Response, so callers never receive a (nil, nil)
// result and dereference it.
var ErrNilResponse = errors.New("replay: recorded call has a nil response")

// Provider is a provider.Provider implementation backed by a
// recording. Safe for concurrent Generate calls; ModeStrict
// progresses a shared counter atomically, ModeLenient hits a
// read-only map.
type Provider struct {
	calls []RecordedCall
	mode  Mode
	idx   atomic.Int32

	// fingerprints maps a fingerprint to the ordered queue of call
	// indices that produced it (lenient mode). Calls sharing a
	// fingerprint are served in recorded order on successive matching
	// requests; cursors tracks how many of each queue have been
	// served.
	fingerprints map[string][]int
	mu           sync.Mutex
	cursors      map[string]int
}

// NewProvider builds a replay Provider over calls. The slice is
// copied defensively so external mutations don't affect playback.
func NewProvider(calls []RecordedCall, mode Mode) *Provider {
	cp := make([]RecordedCall, len(calls))
	copy(cp, calls)
	p := &Provider{calls: cp, mode: mode}
	if mode == ModeLenient {
		p.fingerprints = make(map[string][]int, len(cp))
		p.cursors = make(map[string]int, len(cp))
		for i, c := range cp {
			fp, err := c.Fingerprint()
			if err != nil {
				// A call whose matching surface can't be encoded can
				// never be matched; skip it rather than collapsing it
				// onto a shared empty key.
				continue
			}
			p.fingerprints[fp] = append(p.fingerprints[fp], i)
		}
	}
	return p
}

// Name implements provider.Provider.
func (*Provider) Name() string { return "replay" }

// Capabilities reports the same surface as a real chat provider:
// tool calling is preserved because the recorded responses already
// contain whatever tool calls the original run produced.
func (*Provider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Streaming:   false,
		ToolCalling: true,
	}
}

// Stream is not supported in Session A; callers that need streaming
// replay should fold their stream consumer back into a
// non-streaming Generate at the call site.
func (*Provider) Stream(_ context.Context, _ provider.Request) (provider.StreamReader, error) {
	return nil, provider.ErrUnsupported
}

// Generate returns the recorded response for the incoming request.
// The actual matching strategy is controlled by Mode.
func (p *Provider) Generate(_ context.Context, req provider.Request) (*provider.Response, error) {
	switch p.mode {
	case ModeLenient:
		return p.generateLenient(req)
	default:
		return p.generateStrict(req)
	}
}

func (p *Provider) generateStrict(req provider.Request) (*provider.Response, error) {
	idx := int(p.idx.Add(1)) - 1
	if idx >= len(p.calls) {
		return nil, fmt.Errorf("%w: requested call %d, only %d recorded",
			ErrExhausted, idx+1, len(p.calls))
	}
	rec := p.calls[idx]
	wantFP, err := rec.Fingerprint()
	if err != nil {
		return nil, fmt.Errorf("replay: fingerprint recorded call %d: %w", idx+1, err)
	}
	gotFP, err := fingerprintRequest(req)
	if err != nil {
		return nil, fmt.Errorf("replay: fingerprint incoming request: %w", err)
	}
	if wantFP != gotFP {
		return nil, fmt.Errorf("%w: call %d expected fingerprint %s, got %s",
			ErrPromptMismatch, idx+1, short(wantFP), short(gotFP))
	}
	return responseOrError(rec.Response)
}

func (p *Provider) generateLenient(req provider.Request) (*provider.Response, error) {
	fp, err := fingerprintRequest(req)
	if err != nil {
		return nil, fmt.Errorf("replay: fingerprint incoming request: %w", err)
	}
	queue, ok := p.fingerprints[fp]
	if !ok || len(queue) == 0 {
		return nil, fmt.Errorf("%w: fingerprint %s", ErrUnknownPrompt, short(fp))
	}
	// Serve recorded calls sharing a fingerprint in recorded order,
	// clamping to the last once the queue is drained so a repeated
	// prompt keeps replaying its final recorded response.
	p.mu.Lock()
	cur := p.cursors[fp]
	if cur >= len(queue) {
		cur = len(queue) - 1
	} else {
		p.cursors[fp] = cur + 1
	}
	p.mu.Unlock()
	return responseOrError(p.calls[queue[cur]].Response)
}

// responseOrError returns a deep copy of rec, or a descriptive error
// when the recorded response is nil so callers never receive
// (nil, nil) and nil-deref the result.
func responseOrError(r *provider.Response) (*provider.Response, error) {
	if r == nil {
		return nil, ErrNilResponse
	}
	return cloneResponse(r), nil
}

// Remaining reports how many recorded calls have not been served
// yet (strict mode only — lenient mode reuses fingerprints freely).
// Useful for asserting in tests that every recorded call was
// exercised by the replay.
func (p *Provider) Remaining() int {
	served := int(p.idx.Load())
	if served >= len(p.calls) {
		return 0
	}
	return len(p.calls) - served
}

// Reset rewinds a strict-mode Provider's counter to zero so the
// same recording can drive multiple sequential replays. No-op in
// lenient mode.
func (p *Provider) Reset() { p.idx.Store(0) }

// fingerprintEnvelope is the canonical matching surface that gets
// hashed into a fingerprint. Keeping it a named struct (rather than
// an ad-hoc slice) ensures the recorded side and the incoming-request
// side encode the exact same shape.
type fingerprintEnvelope struct {
	Model      string              `json:"model"`
	Messages   []schema.Message    `json:"messages"`
	Tools      []schema.ToolDef    `json:"tools"`
	ToolChoice provider.ToolChoice `json:"tool_choice"`
}

// fingerprint produces a deterministic SHA-256 over the canonical JSON
// encoding of the matching surface (model, messages, tools, tool
// choice). encoding/json already sorts map keys, so identical inputs
// with identically-keyed metadata round-trip to the same hash.
//
// A marshal error is returned rather than swallowed: distinct failing
// inputs must not both collapse to a shared empty fingerprint and
// false-match.
func fingerprint(model string, msgs []schema.Message, tools []schema.ToolDef, choice provider.ToolChoice) (string, error) {
	raw, err := json.Marshal(fingerprintEnvelope{
		Model:      model,
		Messages:   msgs,
		Tools:      tools,
		ToolChoice: choice,
	})
	if err != nil {
		return "", fmt.Errorf("replay: marshal fingerprint surface: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// fingerprintRequest fingerprints an incoming Request using the same
// surface as RecordedCall.Fingerprint.
func fingerprintRequest(req provider.Request) (string, error) {
	return fingerprint(req.Model, req.Messages, req.Tools, req.ToolChoice)
}

// short returns the first 12 chars of a fingerprint for log lines.
func short(fp string) string {
	if len(fp) <= 12 {
		return fp
	}
	return fp[:12]
}

// cloneResponse deep-copies a recorded response so successive
// callers can mutate the returned value without affecting the
// recording's stored copy.
func cloneResponse(r *provider.Response) *provider.Response {
	if r == nil {
		return nil
	}
	out := *r
	// schema.Message has slices with reference-typed elements; deep
	// copy them so a caller mutating the returned value (including
	// nested *ImageContent and ToolCall.Arguments bytes) can't reach
	// back into the recording's stored copy.
	if len(r.Message.Content) > 0 {
		parts := make([]schema.ContentPart, len(r.Message.Content))
		for i, part := range r.Message.Content {
			if part.Image != nil {
				img := *part.Image
				if img.Data != nil {
					img.Data = append([]byte(nil), part.Image.Data...)
				}
				part.Image = &img
			}
			parts[i] = part
		}
		out.Message.Content = parts
	}
	if len(r.Message.ToolCalls) > 0 {
		calls := make([]schema.ToolCall, len(r.Message.ToolCalls))
		for i, tc := range r.Message.ToolCalls {
			if tc.Arguments != nil {
				tc.Arguments = append(json.RawMessage(nil), tc.Arguments...)
			}
			calls[i] = tc
		}
		out.Message.ToolCalls = calls
	}
	if len(r.ProviderRaw) > 0 {
		out.ProviderRaw = append([]byte(nil), r.ProviderRaw...)
	}
	return &out
}

// Compile-time assertion: Provider satisfies provider.Provider.
var _ provider.Provider = (*Provider)(nil)
