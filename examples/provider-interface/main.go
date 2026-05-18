// Command provider-interface demonstrates galdor's Provider abstraction
// using an in-process stub implementation. It does not call any external
// API. Run with:
//
//	go run ./examples/provider-interface
//
// Real adapters live under providers/<name>/ in their own Go modules; this
// example targets only the interface and shared types.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// stubProvider is a tiny in-memory Provider that uppercases the last user
// message. It illustrates how a real adapter is shaped — Name, Capabilities,
// Generate and Stream — without hitting the network.
type stubProvider struct{}

func (stubProvider) Name() string { return "stub" }

func (stubProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Streaming:        true,
		ToolCalling:      false,
		StructuredOutput: false,
		PromptCaching:    false,
		VisionInput:      false,
		MaxContextTokens: 8192,
	}
}

func (s stubProvider) Generate(ctx context.Context, req provider.Request) (*provider.Response, error) {
	stream, err := s.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	return provider.CollectStream(ctx, stream)
}

func (stubProvider) Stream(_ context.Context, req provider.Request) (provider.StreamReader, error) {
	var last string
	if n := len(req.Messages); n > 0 {
		last = req.Messages[n-1].Text()
	}
	reply := strings.ToUpper(last)

	chunks := []string{}
	const chunkSize = 4
	for i := 0; i < len(reply); i += chunkSize {
		end := i + chunkSize
		if end > len(reply) {
			end = len(reply)
		}
		chunks = append(chunks, reply[i:end])
	}

	events := make([]provider.Event, 0, 1+len(chunks)+1)
	events = append(events, provider.Event{Type: provider.EventMessageStart, Model: req.Model})
	for _, c := range chunks {
		events = append(events, provider.Event{Type: provider.EventContentDelta, ContentDelta: c})
	}
	events = append(events, provider.Event{
		Type:       provider.EventMessageStop,
		StopReason: schema.StopReasonEndTurn,
		Usage:      schema.Usage{InputTokens: len(last), OutputTokens: len(reply)},
	})

	return &sliceStream{events: events}, nil
}

type sliceStream struct {
	events []provider.Event
	pos    int
}

func (s *sliceStream) Recv(ctx context.Context) (provider.Event, error) {
	if err := ctx.Err(); err != nil {
		return provider.Event{}, err
	}
	if s.pos >= len(s.events) {
		return provider.Event{}, io.EOF
	}
	ev := s.events[s.pos]
	s.pos++
	return ev, nil
}

func (s *sliceStream) Close() error { return nil }

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	var p provider.Provider = stubProvider{}
	ctx := context.Background()

	req := provider.Request{
		Model: "stub-v0",
		Messages: []schema.Message{
			schema.SystemMessage("you are a shouting echo"),
			schema.UserMessage("hello galdor"),
		},
	}

	fmt.Printf("provider:     %s\n", p.Name())
	fmt.Printf("capabilities: %+v\n\n", p.Capabilities())

	resp, err := p.Generate(ctx, req)
	if err != nil {
		return fmt.Errorf("generate: %w", err)
	}
	fmt.Printf("Generate -> %q (stop=%s, tokens=%d/%d)\n",
		resp.Message.Text(),
		resp.StopReason,
		resp.Usage.InputTokens,
		resp.Usage.OutputTokens,
	)

	stream, err := p.Stream(ctx, req)
	if err != nil {
		return fmt.Errorf("stream: %w", err)
	}
	defer func() { _ = stream.Close() }()

	fmt.Print("Stream   -> ")
	for {
		ev, err := stream.Recv(ctx)
		if errors.Is(err, io.EOF) {
			fmt.Println()
			return nil
		}
		if err != nil {
			return fmt.Errorf("recv: %w", err)
		}
		if ev.Type == provider.EventContentDelta {
			fmt.Print(ev.ContentDelta)
		}
	}
}
