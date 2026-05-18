package store

import (
	"context"
	"fmt"
	"sort"
)

// Stats summarizes a slice of spans for a single grouping (overall,
// per provider, per model, ...). All counts are derived from the
// same row scan so the values are mutually consistent.
type Stats struct {
	Key          string // grouping value: "" for overall, "anthropic" / "claude-haiku-4-5" / ...
	SpanCount    int    // total spans in the group
	ErrorCount   int    // spans with status_code = 'error'
	InputTokens  int    // sum of gen_ai.usage.input_tokens, when present
	OutputTokens int    // sum of gen_ai.usage.output_tokens, when present
	LatencyP50Ns int64  // 50th percentile of (end - start), nanoseconds
	LatencyP95Ns int64
	LatencyP99Ns int64
	LatencyMaxNs int64
}

// OverallStats returns a single Stats row computed over every span
// in the store. Useful for the headline numbers in
// `galdor scry stats`.
func (s *Store) OverallStats(ctx context.Context) (Stats, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT name, attrs_json, status_code,
		       end_time_unix_nano - start_time_unix_nano AS dur
		FROM spans`)
	if err != nil {
		return Stats{}, fmt.Errorf("store: overall stats: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return foldStats(rows, "")
}

// StatsByProvider returns one Stats row per distinct
// galdor.provider.name attribute. Spans without a provider
// attribute (e.g. graph or tool spans) are excluded so the per-
// provider rollup doesn't dilute the latency percentiles.
func (s *Store) StatsByProvider(ctx context.Context) ([]Stats, error) {
	return s.groupedStats(ctx, "galdor.provider.name", "galdor.provider.generate", "galdor.provider.stream")
}

// StatsByModel returns one Stats row per distinct
// gen_ai.request.model attribute. Same exclusion semantics as
// StatsByProvider.
func (s *Store) StatsByModel(ctx context.Context) ([]Stats, error) {
	return s.groupedStats(ctx, "gen_ai.request.model", "galdor.provider.generate", "galdor.provider.stream")
}

// groupedStats walks every span whose name is in spanNames, extracts
// the value of attrKey from its attributes JSON, and folds the spans
// into per-key Stats. The fold is done in Go rather than SQL because
// computing latency percentiles in SQLite without window functions
// would require either NTILE gymnastics or a recursive CTE for each
// group; the row count we're working with here is tiny.
func (s *Store) groupedStats(ctx context.Context, attrKey string, spanNames ...string) ([]Stats, error) {
	if len(spanNames) == 0 {
		return nil, nil
	}
	// Build the IN clause dynamically.
	placeholders := ""
	args := make([]any, 0, len(spanNames))
	for i, n := range spanNames {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args = append(args, n)
	}
	q := fmt.Sprintf(`
		SELECT name, attrs_json, status_code,
		       end_time_unix_nano - start_time_unix_nano AS dur
		FROM spans
		WHERE name IN (%s)`, placeholders)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: grouped stats: %w", err)
	}
	defer func() { _ = rows.Close() }()

	groups := map[string][]rowSample{}
	for rows.Next() {
		var r rowSample
		if err := rows.Scan(&r.name, &r.attrs, &r.status, &r.duration); err != nil {
			return nil, err
		}
		key, ok := extractAttr(r.attrs, attrKey)
		if !ok || key == "" {
			continue
		}
		groups[key] = append(groups[key], r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]Stats, 0, len(groups))
	for key, samples := range groups {
		out = append(out, statsFromSamples(key, samples))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// rowSample carries the per-span fields the rollup needs.
type rowSample struct {
	name     string
	attrs    string
	status   string
	duration int64
}

// foldStats consumes a row set and reduces it into a single Stats
// keyed by the provided key. Errors during row scan propagate.
func foldStats(rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}, key string) (Stats, error) {
	var samples []rowSample
	for rows.Next() {
		var r rowSample
		if err := rows.Scan(&r.name, &r.attrs, &r.status, &r.duration); err != nil {
			return Stats{}, err
		}
		samples = append(samples, r)
	}
	if err := rows.Err(); err != nil {
		return Stats{}, err
	}
	return statsFromSamples(key, samples), nil
}

// statsFromSamples is the pure reducer used by both the overall and
// the per-group paths.
func statsFromSamples(key string, samples []rowSample) Stats {
	out := Stats{Key: key}
	if len(samples) == 0 {
		return out
	}
	durations := make([]int64, 0, len(samples))
	for _, r := range samples {
		out.SpanCount++
		if r.status == "error" {
			out.ErrorCount++
		}
		if r.duration > 0 {
			durations = append(durations, r.duration)
		}
		if in, ok := extractInt(r.attrs, "gen_ai.usage.input_tokens"); ok {
			out.InputTokens += in
		}
		if outTok, ok := extractInt(r.attrs, "gen_ai.usage.output_tokens"); ok {
			out.OutputTokens += outTok
		}
	}
	if len(durations) > 0 {
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		out.LatencyP50Ns = percentile(durations, 0.50)
		out.LatencyP95Ns = percentile(durations, 0.95)
		out.LatencyP99Ns = percentile(durations, 0.99)
		out.LatencyMaxNs = durations[len(durations)-1]
	}
	return out
}

// percentile returns the value at the given fraction of a sorted
// slice using the nearest-rank method (the simplest definition;
// matches how most ops dashboards present these numbers).
func percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}
	idx := int(float64(len(sorted)-1) * p)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// SpansSince returns every span whose start_time_unix_nano is
// strictly greater than after. Used by `galdor scry tail` to
// poll for new arrivals between ticks.
//
// Spans are returned in ascending start order so callers can pass
// the last item's start time as the next `after` value.
func (s *Store) SpansSince(ctx context.Context, after int64, limit int) ([]Span, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT span_id, trace_id, parent_span_id, name,
		       start_time_unix_nano, end_time_unix_nano,
		       status_code, status_message, attrs_json, events_json, run_id
		FROM spans
		WHERE start_time_unix_nano > ?
		ORDER BY start_time_unix_nano ASC
		LIMIT ?`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("store: spans since: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Span
	for rows.Next() {
		sp, err := scanSpan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// MaxSpanStart returns the largest start_time_unix_nano in the
// store, or 0 if the table is empty. Callers use it to seed the
// cursor for SpansSince.
func (s *Store) MaxSpanStart(ctx context.Context) (int64, error) {
	var v *int64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(start_time_unix_nano) FROM spans`).Scan(&v)
	if err != nil {
		return 0, err
	}
	if v == nil {
		return 0, nil
	}
	return *v, nil
}

// extractAttr pulls a string-valued attribute out of the raw JSON
// blob without unmarshaling the whole map. Returns ok=false if the
// key is missing or the value isn't a string.
func extractAttr(attrsJSON, key string) (string, bool) {
	v, ok := extractRaw(attrsJSON, key)
	if !ok {
		return "", false
	}
	// Trim quotes and unescape simple backslash sequences. SQLite
	// stored the JSON verbatim from json.Marshal, so quoted strings
	// are well-formed.
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		return unescapeJSONString(v[1 : len(v)-1]), true
	}
	return "", false
}

// extractInt pulls an integer-valued attribute. Falls back to
// parsing a JSON number; returns ok=false on absence or non-numeric.
func extractInt(attrsJSON, key string) (int, bool) {
	v, ok := extractRaw(attrsJSON, key)
	if !ok {
		return 0, false
	}
	// Numbers may appear as "10" or "10.0" depending on how the
	// encoder serialized them. parse the integer portion.
	n, ok := parseIntPrefix(v)
	if !ok {
		return 0, false
	}
	return n, true
}

// extractRaw scans attrsJSON for `"key":<value>` and returns
// `<value>` as a substring. Stops at the first matching key.
// Naive but allocation-free for the small attribute blobs galdor
// emits; a full json.Unmarshal would be wasteful given we only
// need one or two fields per span.
func extractRaw(attrsJSON, key string) (string, bool) {
	needle := `"` + key + `":`
	i := indexOf(attrsJSON, needle)
	if i < 0 {
		return "", false
	}
	rest := attrsJSON[i+len(needle):]
	// Skip leading whitespace.
	for len(rest) > 0 && (rest[0] == ' ' || rest[0] == '\t') {
		rest = rest[1:]
	}
	if len(rest) == 0 {
		return "", false
	}
	if rest[0] == '"' {
		// String value: find the matching close-quote, honoring
		// escapes.
		for j := 1; j < len(rest); j++ {
			if rest[j] == '\\' {
				j++
				continue
			}
			if rest[j] == '"' {
				return rest[:j+1], true
			}
		}
		return "", false
	}
	// Non-string value: read until the next , or }.
	for j := 0; j < len(rest); j++ {
		if rest[j] == ',' || rest[j] == '}' {
			return rest[:j], true
		}
	}
	return rest, true
}

// indexOf is strings.Index inlined to keep this file allocation
// minimal — not worth a separate import for one call site.
func indexOf(haystack, needle string) int {
	n := len(needle)
	if n == 0 {
		return 0
	}
	if n > len(haystack) {
		return -1
	}
	for i := 0; i+n <= len(haystack); i++ {
		if haystack[i:i+n] == needle {
			return i
		}
	}
	return -1
}

// unescapeJSONString handles the subset of JSON escapes that
// realistic attribute strings produce: \\, \", \n, \t, \r.
func unescapeJSONString(s string) string {
	if indexOf(s, `\`) < 0 {
		return s
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			out = append(out, s[i])
			continue
		}
		i++
		switch s[i] {
		case '"', '\\', '/':
			out = append(out, s[i])
		case 'n':
			out = append(out, '\n')
		case 't':
			out = append(out, '\t')
		case 'r':
			out = append(out, '\r')
		default:
			out = append(out, '\\', s[i])
		}
	}
	return string(out)
}

// parseIntPrefix reads a leading integer (handles negatives and a
// trailing decimal portion which it ignores).
func parseIntPrefix(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	sign := 1
	i := 0
	if s[0] == '-' {
		sign = -1
		i = 1
	}
	n := 0
	started := false
	for ; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
		started = true
	}
	if !started {
		return 0, false
	}
	return sign * n, true
}
