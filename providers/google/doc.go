// Package google implements the galdor Provider abstraction against
// Google's Gemini API (the AI Studio / Generative Language surface,
// /v1beta/models/{model}:generateContent).
//
// It speaks the raw HTTP wire format without depending on a third-party
// SDK. The adapter is dedicated rather than reusing the openai
// provider because Gemini's request shape (contents/parts, dedicated
// systemInstruction field, role=model instead of assistant, function
// calls without IDs) is fundamentally different from Chat Completions.
//
//	p, err := google.New(google.Config{
//	    APIKey: os.Getenv("GOOGLE_API_KEY"), // AIza... from AI Studio
//	})
//	if err != nil {
//	    return err
//	}
//	resp, err := p.Generate(ctx, provider.Request{
//	    Model:    "gemini-2.5-flash",
//	    Messages: []schema.Message{schema.UserMessage("hello")},
//	})
//
// For Vertex AI (the GCP-authenticated surface) and grounding /
// code-execution / advanced safety configuration, see the package's
// README — those features are not yet exposed in Phase 1.
package google
