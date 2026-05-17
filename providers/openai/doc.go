// Package openai implements the galdor Provider abstraction against
// OpenAI's Chat Completions API (/v1/chat/completions).
//
// It speaks the raw HTTP wire format without depending on a third-party
// SDK. Because the OpenAI Chat Completions surface is the de facto wire
// standard for many providers (Groq, Together AI, Fireworks, MiniMax,
// Mistral La Plateforme, DeepInfra, Anyscale, ...), this same adapter
// targets any of them via the BaseURL configuration field. See
// README.md for the list of validated endpoints.
//
//	p, err := openai.New(openai.Config{
//	    APIKey: os.Getenv("OPENAI_API_KEY"),
//	})
//	if err != nil {
//	    return err
//	}
//	resp, err := p.Generate(ctx, provider.Request{
//	    Model:    "gpt-4o-mini",
//	    Messages: []schema.Message{schema.UserMessage("hello")},
//	})
package openai
