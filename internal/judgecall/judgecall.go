// Package judgecall holds the one-shot LLM-as-judge plumbing shared by
// pkg/eval and pkg/guardrail: validate the provider/model pair and run
// a single system+user Generate call. Centralizing it means judge-wide
// changes (prompt hardening, retries, request shape) land in one place
// and reach both judges.
package judgecall

import (
	"context"
	"errors"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// Do runs one judge call — the system prompt plus a single user message,
// capped at maxTokens — and returns the judge's reply message.
func Do(ctx context.Context, p provider.Provider, model, system string, user schema.Message, maxTokens int) (schema.Message, error) {
	if p == nil {
		return schema.Message{}, errors.New("judge provider is nil")
	}
	if model == "" {
		return schema.Message{}, errors.New("judge model is empty")
	}
	resp, err := p.Generate(ctx, provider.Request{
		Model:     model,
		MaxTokens: &maxTokens,
		Messages: []schema.Message{
			schema.SystemMessage(system),
			user,
		},
	})
	if err != nil {
		return schema.Message{}, err
	}
	return resp.Message, nil
}
