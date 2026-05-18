package bedrock

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// newTestProvider builds a Provider whose AWS calls go to srv.URL,
// avoiding the real AWS endpoint. Anonymous credentials are used so the
// SDK does not require a real key pair; SigV4 still signs the request
// but the test server is free to ignore the signature.
func newTestProvider(t *testing.T, srv *httptest.Server) *Provider {
	t.Helper()
	cfg := aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("AKIAEXAMPLE", "secret/dummy", ""),
		HTTPClient:  srv.Client(),
	}
	p, err := New(Config{
		AWS: cfg,
		ClientOptions: []func(*bedrockruntime.Options){
			func(o *bedrockruntime.Options) {
				o.BaseEndpoint = aws.String(srv.URL)
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNew_RequiresRegion(t *testing.T) {
	t.Parallel()
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected error for empty Region")
	}
}

func TestProvider_NameAndCapabilities(t *testing.T) {
	t.Parallel()
	p, err := New(Config{AWS: aws.Config{Region: "us-east-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "bedrock" {
		t.Errorf("Name = %q", p.Name())
	}
	caps := p.Capabilities()
	if !caps.Streaming || !caps.ToolCalling || !caps.VisionInput {
		t.Errorf("Capabilities = %+v", caps)
	}
}

func TestProvider_StringHidesCredentials(t *testing.T) {
	t.Parallel()
	p, err := New(Config{AWS: aws.Config{Region: "us-east-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.String(), "Anonymous") {
		t.Error("String() should not leak credential source")
	}
}

// fixtureConverseOK is a real-shape Converse response body.
const fixtureConverseOK = `{
  "metrics": {"latencyMs": 100},
  "output": {
    "message": {
      "role": "assistant",
      "content": [{"text": "Hello, world!"}]
    }
  },
  "stopReason": "end_turn",
  "usage": {"inputTokens": 12, "outputTokens": 5, "totalTokens": 17}
}`

func TestGenerate_HappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/converse") {
			t.Errorf("path = %q", r.URL.Path)
		}
		if !strings.Contains(r.URL.Path, "anthropic.claude") {
			t.Errorf("model not in path: %q", r.URL.Path)
		}
		if r.Header.Get("authorization") == "" {
			// SigV4 still runs even with anonymous creds — header present.
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, fixtureConverseOK)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	resp, err := p.Generate(context.Background(), provider.Request{
		Model: "anthropic.claude-3-7-sonnet-20250219-v1:0",
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
	if len(resp.ProviderRaw) == 0 {
		t.Error("ProviderRaw should be populated (serialized SDK output)")
	}
}

func TestGenerate_ToolCallsInResponse(t *testing.T) {
	t.Parallel()
	const body = `{
  "metrics": {"latencyMs": 100},
  "output": {
    "message": {
      "role": "assistant",
      "content": [
        {"toolUse": {"toolUseId": "tu_1", "name": "weather", "input": {"city": "Quito"}}}
      ]
    }
  },
  "stopReason": "tool_use",
  "usage": {"inputTokens": 10, "outputTokens": 7, "totalTokens": 17}
}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	resp, err := p.Generate(context.Background(), provider.Request{
		Model:    "anthropic.claude-3-7-sonnet-20250219-v1:0",
		Messages: []schema.Message{schema.UserMessage("weather in Quito?")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d", len(resp.Message.ToolCalls))
	}
	tc := resp.Message.ToolCalls[0]
	if tc.ID != "tu_1" || tc.Name != "weather" {
		t.Errorf("tool call = %+v", tc)
	}
	if !strings.Contains(string(tc.Arguments), `"city":"Quito"`) {
		t.Errorf("Arguments = %s", tc.Arguments)
	}
	if resp.StopReason != schema.StopReasonToolUse {
		t.Errorf("StopReason = %q", resp.StopReason)
	}
}

func TestGenerate_403MapsToAuth(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.Header().Set("X-Amzn-ErrorType", "AccessDeniedException")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"message":"not allowed"}`)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	_, err := p.Generate(context.Background(), provider.Request{
		Model:    "anthropic.claude-3-7-sonnet-20250219-v1:0",
		Messages: []schema.Message{schema.UserMessage("hi")},
	})
	if !errors.Is(err, provider.ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
}

func TestGenerate_429MapsToRateLimited(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.Header().Set("X-Amzn-ErrorType", "ThrottlingException")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"message":"slow down"}`)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	_, err := p.Generate(context.Background(), provider.Request{
		Model:    "anthropic.claude-3-7-sonnet-20250219-v1:0",
		Messages: []schema.Message{schema.UserMessage("hi")},
	})
	if !errors.Is(err, provider.ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
}

func TestGenerate_400ValidationMapsToInvalidRequest(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.Header().Set("X-Amzn-ErrorType", "ValidationException")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"message":"bad model"}`)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	_, err := p.Generate(context.Background(), provider.Request{
		Model:    "anthropic.claude-3-7-sonnet-20250219-v1:0",
		Messages: []schema.Message{schema.UserMessage("hi")},
	})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("err = %v, want ErrInvalidRequest", err)
	}
}

func TestGenerate_RejectsEmptyModel(t *testing.T) {
	t.Parallel()
	p, err := New(Config{AWS: aws.Config{Region: "us-east-1"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Generate(context.Background(), provider.Request{
		Messages: []schema.Message{schema.UserMessage("hi")},
	})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("err = %v", err)
	}
}
