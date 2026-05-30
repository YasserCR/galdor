package replay_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/YasserCR/galdor/pkg/agent"
	"github.com/YasserCR/galdor/pkg/graph"
	"github.com/YasserCR/galdor/pkg/observability"
	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/replay"
	"github.com/YasserCR/galdor/pkg/schema"
)

// scriptedProvider returns a canned reply per Generate call, useful
// for "record" tests where we know exactly what each turn should
// produce.
type scriptedProvider struct {
	Plan  []*provider.Response
	calls atomic.Int32
}

func (*scriptedProvider) Name() string { return "scripted" }
func (*scriptedProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{ToolCalling: true}
}
func (*scriptedProvider) Stream(_ context.Context, _ provider.Request) (provider.StreamReader, error) {
	return nil, provider.ErrUnsupported
}
func (p *scriptedProvider) Generate(_ context.Context, _ provider.Request) (*provider.Response, error) {
	idx := int(p.calls.Add(1)) - 1
	if idx >= len(p.Plan) {
		return nil, errors.New("scripted: exhausted")
	}
	return p.Plan[idx], nil
}

// ----------- Provider unit tests -----------

func recordedCall(prompt []schema.Message, reply string) replay.RecordedCall {
	return replay.RecordedCall{
		Prompt: prompt,
		Response: &provider.Response{
			Message:    schema.AssistantMessage(reply),
			StopReason: schema.StopReasonEndTurn,
		},
	}
}

func TestProvider_StrictHappyPath(t *testing.T) {
	t.Parallel()
	calls := []replay.RecordedCall{
		recordedCall([]schema.Message{schema.UserMessage("hi")}, "hello"),
		recordedCall([]schema.Message{schema.UserMessage("again")}, "yes"),
	}
	p := replay.NewProvider(calls, replay.ModeStrict)

	resp, err := p.Generate(context.Background(), provider.Request{Messages: []schema.Message{schema.UserMessage("hi")}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Text() != "hello" {
		t.Errorf("first reply = %q", resp.Message.Text())
	}
	resp, _ = p.Generate(context.Background(), provider.Request{Messages: []schema.Message{schema.UserMessage("again")}})
	if resp.Message.Text() != "yes" {
		t.Errorf("second reply = %q", resp.Message.Text())
	}
	if p.Remaining() != 0 {
		t.Errorf("Remaining = %d, want 0", p.Remaining())
	}
}

func TestProvider_StrictMismatchErrors(t *testing.T) {
	t.Parallel()
	calls := []replay.RecordedCall{
		recordedCall([]schema.Message{schema.UserMessage("hi")}, "hello"),
	}
	p := replay.NewProvider(calls, replay.ModeStrict)
	_, err := p.Generate(context.Background(), provider.Request{Messages: []schema.Message{schema.UserMessage("DIFFERENT")}})
	if !errors.Is(err, replay.ErrPromptMismatch) {
		t.Fatalf("err = %v, want ErrPromptMismatch", err)
	}
}

func TestProvider_StrictExhaustionErrors(t *testing.T) {
	t.Parallel()
	calls := []replay.RecordedCall{
		recordedCall([]schema.Message{schema.UserMessage("hi")}, "hello"),
	}
	p := replay.NewProvider(calls, replay.ModeStrict)
	_, _ = p.Generate(context.Background(), provider.Request{Messages: []schema.Message{schema.UserMessage("hi")}})
	_, err := p.Generate(context.Background(), provider.Request{Messages: []schema.Message{schema.UserMessage("hi")}})
	if !errors.Is(err, replay.ErrExhausted) {
		t.Fatalf("err = %v, want ErrExhausted", err)
	}
}

func TestProvider_StrictReset(t *testing.T) {
	t.Parallel()
	calls := []replay.RecordedCall{
		recordedCall([]schema.Message{schema.UserMessage("a")}, "1"),
	}
	p := replay.NewProvider(calls, replay.ModeStrict)
	for i := 0; i < 3; i++ {
		_, err := p.Generate(context.Background(), provider.Request{Messages: []schema.Message{schema.UserMessage("a")}})
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		p.Reset()
	}
}

func TestProvider_LenientReordersCalls(t *testing.T) {
	t.Parallel()
	calls := []replay.RecordedCall{
		recordedCall([]schema.Message{schema.UserMessage("first")}, "1"),
		recordedCall([]schema.Message{schema.UserMessage("second")}, "2"),
	}
	p := replay.NewProvider(calls, replay.ModeLenient)
	// Hit them in reverse order — lenient mode handles that.
	resp2, err := p.Generate(context.Background(), provider.Request{Messages: []schema.Message{schema.UserMessage("second")}})
	if err != nil {
		t.Fatal(err)
	}
	if resp2.Message.Text() != "2" {
		t.Errorf("got %q", resp2.Message.Text())
	}
	resp1, _ := p.Generate(context.Background(), provider.Request{Messages: []schema.Message{schema.UserMessage("first")}})
	if resp1.Message.Text() != "1" {
		t.Errorf("got %q", resp1.Message.Text())
	}
}

func TestProvider_LenientUnknownPrompt(t *testing.T) {
	t.Parallel()
	calls := []replay.RecordedCall{
		recordedCall([]schema.Message{schema.UserMessage("known")}, "ok"),
	}
	p := replay.NewProvider(calls, replay.ModeLenient)
	_, err := p.Generate(context.Background(), provider.Request{Messages: []schema.Message{schema.UserMessage("ghost")}})
	if !errors.Is(err, replay.ErrUnknownPrompt) {
		t.Fatalf("err = %v, want ErrUnknownPrompt", err)
	}
}

func TestProvider_FingerprintStableAcrossSerialization(t *testing.T) {
	t.Parallel()
	a := recordedCall([]schema.Message{
		schema.SystemMessage("sys"),
		schema.UserMessage("u"),
	}, "x")
	b := recordedCall([]schema.Message{
		schema.SystemMessage("sys"),
		schema.UserMessage("u"),
	}, "x")
	fpA, err := a.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	fpB, err := b.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if fpA != fpB {
		t.Errorf("identical prompts hash differently:\n%s\n%s", fpA, fpB)
	}
	c := recordedCall([]schema.Message{schema.UserMessage("diff")}, "x")
	fpC, err := c.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if fpA == fpC {
		t.Errorf("different prompts hash identically")
	}
}

func TestProvider_ResponseIsDeepCopied(t *testing.T) {
	t.Parallel()
	prompt := []schema.Message{schema.UserMessage("hi")}
	call := replay.RecordedCall{
		Prompt: prompt,
		Response: &provider.Response{
			Message: schema.Message{
				Role:    schema.RoleAssistant,
				Content: []schema.ContentPart{schema.TextPart("hello")},
			},
		},
	}
	p := replay.NewProvider([]replay.RecordedCall{call}, replay.ModeStrict)
	resp, _ := p.Generate(context.Background(), provider.Request{Messages: prompt})
	// Mutate the returned response — must not corrupt the recording.
	resp.Message.Content[0].Text = "MUTATED"

	p.Reset()
	again, _ := p.Generate(context.Background(), provider.Request{Messages: prompt})
	if again.Message.Content[0].Text != "hello" {
		t.Errorf("recording was aliased: %q", again.Message.Content[0].Text)
	}
}

// B6: identical messages but a different tool set must NOT match in
// strict mode — the fingerprint folds in Tools/ToolChoice/Model.
func TestProvider_StrictDifferentToolsMismatch(t *testing.T) {
	t.Parallel()
	prompt := []schema.Message{schema.UserMessage("hi")}
	call := replay.RecordedCall{
		Prompt:   prompt,
		Tools:    []schema.ToolDef{{Name: "search"}},
		Response: &provider.Response{Message: schema.AssistantMessage("ok")},
	}
	p := replay.NewProvider([]replay.RecordedCall{call}, replay.ModeStrict)
	// Same messages, different tool set -> must mismatch.
	_, err := p.Generate(context.Background(), provider.Request{
		Messages: prompt,
		Tools:    []schema.ToolDef{{Name: "calculator"}},
	})
	if !errors.Is(err, replay.ErrPromptMismatch) {
		t.Fatalf("err = %v, want ErrPromptMismatch", err)
	}

	// Matching tool set -> served fine.
	p.Reset()
	resp, err := p.Generate(context.Background(), provider.Request{
		Messages: prompt,
		Tools:    []schema.ToolDef{{Name: "search"}},
	})
	if err != nil {
		t.Fatalf("matching tools: %v", err)
	}
	if resp.Message.Text() != "ok" {
		t.Errorf("got %q", resp.Message.Text())
	}
}

// B7: two recorded calls with the same prompt but different responses
// must replay in recorded order in lenient mode.
func TestProvider_LenientSamePromptReplaysInOrder(t *testing.T) {
	t.Parallel()
	prompt := []schema.Message{schema.UserMessage("same")}
	calls := []replay.RecordedCall{
		recordedCall(prompt, "first"),
		recordedCall(prompt, "second"),
	}
	p := replay.NewProvider(calls, replay.ModeLenient)

	r1, err := p.Generate(context.Background(), provider.Request{Messages: prompt})
	if err != nil {
		t.Fatal(err)
	}
	if r1.Message.Text() != "first" {
		t.Errorf("first replay = %q, want first", r1.Message.Text())
	}
	r2, err := p.Generate(context.Background(), provider.Request{Messages: prompt})
	if err != nil {
		t.Fatal(err)
	}
	if r2.Message.Text() != "second" {
		t.Errorf("second replay = %q, want second", r2.Message.Text())
	}
	// Drained queue keeps replaying the last recorded response.
	r3, err := p.Generate(context.Background(), provider.Request{Messages: prompt})
	if err != nil {
		t.Fatal(err)
	}
	if r3.Message.Text() != "second" {
		t.Errorf("third replay = %q, want second (clamped)", r3.Message.Text())
	}
}

// B8: mutating nested *Image and ToolCall.Arguments on the returned
// Response must not corrupt the stored recording.
func TestProvider_NestedResponseIsDeepCopied(t *testing.T) {
	t.Parallel()
	prompt := []schema.Message{schema.UserMessage("hi")}
	call := replay.RecordedCall{
		Prompt: prompt,
		Response: &provider.Response{
			Message: schema.Message{
				Role:    schema.RoleAssistant,
				Content: []schema.ContentPart{schema.ImagePartURL("http://orig/x.png")},
				ToolCalls: []schema.ToolCall{
					{ID: "1", Name: "t", Arguments: json.RawMessage(`{"a":1}`)},
				},
			},
		},
	}
	p := replay.NewProvider([]replay.RecordedCall{call}, replay.ModeStrict)
	resp, err := p.Generate(context.Background(), provider.Request{Messages: prompt})
	if err != nil {
		t.Fatal(err)
	}
	// Mutate nested reference-typed fields on the returned copy.
	resp.Message.Content[0].Image.URL = "MUTATED"
	resp.Message.ToolCalls[0].Arguments[6] = 'X'

	p.Reset()
	again, err := p.Generate(context.Background(), provider.Request{Messages: prompt})
	if err != nil {
		t.Fatal(err)
	}
	if again.Message.Content[0].Image.URL != "http://orig/x.png" {
		t.Errorf("Image was aliased: %q", again.Message.Content[0].Image.URL)
	}
	if string(again.Message.ToolCalls[0].Arguments) != `{"a":1}` {
		t.Errorf("Arguments were aliased: %s", again.Message.ToolCalls[0].Arguments)
	}
}

// C10: a recorded nil response must yield an error from Generate, not
// (nil, nil).
func TestProvider_NilResponseErrors(t *testing.T) {
	t.Parallel()
	prompt := []schema.Message{schema.UserMessage("hi")}
	call := replay.RecordedCall{Prompt: prompt, Response: nil}

	pStrict := replay.NewProvider([]replay.RecordedCall{call}, replay.ModeStrict)
	resp, err := pStrict.Generate(context.Background(), provider.Request{Messages: prompt})
	if !errors.Is(err, replay.ErrNilResponse) {
		t.Fatalf("strict err = %v, want ErrNilResponse", err)
	}
	if resp != nil {
		t.Errorf("strict resp = %+v, want nil", resp)
	}

	pLenient := replay.NewProvider([]replay.RecordedCall{call}, replay.ModeLenient)
	resp, err = pLenient.Generate(context.Background(), provider.Request{Messages: prompt})
	if !errors.Is(err, replay.ErrNilResponse) {
		t.Fatalf("lenient err = %v, want ErrNilResponse", err)
	}
	if resp != nil {
		t.Errorf("lenient resp = %+v, want nil", resp)
	}
}

// C10 (loader): a fixture with a nil response is rejected at load.
func TestLoadFromFile_RejectsNilResponse(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "nilresp.json")
	rec := replay.Recording{
		Version: replay.CurrentFixtureVersion,
		Calls: []replay.RecordedCall{
			{Prompt: []schema.Message{schema.UserMessage("hi")}, Response: nil},
		},
	}
	if err := replay.SaveToFile(rec, path); err != nil {
		t.Fatal(err)
	}
	_, err := replay.LoadFromFile(path)
	if !errors.Is(err, replay.ErrNilResponse) {
		t.Fatalf("err = %v, want ErrNilResponse", err)
	}
}

// ----------- Fixture roundtrip -----------

func TestSaveLoad_RoundTrip(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "fixture.json")
	original := replay.Recording{
		Version: replay.CurrentFixtureVersion,
		RunID:   "demo-1",
		Note:    "smoke test",
		Calls: []replay.RecordedCall{
			recordedCall([]schema.Message{schema.UserMessage("hi")}, "hello"),
		},
	}
	if err := replay.SaveToFile(original, path); err != nil {
		t.Fatal(err)
	}
	loaded, err := replay.LoadFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RunID != "demo-1" || loaded.Note != "smoke test" {
		t.Errorf("metadata: %+v", loaded)
	}
	if len(loaded.Calls) != 1 || loaded.Calls[0].Response.Message.Text() != "hello" {
		t.Errorf("calls: %+v", loaded.Calls)
	}
	// Fingerprints must match across the roundtrip.
	loadedFP, err := loaded.Calls[0].Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	originalFP, err := original.Calls[0].Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if loadedFP != originalFP {
		t.Errorf("fingerprint drifted across roundtrip")
	}
}

func TestLoadFromFile_RejectsBadVersion(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "bad.json")
	bad := replay.Recording{Version: 9999, Calls: []replay.RecordedCall{
		recordedCall([]schema.Message{schema.UserMessage("hi")}, "x"),
	}}
	// Bypass SaveToFile so the version field isn't auto-corrected.
	_ = replay.SaveToFile(bad, path)
	// SaveToFile only overwrites Version when it's 0, so this stays 9999.
	_, err := replay.LoadFromFile(path)
	if err == nil {
		t.Fatal("expected version error")
	}
}

// ----------- Record + Replay roundtrip via SQLite -----------

func TestEndToEnd_RecordAndReplayThroughReAct(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "traces.db")

	// 1. Record: drive a ReAct agent with a scripted provider, with
	//    observability + content capture wired up. The exporter
	//    pushes spans into a SQLite store.
	exporter, err := observability.NewSQLiteExporter(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = exporter.Shutdown(ctx) }()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer func() { _ = tp.Shutdown(ctx) }()
	tracer := tp.Tracer("test")

	// Scripted: one-shot answer.
	innerProvider := &scriptedProvider{Plan: []*provider.Response{
		{
			Message:    schema.AssistantMessage("the answer is 42"),
			StopReason: schema.StopReasonEndTurn,
			Model:      "scripted-1",
		},
	}}
	instrumented := observability.InstrumentProvider(innerProvider, tracer, observability.WithCaptureContent(true))

	r, err := agent.NewReAct(agent.Config{
		Provider: instrumented,
		Model:    "scripted-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	hooks := observability.TraceHooks[agent.State](tracer)
	const runID = "test-run-1"
	_, err = r.InvokeWith(ctx,
		agent.State{Messages: []schema.Message{schema.UserMessage("what is the answer?")}},
		graph.RunOptions[agent.State]{RunID: runID, Hooks: hooks},
	)
	if err != nil {
		t.Fatal(err)
	}

	// Force flush so the SQLite exporter writes everything.
	if flushErr := tp.ForceFlush(ctx); flushErr != nil {
		t.Fatal(flushErr)
	}
	// Give the batch exporter a moment if it's deferring.
	time.Sleep(50 * time.Millisecond)

	// 2. Replay: read the stored spans, build a Provider, drive a
	//    fresh ReAct over the same input. The scripted inner
	//    provider is NOT used this time — the replay Provider
	//    serves the recorded answer.
	rec, err := replay.LoadFromStore(ctx, dbPath, runID)
	if err != nil {
		t.Fatalf("LoadFromStore: %v", err)
	}
	if len(rec.Calls) != 1 {
		t.Fatalf("recorded calls = %d, want 1", len(rec.Calls))
	}

	replayProv := replay.NewProvider(rec.Calls, replay.ModeStrict)
	r2, err := agent.NewReAct(agent.Config{
		Provider: replayProv,
		Model:    "scripted-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	final, err := r2.Invoke(ctx, agent.State{
		Messages: []schema.Message{schema.UserMessage("what is the answer?")},
	})
	if err != nil {
		t.Fatalf("replay invoke: %v", err)
	}
	if final.FinalText != "the answer is 42" {
		t.Errorf("replayed text = %q", final.FinalText)
	}
	if replayProv.Remaining() != 0 {
		t.Errorf("Remaining = %d, want 0 (all recorded calls served)", replayProv.Remaining())
	}
}
