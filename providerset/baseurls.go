package providerset

// openAICompatBaseURLs maps an alias to the default BaseURL for an
// OpenAI-compatible provider. The providers/openai adapter drives all
// of these — the only thing that changes per vendor is the endpoint
// and the API key.
//
// Sources (verified against each vendor's public docs at the time of
// writing; URLs are stable but may shift, so callers can always
// override via Config.BaseURL):
//
//   - groq:     https://api.groq.com/openai/v1
//   - together: https://api.together.xyz/v1
//   - mistral:  https://api.mistral.ai/v1
//   - minimax:  https://api.minimaxi.chat/v1
//   - deepseek: https://api.deepseek.com/v1
//   - vllm:     http://localhost:8000/v1   (self-hosted default)
//   - ollama:   http://localhost:11434/v1  (self-hosted default)
var openAICompatBaseURLs = map[string]string{
	"groq":     "https://api.groq.com/openai/v1",
	"together": "https://api.together.xyz/v1",
	"mistral":  "https://api.mistral.ai/v1",
	"minimax":  "https://api.minimaxi.chat/v1",
	"deepseek": "https://api.deepseek.com/v1",
	"vllm":     "http://localhost:8000/v1",
	"ollama":   "http://localhost:11434/v1",
}

// openAICompatNoAuth lists aliases that resolve to a self-hosted
// endpoint and do not require an API key. providers/openai still
// rejects an empty APIKey, so we substitute a placeholder when none
// is supplied for these.
var openAICompatNoAuth = map[string]bool{
	"vllm":   true,
	"ollama": true,
}
