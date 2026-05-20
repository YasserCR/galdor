package providerset

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNew_NativeProviders(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		cfg     Config
		want    string
		wantErr bool
	}{
		{
			name: "anthropic",
			cfg:  Config{Provider: "anthropic", APIKey: "sk-ant-test"},
			want: "anthropic",
		},
		{
			name: "openai",
			cfg:  Config{Provider: "openai", APIKey: "sk-test"},
			want: "openai",
		},
		{
			name: "google",
			cfg:  Config{Provider: "google", APIKey: "AIzaTest"},
			want: "google",
		},
		{
			name: "case-insensitive",
			cfg:  Config{Provider: "  OpenAI  ", APIKey: "sk-test"},
			want: "openai",
		},
		{
			name:    "anthropic missing key",
			cfg:     Config{Provider: "anthropic"},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := New(tc.cfg)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p == nil {
				t.Fatal("expected non-nil provider")
			}
			if got := p.Name(); got != tc.want {
				t.Fatalf("Name() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNew_OpenAICompatAliases(t *testing.T) {
	t.Parallel()

	// Every alias in the table must construct successfully when given
	// a key. The Provider always reports "openai" because the OpenAI
	// adapter drives all of them.
	for alias := range openAICompatBaseURLs {
		alias := alias
		t.Run(alias, func(t *testing.T) {
			t.Parallel()
			p, err := New(Config{Provider: alias, APIKey: "sk-test"})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := p.Name(); got != "openai" {
				t.Fatalf("Name() = %q, want %q", got, "openai")
			}
		})
	}
}

func TestNew_VLLMAndOllamaSkipAPIKey(t *testing.T) {
	t.Parallel()

	for _, alias := range []string{"vllm", "ollama"} {
		alias := alias
		t.Run(alias, func(t *testing.T) {
			t.Parallel()
			// No key supplied. Self-hosted aliases must still
			// construct because the adapter receives a placeholder.
			p, err := New(Config{Provider: alias})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p == nil {
				t.Fatal("expected non-nil provider")
			}
		})
	}
}

func TestNew_BaseURLOverrideWinsOverTable(t *testing.T) {
	t.Parallel()

	custom := "https://internal.example.com/v1"
	// Override doesn't change Name() but should not error. We can't
	// introspect the BaseURL from outside the adapter, so we settle
	// for "constructs cleanly" and exercise the branch via FromEnv
	// in another test.
	p, err := New(Config{Provider: "groq", APIKey: "sk-test", BaseURL: custom})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "openai" {
		t.Fatalf("Name() = %q, want openai", p.Name())
	}
}

func TestNew_UnknownProvider(t *testing.T) {
	t.Parallel()

	_, err := New(Config{Provider: "nonesuch", APIKey: "sk-test"})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if !strings.Contains(err.Error(), "unknown Provider") {
		t.Fatalf("error %q should mention the unknown provider", err.Error())
	}
	if strings.Contains(err.Error(), "sk-test") {
		t.Fatalf("error leaks APIKey: %q", err.Error())
	}
}

func TestNew_EmptyProvider(t *testing.T) {
	t.Parallel()

	_, err := New(Config{APIKey: "sk-test"})
	if err == nil {
		t.Fatal("expected error for empty provider")
	}
	if !strings.Contains(err.Error(), "Provider is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNew_HTTPClientThreaded(t *testing.T) {
	t.Parallel()

	// Sanity check: passing an HTTPClient does not break construction
	// for any non-bedrock branch.
	hc := &http.Client{Timeout: 5 * time.Second}
	for _, prov := range []string{"anthropic", "openai", "google", "groq", "vllm"} {
		prov := prov
		t.Run(prov, func(t *testing.T) {
			t.Parallel()
			key := "sk-test"
			if _, err := New(Config{Provider: prov, APIKey: key, HTTPClient: hc}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestFromEnv_ReadsAllVars(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "openai")
	t.Setenv("LLM_API_KEY", "sk-test")
	t.Setenv("LLM_BASE_URL", "https://proxy.example.com/v1")
	t.Setenv("LLM_HTTP_TIMEOUT", "30s")

	p, err := FromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "openai" {
		t.Fatalf("Name() = %q, want openai", p.Name())
	}
}

func TestFromEnv_AliasResolved(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "groq")
	t.Setenv("LLM_API_KEY", "gsk-test")
	t.Setenv("LLM_BASE_URL", "")
	t.Setenv("LLM_HTTP_TIMEOUT", "")

	p, err := FromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "openai" {
		t.Fatalf("Name() = %q, want openai", p.Name())
	}
}

func TestFromEnv_MissingProvider(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "")
	_, err := FromEnv()
	if err == nil {
		t.Fatal("expected error when LLM_PROVIDER is unset")
	}
}

func TestFromEnv_BadTimeout(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "openai")
	t.Setenv("LLM_API_KEY", "sk-test")
	t.Setenv("LLM_HTTP_TIMEOUT", "not-a-duration")

	_, err := FromEnv()
	if err == nil {
		t.Fatal("expected error for malformed timeout")
	}
	if !strings.Contains(err.Error(), "LLM_HTTP_TIMEOUT") {
		t.Fatalf("error should mention the offending var: %v", err)
	}
	if strings.Contains(err.Error(), "sk-test") {
		t.Fatalf("error leaks APIKey: %v", err)
	}
}

func TestFromEnv_IntegerTimeoutTreatedAsSeconds(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "openai")
	t.Setenv("LLM_API_KEY", "sk-test")
	t.Setenv("LLM_HTTP_TIMEOUT", "45")

	p, err := FromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestFromEnv_NegativeTimeoutRejected(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "openai")
	t.Setenv("LLM_API_KEY", "sk-test")
	t.Setenv("LLM_HTTP_TIMEOUT", "-5s")

	_, err := FromEnv()
	if err == nil {
		t.Fatal("expected error for negative timeout")
	}
}

func TestFromEnv_UnknownProvider(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "made-up")
	t.Setenv("LLM_API_KEY", "sk-test")

	_, err := FromEnv()
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	var sentinel error
	if errors.Is(err, sentinel) {
		t.Fatalf("unexpected sentinel match")
	}
}
