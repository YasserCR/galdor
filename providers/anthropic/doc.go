// Package anthropic implements the galdor Provider abstraction against
// Anthropic's Messages API.
//
// It speaks the raw HTTP wire format (api.anthropic.com/v1/messages) so
// the code is a readable reference for future adapters. No external SDK
// is imported; the only runtime dependency is the parent galdor module.
//
// Construct an adapter with anthropic.New(Config{...}) and use it
// anywhere a provider.Provider is expected:
//
//	p, err := anthropic.New(anthropic.Config{
//	    APIKey: os.Getenv("ANTHROPIC_API_KEY"),
//	})
//	if err != nil {
//	    return err
//	}
//	resp, err := p.Generate(ctx, provider.Request{
//	    Model:    "claude-haiku-4-5",
//	    Messages: []schema.Message{schema.UserMessage("hello")},
//	    MaxTokens: ptr(256),
//	})
package anthropic
