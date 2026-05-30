package schema

import (
	"encoding/json"
	"regexp"
	"strings"
)

// ParseJSON parses LLM output as JSON-encoded T, tolerating the common
// realities of model responses without resorting to LLM-driven repair:
//
//   - Leading and trailing prose ("Sure! Here is the JSON: { ... }").
//   - Markdown code fences (```json ... ``` or bare ``` ... ```).
//   - Surrounding whitespace.
//
// What it does NOT do (intentionally):
//
//   - Repair malformed JSON (single quotes, trailing commas, unquoted
//     keys). Those indicate either a non-JSON-tuned model or a wrong
//     prompt; silent recovery would hide real bugs.
//   - Re-prompt the model. ParseJSON is a pure stdlib helper. The
//     "LLM-as-fixer" pattern belongs in caller code or a future
//     dedicated package, not here.
//   - Validate against a schema. Use schema.JSONOf[T] (Phase 12) for
//     provider-side schema enforcement.
//
// On success, returns the parsed value. On failure, returns a non-nil
// *BadOutputError that wraps the underlying json error and captures
// the (size-capped) raw input for debugging:
//
//	resp, err := p.Generate(ctx, req)
//	if err != nil { return err }
//	parsed, err := schema.ParseJSON[MyShape](resp.Message.Text())
//	if err != nil {
//	    var bad *schema.BadOutputError
//	    if errors.As(err, &bad) {
//	        log.Printf("model returned: %s", bad.Raw)
//	    }
//	    return err
//	}
func ParseJSON[T any](raw string) (T, error) {
	var zero T
	s := strings.TrimSpace(raw)
	s = stripCodeFence(s)
	s = strings.TrimSpace(s)

	// If the trimmed text doesn't start with a JSON token, look for an
	// embedded object/array between prose. Bail out clearly when no
	// candidate exists rather than letting json.Unmarshal produce a
	// confusing "invalid character" error on the prose itself.
	candidate := s
	if !looksLikeJSONStart(s) {
		payload := extractStructured(s)
		if payload == "" {
			return zero, &BadOutputError{
				Provider: "schema",
				Raw:      capRaw(raw),
				Reason:   "no JSON payload found",
			}
		}
		candidate = payload
	}

	var out T
	err := json.Unmarshal([]byte(candidate), &out)
	if err == nil {
		return out, nil
	}

	// The candidate started with a JSON token but didn't parse cleanly —
	// most commonly because the model appended trailing prose after the
	// value ("{\"a\":1}\n\nHope that helps!"). Retry against the outermost
	// object/array clipped from the text before giving up, so leading AND
	// trailing prose are both tolerated as documented.
	if looksLikeJSONStart(s) {
		if payload := extractStructured(s); payload != "" && payload != candidate {
			if err2 := json.Unmarshal([]byte(payload), &out); err2 == nil {
				return out, nil
			}
		}
	}

	return zero, &BadOutputError{
		Provider: "schema",
		Raw:      capRaw(raw),
		Reason:   "invalid JSON: " + err.Error(),
		Cause:    err,
	}
}

// looksLikeJSONStart reports whether s begins with a token that could
// plausibly start a valid JSON document, so callers can skip the
// embedded-payload search when the input is already well-formed.
func looksLikeJSONStart(s string) bool {
	if s == "" {
		return false
	}
	switch s[0] {
	case '{', '[', '"', '-', 't', 'f', 'n':
		return true
	}
	return s[0] >= '0' && s[0] <= '9'
}

// extractStructured returns the first object or array embedded in s,
// from the earliest `{` or `[` to the matching last `}` or `]`. This is
// intentionally lenient: it does not balance braces precisely (LLM
// output containing two top-level objects will round-trip as garbage
// and json.Unmarshal will reject it). Returns "" when no candidate
// exists.
func extractStructured(s string) string {
	objStart := strings.IndexByte(s, '{')
	arrStart := strings.IndexByte(s, '[')

	var start int
	var closer byte
	switch {
	case objStart < 0 && arrStart < 0:
		return ""
	case objStart < 0:
		start, closer = arrStart, ']'
	case arrStart < 0:
		start, closer = objStart, '}'
	case objStart < arrStart:
		start, closer = objStart, '}'
	default:
		start, closer = arrStart, ']'
	}

	end := strings.LastIndexByte(s, closer)
	if end <= start {
		return ""
	}
	return s[start : end+1]
}

// fenceRe matches a fenced code block at both ends of the string.
// Permits an optional language tag (json, JSON) immediately after the
// opening fence.
var fenceRe = regexp.MustCompile("(?s)^```(?:json|JSON)?\\s*(.*?)\\s*```\\s*$")

// stripCodeFence removes a surrounding triple-backtick block when
// present. Idempotent and a no-op on unfenced input.
func stripCodeFence(s string) string {
	if m := fenceRe.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return s
}

// capRaw bounds the captured raw payload that gets attached to
// BadOutputError.Raw, so error strings never balloon when a model
// emits a wall of text.
func capRaw(s string) string {
	const max = 2048
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}
