package openai

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
	if p.Name() != "openai" {
		t.Errorf("Name = %q", p.Name())
	}
	caps := p.Capabilities()
	if !caps.Streaming || !caps.ToolCalling || !caps.StructuredOutput || !caps.VisionInput {
		t.Errorf("Capabilities = %+v", caps)
	}
	if caps.PromptCaching {
		t.Error("PromptCaching must be false: OpenAI ignores CacheControl hints")
	}
}

func TestProvider_StringHidesKey(t *testing.T) {
	t.Parallel()
	p, _ := New(Config{APIKey: "super-secret-key"})
	if strings.Contains(p.String(), "super-secret-key") {
		t.Fatal("String() leaked the API key")
	}
}

// fixtureGenerateOK is a real-shape Chat Completions success body.
const fixtureGenerateOK = `{
  "id": "chatcmpl-1",
  "object": "chat.completion",
  "created": 1700000000,
  "model": "gpt-4o-mini",
  "choices": [{
    "index": 0,
    "message": {"role": "assistant", "content": "Hello, world!"},
    "finish_reason": "stop"
  }],
  "usage": {"prompt_tokens": 12, "completion_tokens": 5, "total_tokens": 17, "prompt_tokens_details": {"cached_tokens": 4}}
}`

func TestGenerate_HappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("authorization"); got != "Bearer test-key" {
			t.Errorf("authorization = %q", got)
		}
		if r.URL.Path != "/chat/completions" {
			t.Errorf("Path = %q", r.URL.Path)
		}
		var body chatRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Stream {
			t.Error("Generate must not request stream mode")
		}
		if body.Model != "gpt-4o-mini" {
			t.Errorf("Model = %q", body.Model)
		}
		// Two messages: system + user.
		if len(body.Messages) != 2 {
			t.Fatalf("messages = %d", len(body.Messages))
		}
		if body.Messages[0].Role != "system" || body.Messages[1].Role != "user" {
			t.Errorf("roles = %q, %q", body.Messages[0].Role, body.Messages[1].Role)
		}
		// Content should be a plain JSON string for text-only messages.
		var s string
		if err := json.Unmarshal(body.Messages[0].Content, &s); err != nil {
			t.Errorf("system content not a string: %s", body.Messages[0].Content)
		}
		if s != "be terse" {
			t.Errorf("system text = %q", s)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, fixtureGenerateOK)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	resp, err := p.Generate(context.Background(), provider.Request{
		Model: "gpt-4o-mini",
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
	if resp.Usage.InputTokens != 12 || resp.Usage.OutputTokens != 5 {
		t.Errorf("Usage = %+v", resp.Usage)
	}
	if resp.Usage.CacheReadTokens != 4 {
		t.Errorf("CacheReadTokens = %d", resp.Usage.CacheReadTokens)
	}
	if len(resp.ProviderRaw) == 0 {
		t.Error("ProviderRaw should be populated")
	}
}

func TestGenerate_ToolCallsInResponse(t *testing.T) {
	t.Parallel()
	const body = `{
  "id":"chatcmpl-2","object":"chat.completion","created":1,"model":"gpt-4o-mini",
  "choices":[{"index":0,"finish_reason":"tool_calls","message":{
    "role":"assistant","content":null,
    "tool_calls":[
      {"id":"call_1","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Quito\"}"}},
      {"id":"call_2","type":"function","function":{"name":"time","arguments":"{}"}}
    ]
  }}],
  "usage":{"prompt_tokens":10,"completion_tokens":7,"total_tokens":17}
}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	resp, err := p.Generate(context.Background(), provider.Request{
		Model:    "gpt-4o-mini",
		Messages: []schema.Message{schema.UserMessage("anything")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != schema.StopReasonToolUse {
		t.Errorf("StopReason = %q", resp.StopReason)
	}
	if len(resp.Message.ToolCalls) != 2 {
		t.Fatalf("ToolCalls = %d", len(resp.Message.ToolCalls))
	}
	if resp.Message.ToolCalls[0].ID != "call_1" || resp.Message.ToolCalls[0].Name != "weather" {
		t.Errorf("first call = %+v", resp.Message.ToolCalls[0])
	}
	if string(resp.Message.ToolCalls[0].Arguments) != `{"city":"Quito"}` {
		t.Errorf("call_1 args = %s", resp.Message.ToolCalls[0].Arguments)
	}
}

func TestGenerate_401NormalizedToAuth(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"type":"authentication_error","message":"invalid api key","code":"invalid_api_key"}}`)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	_, err := p.Generate(context.Background(), provider.Request{
		Model:    "gpt-4o-mini",
		Messages: []schema.Message{schema.UserMessage("hi")},
	})
	if !errors.Is(err, provider.ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
	var apiErr *provider.APIError
	if !errors.As(err, &apiErr) {
		t.Fatal("err not *APIError")
	}
	if apiErr.Message != "invalid api key" {
		t.Errorf("Message = %q", apiErr.Message)
	}
}

func TestGenerate_429PopulatesRetryAfter(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("retry-after", "11")
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"type":"rate_limit_error","message":"too fast"}}`)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	_, err := p.Generate(context.Background(), provider.Request{
		Model:    "gpt-4o-mini",
		Messages: []schema.Message{schema.UserMessage("hi")},
	})
	if !errors.Is(err, provider.ErrRateLimited) {
		t.Fatalf("err = %v", err)
	}
	var apiErr *provider.APIError
	if !errors.As(err, &apiErr) {
		t.Fatal("err not *APIError")
	}
	if apiErr.RetryAfter != 11 {
		t.Errorf("RetryAfter = %d", apiErr.RetryAfter)
	}
}

func TestGenerate_ContextLengthExceeded(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","message":"too long","code":"context_length_exceeded"}}`)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	_, err := p.Generate(context.Background(), provider.Request{
		Model:    "gpt-4o-mini",
		Messages: []schema.Message{schema.UserMessage("hi")},
	})
	if !errors.Is(err, provider.ErrContextWindow) {
		t.Fatalf("err = %v, want ErrContextWindow", err)
	}
}

func TestGenerate_ContextCanceled(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		<-make(chan struct{}) // hang forever
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.Generate(ctx, provider.Request{
		Model:    "gpt-4o-mini",
		Messages: []schema.Message{schema.UserMessage("hi")},
	})
	if err == nil {
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
		`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
		"",
		`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"Hello, "},"finish_reason":null}]}`,
		"",
		`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"world!"},"finish_reason":null}]}`,
		"",
		`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"",
		`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"gpt-4o-mini","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`,
		"",
		"data: [DONE]",
		"",
		"",
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("accept"); got != "text/event-stream" {
			t.Errorf("accept = %q", got)
		}
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	resp, err := provider.CollectStream(context.Background(), mustStream(t, p,
		provider.Request{
			Model:    "gpt-4o-mini",
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
	if resp.Model != "gpt-4o-mini" {
		t.Errorf("Model = %q", resp.Model)
	}
}

func TestStream_ToolUseAssembled(t *testing.T) {
	t.Parallel()
	body := strings.Join([]string{
		`data: {"id":"c","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"weather","arguments":""}}]}}]}`,
		"",
		`data: {"id":"c","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]}}]}`,
		"",
		`data: {"id":"c","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Quito\"}"}}]}}]}`,
		"",
		`data: {"id":"c","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		"",
		"data: [DONE]",
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
			Model:    "gpt-4o-mini",
			Messages: []schema.Message{schema.UserMessage("weather?")},
		}))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d", len(resp.Message.ToolCalls))
	}
	tc := resp.Message.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "weather" {
		t.Errorf("tool call = %+v", tc)
	}
	if string(tc.Arguments) != `{"city":"Quito"}` {
		t.Errorf("Arguments = %s", tc.Arguments)
	}
	if resp.StopReason != schema.StopReasonToolUse {
		t.Errorf("StopReason = %q", resp.StopReason)
	}
}

func TestStream_4xxBeforeFirstChunk(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","message":"bad model"}}`)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	_, err := p.Stream(context.Background(), provider.Request{
		Model:    "gpt-4o-mini",
		Messages: []schema.Message{schema.UserMessage("hi")},
	})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("err = %v", err)
	}
}

// mustStream is a test helper that opens a stream and fails the test if
// the call returns an error.
func mustStream(t *testing.T, p *Provider, req provider.Request) provider.StreamReader {
	t.Helper()
	s, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
