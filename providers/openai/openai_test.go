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
	if err := provider.ValidateToolCalls(resp.Message); err != nil {
		t.Fatalf("ValidateToolCalls: %v", err)
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

// TestStream_SurfacesReasoning verifies streamed reasoning_content (e.g.
// DeepSeek) is kept off the live content stream and delivered as a
// thinking part on the terminal MessageStop.
func TestStream_SurfacesReasoning(t *testing.T) {
	t.Parallel()
	body := strings.Join([]string{
		`data: {"model":"deepseek-reasoner","choices":[{"delta":{"role":"assistant","reasoning_content":"think "}}]}`,
		"",
		`data: {"choices":[{"delta":{"reasoning_content":"more"}}]}`,
		"",
		`data: {"choices":[{"delta":{"content":"answer"}}]}`,
		"",
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
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
	stream := mustStream(t, p, provider.Request{
		Model:     "deepseek-reasoner",
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
		stopMsg.Content[0].Type != schema.ContentTypeThinking || stopMsg.Content[0].Text != "think more" {
		t.Fatalf("reasoning part wrong: %+v", stopMsg)
	}
}

// TestGenerate_NumericErrorCode covers the envelope OpenRouter sends on a
// rate limit: error.code is a number where the API documents a string.
// Decoding it used to fail, and since the envelope is decoded in one shot
// the failure took the human-readable message with it — the caller got a
// bare status and no reason.
func TestGenerate_NumericErrorCode(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"code":429,"message":"rate limited upstream"}}`)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	_, err := p.Generate(context.Background(), provider.Request{
		Model:    "gpt-4o-mini",
		Messages: []schema.Message{schema.UserMessage("hi")},
	})
	var apiErr *provider.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err not *APIError: %v", err)
	}
	if apiErr.Message != "rate limited upstream" {
		t.Errorf("Message = %q, want the message to survive the numeric code", apiErr.Message)
	}
	if !errors.Is(err, provider.ErrRateLimited) {
		t.Errorf("err = %v, want ErrRateLimited", err)
	}
}

func TestGenerate_UpstreamMetadataSurvives(t *testing.T) {
	t.Parallel()
	// OpenRouter's documented envelope for an upstream failure: a generic
	// top-level message plus error.metadata carrying the actual cause.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"error":{"code":502,"message":"Provider returned error","metadata":{"raw":"{\"error\":{\"message\":\"model overloaded\"}}","provider_name":"Zhipu"}}}`)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	_, err := p.Generate(context.Background(), provider.Request{
		Model:    "glm-4",
		Messages: []schema.Message{schema.UserMessage("hi")},
	})
	var apiErr *provider.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err not *APIError: %v", err)
	}
	for _, want := range []string{"Provider returned error", "upstream Zhipu", "model overloaded"} {
		if !strings.Contains(apiErr.Message, want) {
			t.Errorf("Message = %q, want it to contain %q", apiErr.Message, want)
		}
	}
}

func TestGenerate_UnparseableErrorBodyKept(t *testing.T) {
	t.Parallel()
	// A body that isn't the expected envelope is still the only account
	// of the failure; it must reach the caller instead of a bare status.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "<html>gateway exploded</html>")
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	_, err := p.Generate(context.Background(), provider.Request{
		Model:    "gpt-4o-mini",
		Messages: []schema.Message{schema.UserMessage("hi")},
	})
	var apiErr *provider.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err not *APIError: %v", err)
	}
	if !strings.Contains(apiErr.Message, "gateway exploded") {
		t.Errorf("Message = %q, want the raw body kept", apiErr.Message)
	}
}

func TestNew_NameOverride(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"error":{"message":"Provider returned error"}}`)
	}))
	defer srv.Close()

	p, err := New(Config{APIKey: "x", BaseURL: srv.URL, Name: "openrouter"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "openrouter" {
		t.Errorf("Name() = %q, want openrouter", p.Name())
	}
	_, err = p.Generate(context.Background(), provider.Request{
		Model:    "glm-4",
		Messages: []schema.Message{schema.UserMessage("hi")},
	})
	var apiErr *provider.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err not *APIError: %v", err)
	}
	if apiErr.Provider != "openrouter" {
		t.Errorf("APIError.Provider = %q, want openrouter", apiErr.Provider)
	}
	if !strings.HasPrefix(err.Error(), "openrouter:") {
		t.Errorf("Error() = %q, want the openrouter: prefix", err.Error())
	}
}

// TestFormatErrorMetadata covers the metadata shapes gateways send: the
// documented {provider_name, raw} pair, partial variants, and junk.
func TestFormatErrorMetadata(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"absent", ``, ""},
		{"null", `null`, ""},
		{"empty object", `{}`, ""},
		{"name and raw string", `{"provider_name":"Zhipu","raw":"model overloaded"}`, "upstream Zhipu: model overloaded"},
		{"name only", `{"provider_name":"Zhipu"}`, "upstream Zhipu"},
		{"raw object only", `{"raw":{"code":500}}`, `upstream: {"code":500}`},
		{"unexpected shape", `[1,2]`, "metadata: [1,2]"},
	}
	for _, tc := range cases {
		var meta json.RawMessage
		if tc.in != "" {
			meta = json.RawMessage(tc.in)
		}
		if got := formatErrorMetadata(meta); got != tc.want {
			t.Errorf("%s: formatErrorMetadata(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestTruncateBody(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", maxErrorBodyLen+100)
	got := truncateBody(long)
	if len(got) > maxErrorBodyLen+len("... (truncated)") {
		t.Errorf("len = %d, want capped", len(got))
	}
	if !strings.HasSuffix(got, "... (truncated)") {
		t.Errorf("truncated body must say so, got suffix %q", got[len(got)-20:])
	}
	if short := truncateBody("ok"); short != "ok" {
		t.Errorf("short body altered: %q", short)
	}
}

// TestFlexString covers the shapes the field arrives in across the
// OpenAI-compatible gateways.
func TestFlexString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{`"context_length_exceeded"`, "context_length_exceeded"},
		{`429`, "429"},
		{`null`, ""},
		{`""`, ""},
	}
	for _, tc := range cases {
		var got flexString
		if err := json.Unmarshal([]byte(tc.in), &got); err != nil {
			t.Fatalf("Unmarshal(%s): %v", tc.in, err)
		}
		if string(got) != tc.want {
			t.Errorf("Unmarshal(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
