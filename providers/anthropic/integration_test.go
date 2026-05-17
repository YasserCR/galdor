//go:build integration

package anthropic

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

// These tests hit the real Anthropic API and consume tokens. They run only
// under the "integration" build tag AND when ANTHROPIC_API_KEY is set.
//
//	go test -tags=integration ./providers/anthropic/...
//
// Each test uses minimal prompts to keep cost negligible (well under one
// cent total). MaxTokens is capped at 32 to bound the worst case.

// testModels are the model aliases we exercise. Haiku is the workhorse;
// Sonnet and Opus each get a single basic round-trip to validate that the
// adapter works across the family.
var testModels = []string{
	"claude-haiku-4-5",
	"claude-sonnet-4-6",
	"claude-opus-4-7",
}

func newIntegrationProvider(t *testing.T) *Provider {
	t.Helper()
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		t.Skip("ANTHROPIC_API_KEY not set; skipping integration test")
	}
	p, err := New(Config{APIKey: key})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func smallMaxTokens() *int {
	v := 32
	return &v
}

func TestIntegration_GenerateBasic_AllModels(t *testing.T) {
	p := newIntegrationProvider(t)
	for _, model := range testModels {
		model := model
		t.Run(model, func(t *testing.T) {
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
			text := resp.Message.Text()
			if text == "" {
				t.Fatal("empty reply")
			}
			t.Logf("model=%s reply=%q stop=%s in=%d out=%d",
				model, text, resp.StopReason,
				resp.Usage.InputTokens, resp.Usage.OutputTokens)
			if resp.Usage.InputTokens == 0 || resp.Usage.OutputTokens == 0 {
				t.Errorf("Usage not reported: %+v", resp.Usage)
			}
			if resp.Model == "" {
				t.Error("Response.Model empty")
			}
		})
	}
}

func TestIntegration_Stream_Haiku(t *testing.T) {
	p := newIntegrationProvider(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	stream, err := p.Stream(ctx, provider.Request{
		Model:     "claude-haiku-4-5",
		MaxTokens: smallMaxTokens(),
		Messages: []schema.Message{
			schema.UserMessage("Count from 1 to 3, comma-separated, nothing else."),
		},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()

	var (
		sawStart, sawStop bool
		chunks            int
	)
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
	if !sawStart {
		t.Error("never saw message_start")
	}
	if !sawStop {
		t.Error("never saw message_stop")
	}
	if chunks == 0 {
		t.Error("no content deltas")
	}
}

func TestIntegration_AuthFailure(t *testing.T) {
	// Use a key shape that's syntactically plausible but invalid.
	p, err := New(Config{APIKey: "sk-ant-invalid-test-key-0000000000"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err = p.Generate(ctx, provider.Request{
		Model:     "claude-haiku-4-5",
		MaxTokens: smallMaxTokens(),
		Messages:  []schema.Message{schema.UserMessage("hi")},
	})
	if !errors.Is(err, provider.ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
}
