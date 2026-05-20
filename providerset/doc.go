// Package providerset selects a provider.Provider at runtime so apps can
// swap LLM backends via configuration instead of code edits.
//
// galdor ships one adapter per backend under providers/<name>/, each in
// its own Go module. Apps that need to choose between them at startup
// normally end up writing a switch over an env var. providerset owns
// that switch: pass a Config (or call FromEnv) and get a Provider back.
//
//	p, err := providerset.New(providerset.Config{
//	    Provider: "openai",
//	    APIKey:   os.Getenv("OPENAI_API_KEY"),
//	})
//
// OpenAI-compatible endpoints (groq, together, mistral, minimax,
// deepseek, vllm, ollama) are exposed as aliases that resolve to the
// providers/openai adapter with a preset BaseURL. Override BaseURL on
// the Config when pointing at a self-hosted gateway.
//
//	p, err := providerset.New(providerset.Config{
//	    Provider: "groq",
//	    APIKey:   os.Getenv("GROQ_API_KEY"),
//	})
//
// FromEnv reads LLM_PROVIDER, LLM_API_KEY, LLM_BASE_URL and
// LLM_HTTP_TIMEOUT. It is the path most apps want.
package providerset
