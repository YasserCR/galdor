package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/YasserCR/galdor/internal/store"
	"github.com/YasserCR/galdor/pkg/graph"
	"github.com/YasserCR/galdor/pkg/observability"
	"github.com/YasserCR/galdor/pkg/schema"
)

// handleRoot serves the run list at "/".
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r.URL.Query().Get("limit"), 50)
	runs, err := s.store.ListRuns(r.Context(), limit)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "list runs", err)
		return
	}
	// OrphanSpanCount catches the silent-failure case: spans landed
	// in the DB but no trace carries galdor.run.id, so ListRuns
	// returns empty. We surface it as a banner instead of pretending
	// the store is empty.
	orphans, _ := s.store.OrphanSpanCount(r.Context())
	data := runsPageData{
		DBPath:      s.dbPath,
		Limit:       limit,
		Runs:        buildRunRows(runs),
		OrphanSpans: orphans,
	}
	s.renderTemplate(w, "runs.html", data)
}

// handleRun serves the per-run span tree at "/runs/{runID}".
func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
	if runID == "" {
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
	roots, total, errs := buildSpanTree(spans, runID)
	graphSVG := s.renderRunGraphSVG(r.Context(), runID, spans)
	data := runPageData{
		DBPath:   s.dbPath,
		RunID:    runID,
		Total:    total,
		Errors:   errs,
		Roots:    roots,
		Timeline: buildTimeline(spans),
		GraphSVG: graphSVG,
	}
	s.renderTemplate(w, "run.html", data)
}

// renderRunGraphSVG returns an inline SVG of the graph topology
// recorded for runID, or "" when no spec was recorded for the run.
// Errors are swallowed: a broken or absent spec is shown as the
// absence of the graph panel, not as a 500 on the run page.
func (s *Server) renderRunGraphSVG(ctx context.Context, runID string, spans []store.Span) template.HTML {
	specJSON, err := s.store.GetGraphSpec(ctx, runID)
	if err != nil || specJSON == "" {
		return ""
	}
	var spec graph.Spec
	if err := json.Unmarshal([]byte(specJSON), &spec); err != nil {
		return ""
	}
	var buf bytes.Buffer
	if err := spec.RenderSVGAnnotated(&buf, buildNodeAnnotations(runID, spans)); err != nil {
		return ""
	}
	// #nosec G203 -- SVG body is produced in-process by pkg/graph.Spec; no user input.
	return template.HTML(buf.String())
}

// buildNodeAnnotations maps each graph node to the run step that executed
// it, turning the static topology into a clickable map: hover shows the
// node's duration + status, click jumps to that node's step in the
// time-travel view. The first execution wins when a node runs more than
// once (a loop).
func buildNodeAnnotations(runID string, spans []store.Span) map[string]graph.NodeAnnotation {
	var nodes []store.Span
	for _, sp := range spans {
		if sp.Name == "galdor.graph.node" {
			nodes = append(nodes, sp)
		}
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		return nodes[i].StartTimeUnixNano < nodes[j].StartTimeUnixNano
	})
	ann := make(map[string]graph.NodeAnnotation, len(nodes))
	for i, n := range nodes {
		name := stringAttr(n.Attributes, "galdor.node.name")
		if name == "" {
			name = n.Name
		}
		if _, seen := ann[name]; seen {
			continue // first occurrence wins
		}
		status := n.StatusCode
		if status == "" || status == "unset" {
			status = "ok"
		}
		ann[name] = graph.NodeAnnotation{
			Href:    fmt.Sprintf("/runs/%s/steps#step-%d", runID, i+1),
			Tooltip: fmt.Sprintf("%s · %s · %s", name, formatDuration(n.Duration()), status),
		}
	}
	return ann
}

// handleSpan serves a single span's detail at
// "/runs/{runID}/spans/{spanID}". The page surfaces the full
// attribute table, status, events and — when content capture was
// enabled — the prompt and completion messages.
func (s *Server) handleSpan(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
	spanID := r.PathValue("spanID")
	if runID == "" || spanID == "" {
		http.NotFound(w, r)
		return
	}
	spans, err := s.store.SpansForRun(r.Context(), runID)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "load spans", err)
		return
	}
	var found *store.Span
	for i := range spans {
		if spans[i].SpanID == spanID {
			found = &spans[i]
			break
		}
	}
	if found == nil {
		s.renderError(w, http.StatusNotFound, "span not found",
			fmt.Errorf("span %q not in run %q", spanID, runID))
		return
	}
	s.renderTemplate(w, "span.html", buildSpanPage(*found, runID, s.dbPath))
}

// handleAPIRuns returns the run list as JSON. Same data the runs
// page uses; useful for shell pipelines and future polling.
func (s *Server) handleAPIRuns(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r.URL.Query().Get("limit"), 50)
	runs, err := s.store.ListRuns(r.Context(), limit)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

// handleAPIRunSpans returns the spans for a single run at
// "/api/runs/{runID}/spans".
func (s *Server) handleAPIRunSpans(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
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

// handleAPISpan returns a single span at
// "/api/runs/{runID}/spans/{spanID}".
func (s *Server) handleAPISpan(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
	spanID := r.PathValue("spanID")
	if runID == "" || spanID == "" {
		http.NotFound(w, r)
		return
	}
	spans, err := s.store.SpansForRun(r.Context(), runID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}
	for _, sp := range spans {
		if sp.SpanID == spanID {
			writeJSON(w, http.StatusOK, sp)
			return
		}
	}
	writeJSONError(w, http.StatusNotFound, fmt.Errorf("span %q not in run %q", spanID, runID))
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
	DBPath      string
	Limit       int
	Runs        []runRow
	OrphanSpans int
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
	DBPath   string
	RunID    string
	Total    int
	Errors   int
	Roots    []*spanNode
	Timeline timelineView
	GraphSVG template.HTML
}

// timelineView holds the SVG-ready geometry for the run's spans.
// Coordinates are absolute pixels so the template stays declarative
// (no math in `text/template`). Width is fixed; height grows with
// span count so dense traces don't squish.
type timelineView struct {
	Width      int
	Height     int
	RowHeight  int
	LabelWidth int
	HeaderH    int            // top band reserved for the time axis
	AxisLabelY int            // baseline y for the tick labels
	Ticks      []timelineTick // evenly spaced time-axis ticks
	Bars       []timelineBar
}

// timelineTick is one mark on the top time axis: an x position and the
// elapsed-time label drawn above it (e.g. "0", "1.5s").
type timelineTick struct {
	X     int
	Label string
}

type timelineBar struct {
	Y       int
	X       int
	W       int
	LabelX  int    // x of the (depth-indented) span name
	LabelY  int    // baseline y shared by the name + duration
	DurX    int    // x of the right-aligned duration label
	Dur     string // formatted span duration
	OK      bool
	SpanID  string
	Name    string
	Tooltip string
}

// spanNode mirrors store.Span enriched with derived display strings
// and pointer children so html/template can recurse. RunID is
// duplicated on every node because html/template's recursive
// templates lose access to outer-scope `$`; it makes the row links
// trivial to render.
type spanNode struct {
	RunID    string
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
// to walk the tree again. runID is stamped on each node so the
// recursive row template can render absolute links without
// reaching for outer-scope $.
func buildSpanTree(spans []store.Span, runID string) (roots []*spanNode, total, errors int) {
	byID := make(map[string]*spanNode, len(spans))
	srcByID := make(map[string]store.Span, len(spans))
	for _, sp := range spans {
		srcByID[sp.SpanID] = sp
		byID[sp.SpanID] = &spanNode{
			RunID:    runID,
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

// buildTimeline projects spans onto a fixed-width Gantt chart. The
// time axis spans from the earliest span start to the latest span
// end; each span gets one row positioned in DFS order so parents
// sit above their children, matching the tree view. Bars shorter
// than 2px are widened so they remain clickable.
func buildTimeline(spans []store.Span) timelineView {
	const (
		width      = 1000
		labelWidth = 300
		rowHeight  = 22
		barH       = 11
		headerH    = 30
		minBarPx   = 2
		numTicks   = 6
	)
	tv := timelineView{
		Width:      width,
		LabelWidth: labelWidth,
		RowHeight:  rowHeight,
		HeaderH:    headerH,
		AxisLabelY: headerH - 11,
	}
	if len(spans) == 0 {
		return tv
	}

	// Compute the time window across all spans, not just roots —
	// some adapters may emit spans whose end time exceeds the root.
	minStart, maxEnd := spans[0].StartTimeUnixNano, spans[0].EndTimeUnixNano
	for _, sp := range spans[1:] {
		if sp.StartTimeUnixNano < minStart {
			minStart = sp.StartTimeUnixNano
		}
		if sp.EndTimeUnixNano > maxEnd {
			maxEnd = sp.EndTimeUnixNano
		}
	}
	total := maxEnd - minStart
	if total <= 0 {
		total = 1
	}

	// Reuse the tree structure so visual order matches the indented
	// tree the user sees below.
	roots, _, _ := buildSpanTree(spans, "")
	srcByID := make(map[string]store.Span, len(spans))
	for _, sp := range spans {
		srcByID[sp.SpanID] = sp
	}

	chartWidth := width - labelWidth

	// Top time axis: evenly spaced ticks from 0 to the total duration so
	// the chart reads as a real scale, not just bars on a strip.
	for k := 0; k <= numTicks; k++ {
		tv.Ticks = append(tv.Ticks, timelineTick{
			X:     labelWidth + chartWidth*k/numTicks,
			Label: tickLabel(total * int64(k) / int64(numTicks)),
		})
	}

	row := 0
	var walk func(node *spanNode, depth int)
	walk = func(node *spanNode, depth int) {
		sp := srcByID[node.SpanID]
		x := labelWidth + int(float64(chartWidth)*float64(sp.StartTimeUnixNano-minStart)/float64(total))
		w := max(int(float64(chartWidth)*float64(sp.EndTimeUnixNano-sp.StartTimeUnixNano)/float64(total)), minBarPx)
		if x+w > width {
			w = width - x
		}
		rowTop := headerH + row*rowHeight
		labelX := 10 + depth*14 // indent by tree depth: run › node › provider
		if labelX > labelWidth-48 {
			labelX = labelWidth - 48
		}
		tv.Bars = append(tv.Bars, timelineBar{
			Y:       rowTop + (rowHeight-barH)/2,
			X:       x,
			W:       w,
			LabelX:  labelX,
			LabelY:  rowTop + 15,
			DurX:    labelWidth - 8,
			Dur:     formatDuration(sp.Duration()),
			OK:      sp.StatusCode != "error",
			SpanID:  sp.SpanID,
			Name:    strings.TrimPrefix(sp.Name, "galdor."),
			Tooltip: fmt.Sprintf("%s · %s", sp.Name, formatDuration(sp.Duration())),
		})
		row++
		for _, child := range node.Children {
			walk(child, depth+1)
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	tv.Height = headerH + row*rowHeight + 12
	return tv
}

// tickLabel formats an elapsed-time axis label; the origin reads "0"
// rather than the em-dash formatDuration uses for zero durations.
func tickLabel(ns int64) string {
	if ns <= 0 {
		return "0"
	}
	return formatDuration(ns)
}

// spanPageData backs the span detail page. Prompt and Completion
// are nil unless the producer opted into content capture via
// observability.WithCaptureContent — the template branches on that
// to suppress an empty "messages" panel.
type spanPageData struct {
	DBPath        string
	RunID         string
	SpanID        string
	Name          string
	ParentSpanID  string
	TraceID       string
	Status        string
	StatusOK      bool
	StatusMessage string
	Duration      string
	StartedAt     string
	EndedAt       string
	Attributes    []spanAttribute
	Events        []store.Event
	Prompt        []renderedMessage
	Completion    *renderedMessage
}

type spanAttribute struct {
	Key   string
	Value string
}

// renderedMessage is the display-friendly view of a schema.Message:
// the role plus a sequence of text blocks and tool call summaries.
// We deliberately flatten ContentParts into strings so the template
// doesn't need to know about the schema types.
type renderedMessage struct {
	Role      string
	TextParts []string
	ToolCalls []renderedToolCall
}

type renderedToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// buildSpanPage projects a store.Span into the template-ready
// spanPageData. The transformation lives in Go so the template
// stays declarative (no field-shape branching).
func buildSpanPage(sp store.Span, runID, dbPath string) spanPageData {
	out := spanPageData{
		DBPath:        dbPath,
		RunID:         runID,
		SpanID:        sp.SpanID,
		Name:          sp.Name,
		ParentSpanID:  sp.ParentSpanID,
		TraceID:       sp.TraceID,
		Status:        displayStatus(sp.StatusCode),
		StatusOK:      sp.StatusCode != "error",
		StatusMessage: sp.StatusMessage,
		Duration:      formatDuration(sp.Duration()),
		StartedAt:     formatTimestamp(sp.StartTimeUnixNano),
		EndedAt:       formatTimestamp(sp.EndTimeUnixNano),
		Events:        sp.Events,
	}

	// Attributes are sorted alphabetically so the table is stable
	// across reloads — JSON map iteration order isn't.
	keys := make([]string, 0, len(sp.Attributes))
	for k := range sp.Attributes {
		// Skip the content-capture fields here so they don't dilute
		// the attribute table; they get their own dedicated panel.
		if k == observability.AttrGenAIPrompt || k == observability.AttrGenAICompletion {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out.Attributes = append(out.Attributes, spanAttribute{
			Key:   k,
			Value: formatAttrValue(sp.Attributes[k]),
		})
	}

	if raw, ok := sp.Attributes[observability.AttrGenAIPrompt].(string); ok && raw != "" {
		out.Prompt = decodeMessages(raw)
	}
	if raw, ok := sp.Attributes[observability.AttrGenAICompletion].(string); ok && raw != "" {
		msgs := decodeMessages("[" + raw + "]")
		if len(msgs) > 0 {
			out.Completion = &msgs[0]
		}
	}
	return out
}

// decodeMessages parses the JSON-encoded message list that
// InstrumentProvider stashes on the span. Best-effort: malformed
// JSON yields an empty slice rather than an error, because the
// detail page should still render even if the capture payload was
// truncated or corrupted.
func decodeMessages(raw string) []renderedMessage {
	var msgs []schema.Message
	if err := json.Unmarshal([]byte(raw), &msgs); err != nil {
		return nil
	}
	out := make([]renderedMessage, 0, len(msgs))
	for _, m := range msgs {
		rm := renderedMessage{Role: string(m.Role)}
		for _, p := range m.Content {
			if p.Type == schema.ContentTypeText && p.Text != "" {
				rm.TextParts = append(rm.TextParts, p.Text)
			}
		}
		for _, c := range m.ToolCalls {
			rm.ToolCalls = append(rm.ToolCalls, renderedToolCall{
				ID:        c.ID,
				Name:      c.Name,
				Arguments: string(c.Arguments),
			})
		}
		out = append(out, rm)
	}
	return out
}

// formatAttrValue renders an attribute value as a compact string.
// Numbers come back as float64 (json default); integers display
// without a trailing ".0".
func formatAttrValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case nil:
		return ""
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
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
