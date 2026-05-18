package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/YasserCR/galdor/pkg/schema"
)

// echoProvider is a minimal Provider implementation used to verify that
// the interface contract can be satisfied with idiomatic Go.
type echoProvider struct {
	caps Capabilities
}

func (e *echoProvider) Name() string               { return "echo" }
func (e *echoProvider) Capabilities() Capabilities { return e.caps }
func (e *echoProvider) Stream(ctx context.Context, _ Request) (StreamReader, error) {
	return nil, ErrUnsupported
}

func (e *echoProvider) Generate(ctx context.Context, req Request) (*Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var text string
	if n := len(req.Messages); n > 0 {
		text = req.Messages[n-1].Text()
	}
	return &Response{
		Message:    schema.AssistantMessage(text),
		StopReason: schema.StopReasonEndTurn,
		Model:      req.Model,
	}, nil
}

func TestProvider_InterfaceSatisfied(t *testing.T) {
	t.Parallel()
	var _ Provider = (*echoProvider)(nil)
}

func TestProvider_GenerateEcho(t *testing.T) {
	t.Parallel()
	p := &echoProvider{caps: Capabilities{Streaming: false}}
	resp, err := p.Generate(context.Background(), Request{
		Model:    "echo-1",
		Messages: []schema.Message{schema.UserMessage("ping")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Text() != "ping" {
		t.Errorf("Text = %q", resp.Message.Text())
	}
	if resp.Model != "echo-1" {
		t.Errorf("Model = %q", resp.Model)
	}
	if resp.StopReason != schema.StopReasonEndTurn {
		t.Errorf("StopReason = %q", resp.StopReason)
	}
}

func TestProvider_GenerateContextCanceled(t *testing.T) {
	t.Parallel()
	p := &echoProvider{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Generate(ctx, Request{Model: "x"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestProvider_StreamUnsupported(t *testing.T) {
	t.Parallel()
	p := &echoProvider{}
	_, err := p.Stream(context.Background(), Request{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}
