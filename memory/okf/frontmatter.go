package okf

import (
	"strings"
)

// frontmatter parsing for OKF concept documents.
//
// An OKF concept is a Markdown file whose frontmatter is a YAML mapping
// delimited by `---`. Rather than pull a full YAML dependency into this
// module, we parse the small subset the OKF spec's frontmatter actually
// uses: scalar values, quoted strings, inline `[a, b]` lists and block
// `- item` lists. This keeps memory/okf dependency-light (only the galdor
// core + memory/sqlite) and offline-buildable.
//
// Conformance is deliberately permissive, per the OKF spec: unknown keys
// are preserved, and only `type` is required. Authoring tools may enforce
// the stricter (type, title, description, timestamp) set; that is a policy
// left to producers, not to this reader.

const delim = "---"

// splitFrontmatter returns (frontmatterText, body, present). present is
// false when the document has no well-formed frontmatter block — §9's
// first conformance rule requires one on every concept, so callers that
// validate need the distinction; the loader itself stays permissive.
func splitFrontmatter(text string) (string, string, bool) {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != delim {
		return "", text, false
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == delim {
			end = i
			break
		}
	}
	if end == -1 {
		// Unterminated frontmatter: treat the whole thing as body rather
		// than rejecting the document (permissive conformance).
		return "", text, false
	}
	fm := strings.Join(lines[1:end], "\n")
	body := strings.Join(lines[end+1:], "\n")
	body = strings.TrimPrefix(body, "\n")
	return fm, body, true
}

// parseFrontmatter parses the frontmatter subset into a string→value map,
// where a value is either a string or a []string (for lists).
func parseFrontmatter(fm string) map[string]any {
	out := make(map[string]any)
	var curKey string
	var block []string
	haveBlock := false

	flushBlock := func() {
		if haveBlock && curKey != "" {
			out[curKey] = append([]string(nil), block...)
		}
		block = block[:0]
		haveBlock = false
	}

	for _, raw := range strings.Split(fm, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Block list item belonging to the previous key.
		if strings.HasPrefix(line, "- ") && curKey != "" {
			block = append(block, coerce(strings.TrimSpace(line[2:])))
			haveBlock = true
			out[curKey] = append([]string(nil), block...)
			continue
		}
		key, val, ok := splitKeyValue(raw)
		if !ok {
			continue
		}
		flushBlock()
		curKey = key
		val = strings.TrimSpace(val)
		switch {
		case val == "":
			// May be filled by a following block list.
			out[key] = ""
		case strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]"):
			out[key] = parseInlineList(val)
		default:
			out[key] = coerce(val)
		}
	}
	return out
}

// splitKeyValue splits `key: value` on the first colon. It requires the
// key to be a bare identifier so that colons inside values (URIs, times)
// are not misread as key separators.
func splitKeyValue(raw string) (key, val string, ok bool) {
	trimmed := strings.TrimSpace(raw)
	idx := strings.Index(trimmed, ":")
	if idx <= 0 {
		return "", "", false
	}
	key = trimmed[:idx]
	for _, r := range key {
		if !isBareKeyChar(r) {
			return "", "", false
		}
	}
	return key, trimmed[idx+1:], true
}

func isBareKeyChar(r rune) bool {
	return r == '_' ||
		(r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9')
}

func parseInlineList(v string) []string {
	inner := strings.TrimSpace(v[1 : len(v)-1])
	if inner == "" {
		return []string{}
	}
	parts := strings.Split(inner, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, coerce(strings.TrimSpace(p)))
	}
	return out
}

// coerce strips surrounding quotes from a scalar. Booleans and numbers are
// kept as their string form — the OKF fields this module reads (type,
// title, tags, ...) are all strings, so numeric coercion would only lose
// fidelity round-tripping into metadata.
func coerce(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}

// asString returns the value for key as a string ("" if absent or a list).
func asString(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// asList returns the value for key as a []string. A scalar becomes a
// one-element list; absent keys become an empty list.
func asList(m map[string]any, key string) []string {
	v, ok := m[key]
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case []string:
		return t
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	default:
		return nil
	}
}
