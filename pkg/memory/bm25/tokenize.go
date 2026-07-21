// Package bm25 is galdor's native lexical retrieval backend: a code-aware
// tokenizer feeding an in-memory BM25 inverted index. It implements
// memory.Store, so it drops in wherever a lexical store is expected
// (notably as the OKF backend and as the lexical source of a
// memory.HybridRetriever).
//
// Unlike a stock full-text tokenizer, the default CodeTokenizer keeps a
// compound identifier AND emits its parts: "customer_id" indexes as
// customer_id, customer and id, and "customerId" the same. A query for the
// whole identifier, or for any part, therefore matches — the behavior an
// external SQLite FTS5 tokenizer (which the pure-Go driver cannot replace)
// could not provide.
package bm25

import (
	"strings"
	"unicode"
)

// Tokenizer converts text into the terms an index stores and a query
// matches. The SAME Tokenizer must be used for documents and queries.
// Implementations must be deterministic.
type Tokenizer interface {
	Tokenize(text string) []string
}

// CodeTokenizer is the default Tokenizer. It folds case, splits text on any
// non-identifier character, and — this is the point — for every compound
// identifier it emits both the whole token and its constituent parts, split
// on underscores and camelCase boundaries. Plain words emit a single token,
// so ordinary prose is tokenized exactly as a lexical index would expect.
type CodeTokenizer struct{}

// Tokenize implements Tokenizer.
func (CodeTokenizer) Tokenize(text string) []string {
	var out []string
	forEachIdentifierRun(text, func(run string) {
		whole := strings.ToLower(run)
		out = append(out, whole)
		for _, part := range splitIdentifier(run) {
			if part != whole {
				out = append(out, part)
			}
		}
	})
	return out
}

// Tokenize is a convenience wrapper over the default CodeTokenizer.
func Tokenize(text string) []string { return CodeTokenizer{}.Tokenize(text) }

// forEachIdentifierRun calls fn for every maximal run of identifier
// characters (letters, digits, underscore); every other rune is a boundary.
func forEachIdentifierRun(text string, fn func(run string)) {
	start := -1
	for i, r := range text {
		if isIdentRune(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			fn(text[start:i])
			start = -1
		}
	}
	if start >= 0 {
		fn(text[start:])
	}
}

func isIdentRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// splitIdentifier breaks a run into its lowercased sub-parts, splitting on
// underscores and camelCase boundaries. "customer_id" -> [customer, id];
// "customerId" -> [customer, id]; "HTTPServer" -> [http, server]; "mrr" ->
// [mrr].
func splitIdentifier(run string) []string {
	var parts []string
	for _, seg := range strings.Split(run, "_") {
		for _, camel := range splitCamel(seg) {
			if camel != "" {
				parts = append(parts, strings.ToLower(camel))
			}
		}
	}
	return parts
}

// splitCamel splits a separator-free segment on camelCase boundaries: a
// lower/digit followed by an upper starts a new word, and an acronym run of
// uppers keeps its last letter with the following lowercase word
// ("HTTPServer" -> HTTP, Server).
func splitCamel(seg string) []string {
	runes := []rune(seg)
	if len(runes) < 2 {
		return []string{seg}
	}
	var out []string
	start := 0
	for i := 1; i < len(runes); i++ {
		prev, cur := runes[i-1], runes[i]
		boundary := (isLowerOrDigit(prev) && unicode.IsUpper(cur)) ||
			(unicode.IsUpper(prev) && unicode.IsUpper(cur) &&
				i+1 < len(runes) && unicode.IsLower(runes[i+1]))
		if boundary {
			out = append(out, string(runes[start:i]))
			start = i
		}
	}
	return append(out, string(runes[start:]))
}

func isLowerOrDigit(r rune) bool {
	return unicode.IsDigit(r) || unicode.IsLower(r)
}
