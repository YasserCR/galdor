package ui

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/YasserCR/galdor/internal/store"
)

// handleRoot serves the run list at "/". Anything else not matched
// by a more specific route falls into 404 here so the embedded
// dashboard doesn't accidentally proxy unrelated paths.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 50)
	runs, err := s.store.ListRuns(r.Context(), limit)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "list runs", err)
		return
	}
	data := runsPageData{
		DBPath: s.dbPath,
		Limit:  limit,
		Runs:   buildRunRows(runs),
	}
	s.renderTemplate(w, "runs.html", data)
}

// handleRun serves the per-run span tree at "/runs/{id}".
func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	runID := strings.TrimPrefix(r.URL.Path, "/runs/")
	if runID == "" || strings.Contains(runID, "/") {
		http.NotFound(w, r)
		return
	}
	spans, err := s.store.SpansForRun(r.Context(), runID)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "load spans", err)
		return
	}
	if len(spans) == 0 {
		s.renderError(w, http.StatusNotFound, "run not found",
			fmt.Errorf("no spans recorded for run %q", runID))
		return
	}
	roots, total, errors := buildSpanTree(spans)
	data := runPageData{
		DBPath: s.dbPath,
		RunID:  runID,
		Total:  total,
		Errors: errors,
		Roots:  roots,
	}
	s.renderTemplate(w, "run.html", data)
}

// handleAPIRuns returns the run list as JSON. Same data the runs
// page uses; useful for shell pipelines and future polling.
func (s *Server) handleAPIRuns(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/runs" {
		http.NotFound(w, r)
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 50)
	runs, err := s.store.ListRuns(r.Context(), limit)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

// handleAPIRunSpans returns the spans for a single run at
// "/api/runs/{id}/spans".
func (s *Server) handleAPIRunSpans(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/runs/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[1] != "spans" {
		http.NotFound(w, r)
		return
	}
	runID := parts[0]
	if runID == "" {
		http.NotFound(w, r)
		return
	}
	spans, err := s.store.SpansForRun(r.Context(), runID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}
	if len(spans) == 0 {
		writeJSONError(w, http.StatusNotFound, fmt.Errorf("run %q not found", runID))
		return
	}
	writeJSON(w, http.StatusOK, spans)
}

// renderTemplate executes name and writes it to w. Errors are
// surfaced as 500s with the error in the body — there is no
// recovery from a template bug, and hiding it would make debugging
// harder.
func (s *Server) renderTemplate(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, name, data); err != nil {
		// Headers may have been flushed by this point; best effort.
		_, _ = fmt.Fprintf(w, "\n<!-- template %s: %v -->\n", name, err)
	}
}

// renderError sends a small HTML error page so the dashboard stays
// readable when something goes wrong. Errors visible to the user
// only — sensitive details should be logged separately if we ever
// add a logger.
func (s *Server) renderError(w http.ResponseWriter, code int, msg string, err error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	data := errorPageData{
		DBPath: s.dbPath,
		Code:   code,
		Title:  http.StatusText(code),
		Msg:    msg,
		Detail: err.Error(),
	}
	_ = s.templates.ExecuteTemplate(w, "error.html", data)
}

// runsPageData / runPageData / errorPageData are the template
// contracts. Keeping them in one file makes it easy to see what the
// HTML can read.
type runsPageData struct {
	DBPath string
	Limit  int
	Runs   []runRow
}

type runRow struct {
	RunID    string
	Status   string
	StatusOK bool
	Duration string
	Spans    int
	Errors   int
	Started  string
}

type runPageData struct {
	DBPath string
	RunID  string
	Total  int
	Errors int
	Roots  []*spanNode
}

// spanNode mirrors store.Span enriched with derived display strings
// and pointer children so html/template can recurse.
type spanNode struct {
	SpanID   string
	Name     string
	Status   string
	StatusOK bool
	Duration string
	Extras   []spanExtra
	Children []*spanNode
}

type spanExtra struct {
	Key   string
	Value string
}

type errorPageData struct {
	DBPath string
	Code   int
	Title  string
	Msg    string
	Detail string
}

// buildRunRows transforms store.RunSummary into the row struct the
// template renders. Display formatting belongs here, not in the
// template.
func buildRunRows(runs []store.RunSummary) []runRow {
	out := make([]runRow, 0, len(runs))
	for _, r := range runs {
		status := r.Status()
		out = append(out, runRow{
			RunID:    r.RunID,
			Status:   status,
			StatusOK: status == "ok",
			Duration: formatDuration(r.Duration()),
			Spans:    r.SpanCount,
			Errors:   r.ErrorCount,
			Started:  formatTimestamp(r.StartTimeUnixNano),
		})
	}
	return out
}

// buildSpanTree assembles the parent/child structure of spans. The
// returned roots are sorted by start time; descendants too.
// total/errors are aggregated so the run page banner doesn't need
// to walk the tree again.
func buildSpanTree(spans []store.Span) (roots []*spanNode, total, errors int) {
	byID := make(map[string]*spanNode, len(spans))
	srcByID := make(map[string]store.Span, len(spans))
	for _, sp := range spans {
		srcByID[sp.SpanID] = sp
		byID[sp.SpanID] = &spanNode{
			SpanID:   sp.SpanID,
			Name:     sp.Name,
			Status:   displayStatus(sp.StatusCode),
			StatusOK: sp.StatusCode != "error",
			Duration: formatDuration(sp.Duration()),
			Extras:   extractExtras(sp),
		}
	}
	for _, sp := range spans {
		total++
		if sp.StatusCode == "error" {
			errors++
		}
		node := byID[sp.SpanID]
		parent, ok := byID[sp.ParentSpanID]
		if sp.ParentSpanID != "" && ok {
			parent.Children = append(parent.Children, node)
		} else {
			roots = append(roots, node)
		}
	}
	// Sort siblings + roots by source start time.
	sortByStart := func(nodes []*spanNode) {
		// Tiny n; insertion sort keeps this allocation-free without
		// pulling in sort.Slice closures for every level.
		for i := 1; i < len(nodes); i++ {
			for j := i; j > 0; j-- {
				if srcByID[nodes[j].SpanID].StartTimeUnixNano <
					srcByID[nodes[j-1].SpanID].StartTimeUnixNano {
					nodes[j], nodes[j-1] = nodes[j-1], nodes[j]
				} else {
					break
				}
			}
		}
	}
	sortByStart(roots)
	for _, node := range byID {
		sortByStart(node.Children)
	}
	return roots, total, errors
}

// extractExtras pulls the handful of attributes the dashboard
// surfaces inline next to the span name. Mirrors the CLI's
// formatExtras so users see the same data in both surfaces.
func extractExtras(sp store.Span) []spanExtra {
	var extras []spanExtra
	add := func(k, v string) {
		if v != "" {
			extras = append(extras, spanExtra{Key: k, Value: v})
		}
	}
	if v, ok := sp.Attributes["galdor.node.name"].(string); ok {
		add("node", v)
	}
	if v, ok := sp.Attributes["galdor.provider.name"].(string); ok {
		add("provider", v)
	}
	if v, ok := sp.Attributes["gen_ai.request.model"].(string); ok {
		add("model", v)
	}
	if v, ok := sp.Attributes["gen_ai.tool.name"].(string); ok {
		add("tool", v)
	}
	if v, ok := sp.Attributes["gen_ai.usage.input_tokens"].(float64); ok && v > 0 {
		add("in", strconv.Itoa(int(v)))
	}
	if v, ok := sp.Attributes["gen_ai.usage.output_tokens"].(float64); ok && v > 0 {
		add("out", strconv.Itoa(int(v)))
	}
	return extras
}

func displayStatus(s string) string {
	if s == "" || s == "unset" {
		return "·"
	}
	return s
}

// formatDuration renders a nanosecond count as a short human string.
// Mirrors cmd/galdor/scry.go formatDuration; kept duplicated so the
// internal/ui package doesn't import cmd/.
func formatDuration(nanos int64) string {
	if nanos <= 0 {
		return "—"
	}
	d := time.Duration(nanos)
	switch {
	case d < time.Microsecond:
		return fmt.Sprintf("%dns", nanos)
	case d < time.Millisecond:
		return fmt.Sprintf("%.1fµs", float64(d)/float64(time.Microsecond))
	case d < time.Second:
		return fmt.Sprintf("%.1fms", float64(d)/float64(time.Millisecond))
	default:
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
}

func formatTimestamp(nanos int64) string {
	if nanos == 0 {
		return "—"
	}
	return time.Unix(0, nanos).Format(time.RFC3339)
}

func parseLimit(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	if n > 500 {
		return 500
	}
	return n
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeJSONError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

// templateFuncs exposes a small set of helpers to templates. The
// recursive span rendering uses the `tree` block in run.html, which
// only needs basic html/template features — but keeping a Funcs map
// lets us add helpers without rewriting parseTemplates.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"shortID": func(s string) string {
			if len(s) <= 12 {
				return s
			}
			return s[:8] + "…" + s[len(s)-4:]
		},
	}
}
