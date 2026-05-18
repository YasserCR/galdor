//go:build integration

package bedrock

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// These tests hit real AWS Bedrock and consume tokens. They run only
// under the "integration" build tag AND when the AWS SDK can resolve
// credentials from the default chain (env vars, ~/.aws/credentials,
// IAM role, SSO, etc.). The test model is picked from BEDROCK_TEST_MODEL_ID
// (default: anthropic.claude-3-5-haiku-20241022-v1:0, which is cheap).
//
//	export AWS_REGION=us-east-1
//	export AWS_ACCESS_KEY_ID=...
//	export AWS_SECRET_ACCESS_KEY=...
//	# optional:
//	export BEDROCK_TEST_MODEL_ID=anthropic.claude-3-5-haiku-20241022-v1:0
//	go test -tags=integration ./providers/bedrock/...
//
// All prompts are minimal and MaxTokens is capped at 32 so a full run
// costs only fractions of a cent on the cheapest Claude Haiku rates.

func newIntegrationProvider(t *testing.T) (*Provider, string) {
	t.Helper()

	// Refuse to attempt the call unless the user has explicitly opted in.
	// We don't want CI runners that happen to have IAM roles to silently
	// burn tokens on every PR.
	if os.Getenv("AWS_ACCESS_KEY_ID") == "" && os.Getenv("AWS_PROFILE") == "" {
		t.Skip("AWS_ACCESS_KEY_ID / AWS_PROFILE not set; skipping Bedrock integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		t.Fatalf("LoadDefaultConfig: %v", err)
	}
	if awsCfg.Region == "" {
		t.Skip("AWS region not configured; skipping")
	}

	p, err := New(Config{AWS: awsCfg})
	if err != nil {
		t.Fatal(err)
	}

	model := os.Getenv("BEDROCK_TEST_MODEL_ID")
	if model == "" {
		model = "anthropic.claude-3-5-haiku-20241022-v1:0"
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
