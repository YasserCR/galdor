package google

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

func newTestProvider(t *testing.T, srv *httptest.Server) *Provider {
	t.Helper()
	p, err := New(Config{APIKey: "test-key", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNew_RequiresAPIKey(t *testing.T) {
	t.Parallel()
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected error for empty APIKey")
	}
}

func TestNew_DefaultsApplied(t *testing.T) {
	t.Parallel()
	p, err := New(Config{APIKey: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if p.baseURL != defaultBaseURL {
		t.Errorf("baseURL = %q", p.baseURL)
	}
	if p.httpClient == nil {
		t.Error("httpClient must not be nil")
	}
}

func TestProvider_NameAndCapabilities(t *testing.T) {
	t.Parallel()
	p, _ := New(Config{APIKey: "x"})
	if p.Name() != "google" {
		t.Errorf("Name = %q", p.Name())
	}
	caps := p.Capabilities()
	if !caps.Streaming || !caps.ToolCalling || !caps.StructuredOutput || !caps.VisionInput {
		t.Errorf("Capabilities = %+v", caps)
	}
	if caps.PromptCaching {
		t.Error("PromptCaching must be false: adapter does not wire CacheControl into CachedContent")
	}
	if caps.MaxContextTokens < 1_000_000 {
		t.Errorf("MaxContextTokens too low: %d", caps.MaxContextTokens)
	}
}

func TestProvider_StringHidesKey(t *testing.T) {
	t.Parallel()
	p, _ := New(Config{APIKey: "AIzaSuperSecret"})
	if strings.Contains(p.String(), "AIzaSuperSecret") {
		t.Fatal("String() leaked the API key")
	}
}

// fixtureGenerateOK is a real-shape Gemini generateContent success body.
const fixtureGenerateOK = `{
  "candidates": [{
    "content": {"role": "model", "parts": [{"text": "Hello, world!"}]},
    "finishReason": "STOP",
    "index": 0
  }],
  "usageMetadata": {"promptTokenCount": 12, "candidatesTokenCount": 5, "totalTokenCount": 17, "cachedContentTokenCount": 4, "thoughtsTokenCount": 2},
  "modelVersion": "gemini-2.5-flash-001"
}`

func TestGenerate_HappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-goog-api-key"); got != "test-key" {
			t.Errorf("x-goog-api-key = %q", got)
		}
		if !strings.HasSuffix(r.URL.Path, "/models/gemini-2.5-flash:generateContent") {
			t.Errorf("path = %q", r.URL.Path)
		}
		var body generateRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		// System message must be hoisted to systemInstruction.
		if body.SystemInstruction == nil || body.SystemInstruction.Parts[0].Text != "be terse" {
			t.Errorf("systemInstruction wrong: %+v", body.SystemInstruction)
		}
		if len(body.Contents) != 1 || body.Contents[0].Role != "user" {
			t.Errorf("contents = %+v", body.Contents)
		}
		if body.Contents[0].Parts[0].Text != "hi" {
			t.Errorf("text part = %+v", body.Contents[0].Parts[0])
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, fixtureGenerateOK)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	resp, err := p.Generate(context.Background(), provider.Request{
		Model: "gemini-2.5-flash",
		Messages: []schema.Message{
			schema.SystemMessage("be terse"),
			schema.UserMessage("hi"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Message.Text(); got != "Hello, world!" {
		t.Errorf("Text = %q", got)
	}
	if resp.StopReason != schema.StopReasonEndTurn {
		t.Errorf("StopReason = %q", resp.StopReason)
	}
	// Output tokens include thoughtsTokenCount.
	if resp.Usage.InputTokens != 12 || resp.Usage.OutputTokens != 7 {
		t.Errorf("Usage = %+v", resp.Usage)
	}
	if resp.Usage.CacheReadTokens != 4 {
		t.Errorf("CacheReadTokens = %d", resp.Usage.CacheReadTokens)
	}
	if resp.Model != "gemini-2.5-flash-001" {
		t.Errorf("Model = %q", resp.Model)
	}
	if len(resp.ProviderRaw) == 0 {
		t.Error("ProviderRaw should be populated")
	}
}

func TestGenerate_ToolCallsInResponse(t *testing.T) {
	t.Parallel()
	const body = `{
  "candidates": [{
    "content": {"role": "model", "parts": [
      {"functionCall": {"name": "weather", "args": {"city": "Quito"}}},
      {"functionCall": {"name": "time", "args": {}}}
    ]},
    "finishReason": "STOP"
  }],
  "usageMetadata": {"promptTokenCount": 10, "candidatesTokenCount": 7, "totalTokenCount": 17},
  "modelVersion": "gemini-2.5-flash-001"
}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	resp, err := p.Generate(context.Background(), provider.Request{
		Model:    "gemini-2.5-flash",
		Messages: []schema.Message{schema.UserMessage("anything")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.ToolCalls) != 2 {
		t.Fatalf("ToolCalls = %d", len(resp.Message.ToolCalls))
	}
	if resp.Message.ToolCalls[0].Name != "weather" || resp.Message.ToolCalls[1].Name != "time" {
		t.Errorf("calls = %+v", resp.Message.ToolCalls)
	}
	// IDs must be synthesized (Gemini doesn't issue them) and include the function name.
	if !strings.Contains(resp.Message.ToolCalls[0].ID, "weather") {
		t.Errorf("synthetic id should embed name: %q", resp.Message.ToolCalls[0].ID)
	}
	if err := provider.ValidateToolCalls(resp.Message); err != nil {
		t.Fatalf("ValidateToolCalls: %v", err)
	}
}

func TestGenerate_401NormalizedToAuth(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"code":401,"message":"API key not valid","status":"UNAUTHENTICATED"}}`)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	_, err := p.Generate(context.Background(), provider.Request{
		Model:    "gemini-2.5-flash",
		Messages: []schema.Message{schema.UserMessage("hi")},
	})
	if !errors.Is(err, provider.ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
	var apiErr *provider.APIError
	if !errors.As(err, &apiErr) {
		t.Fatal("err not *APIError")
	}
	if apiErr.Message != "API key not valid" {
		t.Errorf("Message = %q", apiErr.Message)
	}
}

func TestGenerate_InvalidAPIKeyPromotedToAuth(t *testing.T) {
	t.Parallel()
	// Google returns 400 + status=INVALID_ARGUMENT for an invalid API
	// key, with the real classification only in details[].reason. The
	// adapter must promote that to ErrAuth.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"code":400,"message":"API key not valid.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"API_KEY_INVALID","domain":"googleapis.com"}]}}`)
	}))
	defer srv.Close()
	p := newTestProvider(t, srv)
	_, err := p.Generate(context.Background(), provider.Request{
		Model:    "gemini-2.5-flash",
		Messages: []schema.Message{schema.UserMessage("hi")},
	})
	if !errors.Is(err, provider.ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth (promoted from details[].reason=API_KEY_INVALID)", err)
	}
}

func TestGenerate_429NormalizedToRateLimit(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("retry-after", "13")
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"code":429,"message":"quota exceeded","status":"RESOURCE_EXHAUSTED"}}`)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	_, err := p.Generate(context.Background(), provider.Request{
		Model:    "gemini-2.5-flash",
		Messages: []schema.Message{schema.UserMessage("hi")},
	})
	if !errors.Is(err, provider.ErrRateLimited) {
		t.Fatalf("err = %v", err)
	}
	var apiErr *provider.APIError
	if !errors.As(err, &apiErr) {
		t.Fatal("err not *APIError")
	}
	if apiErr.RetryAfter != 13 {
		t.Errorf("RetryAfter = %d", apiErr.RetryAfter)
	}
}

func TestGenerate_ArrayErrorBody(t *testing.T) {
	t.Parallel()
	// Some Google APIs return a JSON array of error objects on early
	// failures. The adapter must handle that shape too.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `[{"error":{"code":400,"message":"bad model","status":"INVALID_ARGUMENT"}}]`)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	_, err := p.Generate(context.Background(), provider.Request{
		Model:    "gemini-2.5-flash",
		Messages: []schema.Message{schema.UserMessage("hi")},
	})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("err = %v", err)
	}
	var apiErr *provider.APIError
	if !errors.As(err, &apiErr) || apiErr.Message != "bad model" {
		t.Fatalf("apiErr = %+v", apiErr)
	}
}

func TestGenerate_ContextCanceled(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		<-make(chan struct{})
	}))
	defer srv.Close()
	p := newTestProvider(t, srv)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Generate(ctx, provider.Request{
		Model:    "gemini-2.5-flash",
		Messages: []schema.Message{schema.UserMessage("hi")},
	}); err == nil {
		t.Fatal("expected error")
	}
}

func TestGenerate_RejectsEmptyModel(t *testing.T) {
	t.Parallel()
	p, _ := New(Config{APIKey: "x"})
	_, err := p.Generate(context.Background(), provider.Request{
		Messages: []schema.Message{schema.UserMessage("hi")},
	})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("err = %v", err)
	}
}

func TestStream_HappyPath(t *testing.T) {
	t.Parallel()
	body := strings.Join([]string{
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hello, "}]},"index":0}],"modelVersion":"gemini-2.5-flash-001"}`,
		"",
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"world!"}]},"index":0}],"modelVersion":"gemini-2.5-flash-001"}`,
		"",
		`data: {"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":3,"totalTokenCount":10},"modelVersion":"gemini-2.5-flash-001"}`,
		"",
		"",
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("accept"); got != "text/event-stream" {
			t.Errorf("accept = %q", got)
		}
		if !strings.Contains(r.URL.Path, ":streamGenerateContent") {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("alt") != "sse" {
			t.Errorf("alt = %q", r.URL.Query().Get("alt"))
		}
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	resp, err := provider.CollectStream(context.Background(), mustStream(t, p,
		provider.Request{
			Model:    "gemini-2.5-flash",
			Messages: []schema.Message{schema.UserMessage("hi")},
		}))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Text() != "Hello, world!" {
		t.Errorf("Text = %q", resp.Message.Text())
	}
	if resp.StopReason != schema.StopReasonEndTurn {
		t.Errorf("StopReason = %q", resp.StopReason)
	}
	if resp.Usage.InputTokens != 7 || resp.Usage.OutputTokens != 3 {
		t.Errorf("Usage = %+v", resp.Usage)
	}
	if resp.Model != "gemini-2.5-flash-001" {
		t.Errorf("Model = %q", resp.Model)
	}
}

func TestStream_ToolCallEmitted(t *testing.T) {
	t.Parallel()
	body := strings.Join([]string{
		`data: {"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"weather","args":{"city":"Quito"}}}]},"index":0}],"modelVersion":"gemini-2.5-flash-001"}`,
		"",
		`data: {"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":4},"modelVersion":"gemini-2.5-flash-001"}`,
		"",
		"",
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	resp, err := provider.CollectStream(context.Background(), mustStream(t, p,
		provider.Request{
			Model:    "gemini-2.5-flash",
			Messages: []schema.Message{schema.UserMessage("weather?")},
		}))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].Name != "weather" {
		t.Fatalf("calls = %+v", resp.Message.ToolCalls)
	}
	if string(resp.Message.ToolCalls[0].Arguments) != `{"city":"Quito"}` {
		t.Errorf("Args = %s", resp.Message.ToolCalls[0].Arguments)
	}
}

func TestStream_4xxBeforeFirstChunk(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"code":400,"message":"bad model","status":"INVALID_ARGUMENT"}}`)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	_, err := p.Stream(context.Background(), provider.Request{
		Model:    "gemini-2.5-flash",
		Messages: []schema.Message{schema.UserMessage("hi")},
	})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("err = %v", err)
	}
}

func mustStream(t *testing.T, p *Provider, req provider.Request) provider.StreamReader {
	t.Helper()
	s, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestStream_SurfacesReasoning verifies streamed thought parts are kept
// off the live content stream and delivered as a thinking part on the
// terminal MessageStop.
func TestStream_SurfacesReasoning(t *testing.T) {
	t.Parallel()
	body := strings.Join([]string{
		`data: {"modelVersion":"gemini-2.5-flash","candidates":[{"content":{"parts":[{"text":"let me ","thought":true}]}}]}`,
		"",
		`data: {"candidates":[{"content":{"parts":[{"text":"reason","thought":true}]}}]}`,
		"",
		`data: {"candidates":[{"content":{"parts":[{"text":"answer"}]}}]}`,
		"",
		`data: {"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}`,
		"",
		"",
	}, "\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	stream := mustStream(t, p, provider.Request{
		Model:     "gemini-2.5-flash",
		Messages:  []schema.Message{schema.UserMessage("hi")},
		Reasoning: &provider.ReasoningConfig{Enabled: true},
	})
	defer func() { _ = stream.Close() }()

	var live string
	var stopMsg *schema.Message
	for {
		ev, err := stream.Recv(context.Background())
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		switch ev.Type {
		case provider.EventContentDelta:
			live += ev.ContentDelta
		case provider.EventMessageStop:
			stopMsg = ev.Message
		}
	}
	if live != "answer" {
		t.Errorf("live stream = %q, want clean %q", live, "answer")
	}
	if stopMsg == nil || len(stopMsg.Content) != 1 ||
		stopMsg.Content[0].Type != schema.ContentTypeThinking || stopMsg.Content[0].Text != "let me reason" {
		t.Fatalf("reasoning part wrong: %+v", stopMsg)
	}
}
