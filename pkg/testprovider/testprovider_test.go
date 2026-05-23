package testprovider

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

func TestGenerate_ReturnsScriptedResponse(t *testing.T) {
	t.Parallel()
	p := New(Responses("hello", "world"))
	ctx := context.Background()

	r1, err := p.Generate(ctx, provider.Request{Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if got := r1.Message.Text(); got != "hello" {
		t.Errorf("first = %q", got)
	}

	r2, err := p.Generate(ctx, provider.Request{Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if got := r2.Message.Text(); got != "world" {
		t.Errorf("second = %q", got)
	}
}

func TestGenerate_ReturnsScriptedError(t *testing.T) {
	t.Parallel()
	rl := provider.Classify(&provider.APIError{Kind: provider.ErrRateLimited, Provider: "test"})
	p := New(Responses("first"), Errors(rl), Responses("third"))
	ctx := context.Background()

	r, err := p.Generate(ctx, provider.Request{})
	if err != nil || r.Message.Text() != "first" {
		t.Fatalf("step 1: %v / %q", err, textOrEmpty(r))
	}

	_, err = p.Generate(ctx, provider.Request{})
	var asRL *provider.RateLimitError
	if !errors.As(err, &asRL) {
		t.Fatalf("step 2: expected *RateLimitError, got %T (%v)", err, err)
	}

	r, err = p.Generate(ctx, provider.Request{})
	if err != nil || r.Message.Text() != "third" {
		t.Fatalf("step 3: %v / %q", err, textOrEmpty(r))
	}
}

func TestGenerate_ExhaustionIsTyped(t *testing.T) {
	t.Parallel()
	p := New(Responses("only"))
	ctx := context.Background()
	if _, err := p.Generate(ctx, provider.Request{}); err != nil {
		t.Fatal(err)
	}
	_, err := p.Generate(ctx, provider.Request{})
	if !errors.Is(err, ErrScriptExhausted) {
		t.Errorf("expected ErrScriptExhausted; got %v", err)
	}
}

func TestJSONResponses(t *testing.T) {
	t.Parallel()
	type S struct {
		Name string `json:"name"`
	}
	p := New(JSONResponses(S{Name: "alpha"}))
	r, err := p.Generate(context.Background(), provider.Request{})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := schema.ParseJSON[S](r.Message.Text())
	if err != nil {
		t.Fatalf("ParseJSON: %v", err)
	}
	if parsed.Name != "alpha" {
		t.Errorf("Name = %q", parsed.Name)
	}
}

func TestStream_ReplaysAsSyntheticEvents(t *testing.T) {
	t.Parallel()
	p := New(Responses("streamed body"))
	stream, err := p.Stream(context.Background(), provider.Request{Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()

	var saw []provider.EventType
	var body string
	for {
		ev, err := stream.Recv(context.Background())
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		saw = append(saw, ev.Type)
		if ev.Type == provider.EventContentDelta {
			body += ev.ContentDelta
		}
	}
	if len(saw) != 3 || saw[0] != provider.EventMessageStart || saw[2] != provider.EventMessageStop {
		t.Errorf("event sequence = %v", saw)
	}
	if body != "streamed body" {
		t.Errorf("body = %q", body)
	}
}

func TestRequests_RecordsInOrder(t *testing.T) {
	t.Parallel()
	p := New(Responses("a", "b"))
	ctx := context.Background()
	_, _ = p.Generate(ctx, provider.Request{Model: "m1"})
	_, _ = p.Generate(ctx, provider.Request{Model: "m2"})
	got := p.Requests()
	if len(got) != 2 || got[0].Model != "m1" || got[1].Model != "m2" {
		t.Errorf("Requests() = %+v", got)
	}
	// Snapshot must not alias internal storage.
	got[0].Model = "mutated"
	again := p.Requests()
	if again[0].Model != "m1" {
		t.Errorf("snapshot aliased internal slice; got %q", again[0].Model)
	}
}

func TestReset(t *testing.T) {
	t.Parallel()
	p := New(Responses("x"))
	if _, err := p.Generate(context.Background(), provider.Request{}); err != nil {
		t.Fatal(err)
	}
	if p.Remaining() != 0 {
		t.Errorf("Remaining() = %d, want 0", p.Remaining())
	}
	p.Reset()
	if p.Remaining() != 1 {
		t.Errorf("after Reset, Remaining() = %d, want 1", p.Remaining())
	}
	if got := len(p.Requests()); got != 0 {
		t.Errorf("after Reset, Requests() len = %d", got)
	}
}

func TestNameAndCapabilities(t *testing.T) {
	t.Parallel()
	p := New(
		Name("scripted"),
		Capabilities(provider.Capabilities{Streaming: false, MaxContextTokens: 128}),
	)
	if p.Name() != "scripted" {
		t.Errorf("Name() = %q", p.Name())
	}
	if p.Capabilities().Streaming {
		t.Errorf("Streaming should be false")
	}
	if p.Capabilities().MaxContextTokens != 128 {
		t.Errorf("MaxContextTokens = %d", p.Capabilities().MaxContextTokens)
	}
}

func TestConcurrentGenerate(t *testing.T) {
	t.Parallel()
	const N = 50
	responses := make([]string, N)
	for i := range responses {
		responses[i] = "r"
	}
	p := New(Responses(responses...))

	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			_, _ = p.Generate(context.Background(), provider.Request{})
		}()
	}
	wg.Wait()

	if p.Remaining() != 0 {
		t.Errorf("Remaining() after %d concurrent calls = %d", N, p.Remaining())
	}
	if got := len(p.Requests()); got != N {
		t.Errorf("Requests() len = %d, want %d", got, N)
	}
}

func TestContextCancellationBeforeGenerate(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := New(Responses("never"))
	_, err := p.Generate(ctx, provider.Request{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled; got %v", err)
	}
	// The scripted step must NOT have been consumed.
	if p.Remaining() != 1 {
		t.Errorf("script consumed despite cancelled ctx; Remaining = %d", p.Remaining())
	}
}

func textOrEmpty(r *provider.Response) string {
	if r == nil {
		return ""
	}
	return r.Message.Text()
}
