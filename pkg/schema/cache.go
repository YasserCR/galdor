package schema

// CacheControl is a per-message hint to the provider that the prompt prefix
// up to and including this message may be cached for reuse across requests.
//
// Cache semantics are provider-specific. Providers that do not support
// caching ignore the hint. The Type field follows Anthropic's vocabulary
// ("ephemeral") because they introduced the first widely adopted spec;
// other providers map this to their nearest equivalent.
type CacheControl struct {
	Type string `json:"type"`
}

// Ephemeral is the conventional "short-lived cache" cache-control directive.
const CacheTypeEphemeral = "ephemeral"

// EphemeralCache returns a CacheControl directive for short-lived caching.
func EphemeralCache() *CacheControl {
	return &CacheControl{Type: CacheTypeEphemeral}
}
