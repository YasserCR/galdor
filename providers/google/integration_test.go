//go:build integration

package google

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

// These tests hit the real Gemini API (AI Studio) and consume tokens. They
// run only under the "integration" build tag AND when GOOGLE_API_KEY is
// set.
//
//	export GOOGLE_API_KEY=AIza...
//	go test -tags=integration ./providers/google/...
//
// Each test uses minimal prompts to keep cost negligible. The AI Studio
// free tier covers these comfortably.

// testModels are exercised in the basic generation test. Both are
// production aliases. Add others by setting GOOGLE_EXTRA_MODELS to a
// comma-separated list.
var testModels = []string{
	"gemini-2.5-flash",
	"gemini-2.5-flash-lite",
}

func newIntegrationProvider(t *testing.T) *Provider {
	t.Helper()
	key := os.Getenv("GOOGLE_API_KEY")
	if key == "" {
		t.Skip("GOOGLE_API_KEY not set; skipping integration test")
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

func TestIntegration_GenerateBasic_Models(t *testing.T) {
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
			if resp.Usage.InputTokens == 0 {
				t.Errorf("Usage not reported: %+v", resp.Usage)
			}
		})
	}
}

func TestIntegration_Stream(t *testing.T) {
	p := newIntegrationProvider(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	stream, err := p.Stream(ctx, provider.Request{
		Model:     "gemini-2.5-flash",
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
	if os.Getenv("GOOGLE_API_KEY") == "" {
		t.Skip("GOOGLE_API_KEY not set")
	}
	// AI Studio keys start with AIza; this one is syntactically plausible
	// but invalid.
	p, err := New(Config{APIKey: "AIzaInvalid0000000000000000000000000000"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = p.Generate(ctx, provider.Request{
		Model:     "gemini-2.5-flash",
		MaxTokens: smallMaxTokens(),
		Messages:  []schema.Message{schema.UserMessage("hi")},
	})
	if !errors.Is(err, provider.ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
}
