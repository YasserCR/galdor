package anthropic

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

// newTestProvider returns a Provider pointed at srv with a fake key.
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
	if p.apiVersion != defaultAPIVersion {
		t.Errorf("apiVersion = %q", p.apiVersion)
	}
	if p.httpClient == nil {
		t.Error("httpClient must not be nil")
	}
}

func TestProvider_NameAndCapabilities(t *testing.T) {
	t.Parallel()
	p, _ := New(Config{APIKey: "x"})
	if p.Name() != "anthropic" {
		t.Errorf("Name = %q", p.Name())
	}
	caps := p.Capabilities()
	if !caps.Streaming || !caps.ToolCalling || !caps.PromptCaching || !caps.VisionInput {
		t.Errorf("Capabilities = %+v", caps)
	}
}

func TestProvider_StringHidesKey(t *testing.T) {
	t.Parallel()
	p, _ := New(Config{APIKey: "super-secret-key"})
	if strings.Contains(p.String(), "super-secret-key") {
		t.Fatal("String() leaked the API key")
	}
}

// fixtureGenerateOK is a real-shape Anthropic /v1/messages success body.
const fixtureGenerateOK = `{
  "id": "msg_01ABC",
  "type": "message",
  "role": "assistant",
  "model": "claude-haiku-4-5",
  "content": [{"type": "text", "text": "Hello, world!"}],
  "stop_reason": "end_turn",
  "stop_sequence": null,
  "usage": {"input_tokens": 12, "output_tokens": 5, "cache_read_input_tokens": 4}
}`

func TestGenerate_HappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Errorf("x-api-key = %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got != defaultAPIVersion {
			t.Errorf("anthropic-version = %q", got)
		}
		if r.URL.Path != "/v1/messages" {
			t.Errorf("Path = %q", r.URL.Path)
		}
		var body messageRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Stream {
			t.Error("Generate must not request stream mode")
		}
		if body.Model != "claude-haiku-4-5" {
			t.Errorf("Model = %q", body.Model)
		}
		if len(body.System) != 1 || body.System[0].Text != "be terse" {
			t.Errorf("System extraction wrong: %+v", body.System)
		}
		if len(body.Messages) != 1 || body.Messages[0].Role != "user" {
			t.Errorf("Messages = %+v", body.Messages)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, fixtureGenerateOK)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	resp, err := p.Generate(context.Background(), provider.Request{
		Model: "claude-haiku-4-5",
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
	if resp.Model != "claude-haiku-4-5" {
		t.Errorf("Model = %q", resp.Model)
	}
	if len(resp.ProviderRaw) == 0 {
		t.Error("ProviderRaw should be populated")
	}
}

func TestGenerate_4xxNormalizedToAuthError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid api key"}}`)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	_, err := p.Generate(context.Background(), provider.Request{
		Model:    "claude-haiku-4-5",
		Messages: []schema.Message{schema.UserMessage("hi")},
	})
	if !errors.Is(err, provider.ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
	var apiErr *provider.APIError
	if !errors.As(err, &apiErr) {
		t.Fatal("err not *APIError")
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d", apiErr.StatusCode)
	}
	if apiErr.Message != "invalid api key" {
		t.Errorf("Message = %q", apiErr.Message)
	}
}

func TestGenerate_429PopulatesRetryAfter(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("retry-after", "7")
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	_, err := p.Generate(context.Background(), provider.Request{
		Model:    "claude-haiku-4-5",
		Messages: []schema.Message{schema.UserMessage("hi")},
	})
	if !errors.Is(err, provider.ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	var apiErr *provider.APIError
	if !errors.As(err, &apiErr) {
		t.Fatal("err not *APIError")
	}
	if apiErr.RetryAfter != 7 {
		t.Errorf("RetryAfter = %d", apiErr.RetryAfter)
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
		Model:    "claude-haiku-4-5",
		Messages: []schema.Message{schema.UserMessage("hi")},
	})
	if err == nil {
		t.Fatal("expected an error")
	}
}

// fixtureGenerateToolUse is a real-shape Anthropic /v1/messages success body
// where the model invoked two tools in parallel in a single assistant turn.
const fixtureGenerateToolUse = `{
  "id": "msg_01TU",
  "type": "message",
  "role": "assistant",
  "model": "claude-haiku-4-5",
  "content": [
    {"type": "text", "text": "checking..."},
    {"type": "tool_use", "id": "tu_a", "name": "weather", "input": {"city": "Quito"}},
    {"type": "tool_use", "id": "tu_b", "name": "time", "input": {}}
  ],
  "stop_reason": "tool_use",
  "stop_sequence": null,
  "usage": {"input_tokens": 18, "output_tokens": 11}
}`

func TestGenerate_ToolUseRoundtrip(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body messageRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Tools) != 2 {
			t.Errorf("expected 2 tools in request, got %d", len(body.Tools))
		}
		if body.ToolChoice == nil {
			t.Error("ToolChoice must be forwarded when set on the Request")
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, fixtureGenerateToolUse)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	resp, err := p.Generate(context.Background(), provider.Request{
		Model:    "claude-haiku-4-5",
		Messages: []schema.Message{schema.UserMessage("weather and time in Quito?")},
		Tools: []schema.ToolDef{
			{Name: "weather", Description: "get weather", Schema: []byte(`{"type":"object"}`)},
			{Name: "time", Description: "get current time", Schema: []byte(`{"type":"object"}`)},
		},
		ToolChoice: provider.ToolChoiceAuto,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != schema.StopReasonToolUse {
		t.Errorf("StopReason = %q", resp.StopReason)
	}
	if got := resp.Message.Text(); got != "checking..." {
		t.Errorf("Text = %q", got)
	}
	if len(resp.Message.ToolCalls) != 2 {
		t.Fatalf("ToolCalls = %d, want 2 (parallel calls)", len(resp.Message.ToolCalls))
	}
	if err := provider.ValidateToolCalls(resp.Message); err != nil {
		t.Fatalf("ValidateToolCalls: %v", err)
	}
	first := resp.Message.ToolCalls[0]
	if first.ID != "tu_a" || first.Name != "weather" {
		t.Errorf("first call = %+v", first)
	}
	if !strings.Contains(string(first.Arguments), `"Quito"`) {
		t.Errorf("first Arguments = %s", first.Arguments)
	}
	second := resp.Message.ToolCalls[1]
	if second.ID != "tu_b" || second.Name != "time" {
		t.Errorf("second call = %+v", second)
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

func TestBuildRequest_ToolResultsFoldIntoUserMessage(t *testing.T) {
	t.Parallel()
	req := provider.Request{
		Model: "claude-haiku-4-5",
		Messages: []schema.Message{
			schema.UserMessage("what time is it?"),
			{
				Role: schema.RoleAssistant,
				ToolCalls: []schema.ToolCall{
					{ID: "tu_1", Name: "time", Arguments: json.RawMessage(`{}`)},
				},
			},
			schema.ToolResultMessage("tu_1", "16:32"),
		},
	}
	wire, err := buildRequest(req, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(wire.Messages) != 3 {
		t.Fatalf("messages = %d", len(wire.Messages))
	}
	last := wire.Messages[2]
	if last.Role != "user" {
		t.Errorf("tool result must be on a user message, got role=%q", last.Role)
	}
	if len(last.Content) != 1 || last.Content[0].Type != "tool_result" || last.Content[0].ToolUseID != "tu_1" {
		t.Errorf("tool_result wiring: %+v", last.Content)
	}
}

func TestBuildRequest_ImageBase64(t *testing.T) {
	t.Parallel()
	req := provider.Request{
		Model: "claude-haiku-4-5",
		Messages: []schema.Message{{
			Role: schema.RoleUser,
			Content: []schema.ContentPart{
				schema.TextPart("describe this"),
				schema.ImagePartData([]byte{0x89, 0x50, 0x4e, 0x47}, "image/png"),
			},
		}},
	}
	wire, err := buildRequest(req, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(wire.Messages[0].Content) != 2 {
		t.Fatalf("blocks = %d", len(wire.Messages[0].Content))
	}
	img := wire.Messages[0].Content[1]
	if img.Type != "image" || img.Source == nil || img.Source.Type != "base64" || img.Source.MediaType != "image/png" {
		t.Errorf("image wiring: %+v", img)
	}
	if img.Source.Data == "" {
		t.Error("base64 data missing")
	}
}

func TestToolChoiceMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   provider.ToolChoice
		want string
	}{
		{provider.ToolChoiceAuto, "auto"},
		{provider.ToolChoiceNone, "none"},
		{provider.ToolChoiceRequired, "any"}, // mapped to Anthropic vocabulary
	}
	for _, c := range cases {
		t.Run(string(c.in), func(t *testing.T) {
			t.Parallel()
			got := toolChoiceToWire(c.in)
			if got == nil || got.Type != c.want {
				t.Errorf("got %+v, want type=%q", got, c.want)
			}
		})
	}
	if toolChoiceToWire(provider.ToolChoice("")) != nil {
		t.Error("empty ToolChoice should map to nil (unset)")
	}
}

func TestStream_HappyPath(t *testing.T) {
	t.Parallel()
	// Construct a minimal but realistic SSE body covering the happy path:
	// message_start, content_block_start (text), two text deltas,
	// content_block_stop, message_delta with stop_reason, message_stop.
	body := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg_1","model":"claude-haiku-4-5","usage":{"input_tokens":7,"output_tokens":0}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello, "}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"world!"}}`,
		"",
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
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
			Model:    "claude-haiku-4-5",
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
}

func TestStream_SynthesizesStopOnTruncation(t *testing.T) {
	t.Parallel()
	// SSE body that drops after a text delta — no message_delta or
	// message_stop frame, as happens on a mid-stream connection drop.
	body := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg_1","model":"claude-haiku-4-5","usage":{"input_tokens":7,"output_tokens":0}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
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
		Model:    "claude-haiku-4-5",
		Messages: []schema.Message{schema.UserMessage("hi")},
	})
	defer func() { _ = stream.Close() }()

	var stopEv *provider.Event
	for {
		ev, err := stream.Recv(context.Background())
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		if ev.Type == provider.EventMessageStop {
			ev := ev
			stopEv = &ev
		}
	}
	if stopEv == nil {
		t.Fatal("expected a synthesized EventMessageStop on truncated stream")
	}
	if stopEv.Model != "claude-haiku-4-5" || stopEv.Usage.InputTokens != 7 {
		t.Errorf("synthesized stop lost accumulated state: %+v", *stopEv)
	}
}

func TestStream_ToolUseAssembled(t *testing.T) {
	t.Parallel()
	body := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"m","model":"claude-haiku-4-5","usage":{"input_tokens":1,"output_tokens":0}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tu_1","name":"weather"}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"Quito\"}"}}`,
		"",
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":2}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
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
			Model:    "claude-haiku-4-5",
			Messages: []schema.Message{schema.UserMessage("weather in Quito?")},
		}))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %d", len(resp.Message.ToolCalls))
	}
	tc := resp.Message.ToolCalls[0]
	if tc.ID != "tu_1" || tc.Name != "weather" {
		t.Errorf("tool call = %+v", tc)
	}
	if string(tc.Arguments) != `{"city":"Quito"}` {
		t.Errorf("Arguments = %s", tc.Arguments)
	}
	if resp.StopReason != schema.StopReasonToolUse {
		t.Errorf("StopReason = %q", resp.StopReason)
	}
}

func TestStream_ErrorEvent(t *testing.T) {
	t.Parallel()
	body := "event: error\n" +
		`data: {"type":"error","error":{"type":"overloaded_error","message":"servers busy"}}` + "\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	stream, err := p.Stream(context.Background(), provider.Request{
		Model:    "claude-haiku-4-5",
		Messages: []schema.Message{schema.UserMessage("hi")},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	_, err = stream.Recv(context.Background())
	if !errors.Is(err, provider.ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited (overloaded_error)", err)
	}
}

func TestStream_4xxBeforeFirstEvent(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"bad model"}}`)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	_, err := p.Stream(context.Background(), provider.Request{
		Model:    "claude-haiku-4-5",
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

// TestStream_SurfacesReasoning verifies streamed thinking_delta /
// signature_delta are kept off the live content stream and delivered as
// a thinking part on the terminal MessageStop.
func TestStream_SurfacesReasoning(t *testing.T) {
	t.Parallel()
	body := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"m","model":"claude-haiku-4-5","usage":{"input_tokens":1,"output_tokens":0}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"let me "}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"reason"}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig123"}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"answer"}}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
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
		Model:     "claude-haiku-4-5",
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
	if stopMsg == nil {
		t.Fatal("terminal stop carried no Message")
	}
	if stopMsg.Text() != "" {
		t.Errorf("stop Message.Text() = %q, want empty (reasoning-only)", stopMsg.Text())
	}
	if len(stopMsg.Content) != 1 || stopMsg.Content[0].Type != schema.ContentTypeThinking ||
		stopMsg.Content[0].Text != "let me reason" || stopMsg.Content[0].Signature != "sig123" {
		t.Errorf("reasoning part wrong: %+v", stopMsg.Content)
	}
}
