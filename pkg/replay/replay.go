package replay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	// through so the replay can populate Response.Model.
	Model string `json:"model,omitempty"`

	// Prompt is the messages the original Request carried.
	Prompt []schema.Message `json:"prompt"`

	// Response is the answer the real provider returned.
	Response *provider.Response `json:"response"`
}

// Fingerprint returns a stable hash of the prompt. Two RecordedCall
// values with the same Prompt produce the same fingerprint; small
// reorderings inside maps don't change it because the canonical
// JSON encoder sorts keys.
func (r RecordedCall) Fingerprint() string {
	return fingerprintMessages(r.Prompt)
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
const CurrentFixtureVersion = 1

// ErrPromptMismatch is returned by Generate in ModeStrict when the
// incoming prompt doesn't match the next recorded call.
var ErrPromptMismatch = errors.New("replay: prompt does not match next recorded call")

// ErrUnknownPrompt is returned by Generate in ModeLenient when no
// recorded call has a matching fingerprint.
var ErrUnknownPrompt = errors.New("replay: no recorded call matches this prompt")

// ErrExhausted is returned by Generate when the recording has no
// more entries to serve (strict mode after the last call).
var ErrExhausted = errors.New("replay: recording exhausted")

// Provider is a provider.Provider implementation backed by a
// recording. Safe for concurrent Generate calls; ModeStrict
// progresses a shared counter atomically, ModeLenient hits a
// read-only map.
type Provider struct {
	calls        []RecordedCall
	mode         Mode
	idx          atomic.Int32
	fingerprints map[string]int // fingerprint -> index in calls (lenient mode)
}

// NewProvider builds a replay Provider over calls. The slice is
// copied defensively so external mutations don't affect playback.
func NewProvider(calls []RecordedCall, mode Mode) *Provider {
	cp := make([]RecordedCall, len(calls))
	copy(cp, calls)
	p := &Provider{calls: cp, mode: mode}
	if mode == ModeLenient {
		p.fingerprints = make(map[string]int, len(cp))
		for i, c := range cp {
			p.fingerprints[c.Fingerprint()] = i
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
	wantFP := rec.Fingerprint()
	gotFP := fingerprintMessages(req.Messages)
	if wantFP != gotFP {
		return nil, fmt.Errorf("%w: call %d expected fingerprint %s, got %s",
			ErrPromptMismatch, idx+1, short(wantFP), short(gotFP))
	}
	return cloneResponse(rec.Response), nil
}

func (p *Provider) generateLenient(req provider.Request) (*provider.Response, error) {
	fp := fingerprintMessages(req.Messages)
	idx, ok := p.fingerprints[fp]
	if !ok {
		return nil, fmt.Errorf("%w: fingerprint %s", ErrUnknownPrompt, short(fp))
	}
	return cloneResponse(p.calls[idx].Response), nil
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

// fingerprintMessages produces a deterministic SHA-256 of a
// canonical JSON encoding of msgs. encoding/json already sorts map
// keys, so identical message slices with identically-keyed
// metadata round-trip to the same hash.
func fingerprintMessages(msgs []schema.Message) string {
	raw, err := json.Marshal(msgs)
	if err != nil {
		// Should be impossible — schema.Message is well-formed JSON.
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
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
	// schema.Message has slices; clone them too.
	if len(r.Message.Content) > 0 {
		out.Message.Content = make([]schema.ContentPart, len(r.Message.Content))
		copy(out.Message.Content, r.Message.Content)
	}
	if len(r.Message.ToolCalls) > 0 {
		out.Message.ToolCalls = make([]schema.ToolCall, len(r.Message.ToolCalls))
		copy(out.Message.ToolCalls, r.Message.ToolCalls)
	}
	if len(r.ProviderRaw) > 0 {
		out.ProviderRaw = append([]byte(nil), r.ProviderRaw...)
	}
	return &out
}

// Compile-time assertion: Provider satisfies provider.Provider.
var _ provider.Provider = (*Provider)(nil)
