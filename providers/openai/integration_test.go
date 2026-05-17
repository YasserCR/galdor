//go:build integration

package openai

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// These tests hit a real OpenAI-compatible API and consume tokens. They run
// only under the "integration" build tag AND when OPENAI_API_KEY is set.
//
//	export OPENAI_API_KEY=sk-...
//	go test -tags=integration ./providers/openai/...
//
// To target an OpenAI-compatible backend (Groq, Together, MiniMax,
// Mistral, ...) set OPENAI_BASE_URL too, e.g.:
//
//	export OPENAI_BASE_URL=https://api.minimax.io/v1
//	export OPENAI_MODEL=MiniMax-M1
//	go test -tags=integration ./providers/openai/...
//
// Each test uses minimal prompts to keep cost negligible. MaxTokens is
// capped at 32 to bound the worst case.

func newIntegrationProvider(t *testing.T) (*Provider, string) {
	t.Helper()
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("OPENAI_API_KEY not set; skipping integration test")
	}
	cfg := Config{APIKey: key}
	if base := os.Getenv("OPENAI_BASE_URL"); base != "" {
		cfg.BaseURL = base
	}
	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	model := os.Getenv("OPENAI_MODEL")
	if model == "" {
		model = "gpt-4o-mini"
	}
	return p, model
}

func smallMaxTokens() *int {
	v := 32
	return &v
}

func TestIntegration_GenerateBasic(t *testing.T) {
	p, model := newIntegrationProvider(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := p.Generate(ctx, provider.Request{
		Model:     model,
		MaxTokens: smallMaxTokens(),
		Messages: []schema.Message{
			schema.SystemMessage("Reply with a single word."),
			schema.UserMessage("Say 'pong'."),
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Message.Text() == "" {
		t.Fatal("empty reply")
	}
	t.Logf("model=%s reply=%q stop=%s in=%d out=%d",
		model, resp.Message.Text(), resp.StopReason,
		resp.Usage.InputTokens, resp.Usage.OutputTokens)
	if resp.Usage.InputTokens == 0 || resp.Usage.OutputTokens == 0 {
		t.Errorf("Usage not reported: %+v", resp.Usage)
	}
}

func TestIntegration_Stream(t *testing.T) {
	p, model := newIntegrationProvider(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	stream, err := p.Stream(ctx, provider.Request{
		Model:     model,
		MaxTokens: smallMaxTokens(),
		Messages: []schema.Message{
			schema.UserMessage("Count 1 to 3, comma-separated, nothing else."),
		},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()

	var sawStart, sawStop bool
	var chunks int
	for {
		ev, err := stream.Recv(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		switch ev.Type {
		case provider.EventMessageStart:
			sawStart = true
		case provider.EventContentDelta:
			chunks++
		case provider.EventMessageStop:
			sawStop = true
			t.Logf("stop reason=%s in=%d out=%d", ev.StopReason, ev.Usage.InputTokens, ev.Usage.OutputTokens)
		}
	}
	if !sawStart || !sawStop || chunks == 0 {
		t.Errorf("stream invariants: start=%v stop=%v chunks=%d", sawStart, sawStop, chunks)
	}
}

func TestIntegration_AuthFailure(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("OPENAI_API_KEY not set")
	}
	cfg := Config{APIKey: "sk-invalid-key-0000000000000000"}
	if base := os.Getenv("OPENAI_BASE_URL"); base != "" {
		cfg.BaseURL = base
	}
	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	model := os.Getenv("OPENAI_MODEL")
	if model == "" {
		model = "gpt-4o-mini"
	}
	_, err = p.Generate(ctx, provider.Request{
		Model:     model,
		MaxTokens: smallMaxTokens(),
		Messages:  []schema.Message{schema.UserMessage("hi")},
	})
	if !errors.Is(err, provider.ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
}
