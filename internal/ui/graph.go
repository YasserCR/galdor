package ui

import (
	"bytes"
	"encoding/json"
	"html/template"
	"io"
	"net/http"

	"github.com/YasserCR/galdor/pkg/graph"
)

const (
	// maxGraphSVGBody caps the request body for /api/graph/svg. A
	// spec large enough to exceed this is almost certainly an attempt
	// to amplify a small POST into a multi-MB SVG buffered in memory.
	maxGraphSVGBody = 1 << 20 // 1 MiB
	// maxGraphNodes caps the node count we'll render. The SVG grows
	// with the node count, so this bounds peak render memory even for
	// a body that fits under maxGraphSVGBody.
	maxGraphNodes = 2000
)

// handleGraphPage serves the DAG viewer at /graph. By default it
// auto-loads the execution graph recorded for the most recent run
// (specs are persisted per run by observability.RecordGraphSpec); a
// run dropdown switches between them, and an "advanced" panel still
// lets you paste an ad-hoc graph.Spec. ?run=<id> selects a run
// directly.
func (s *Server) handleGraphPage(w http.ResponseWriter, r *http.Request) {
	runs, _ := s.store.ListRuns(r.Context(), 50)

	selected := r.URL.Query().Get("run")
	if selected == "" && len(runs) > 0 {
		selected = runs[0].RunID // default to the latest run
	}

	data := graphPageData{DBPath: s.dbPath, SelectedRun: selected}
	for _, ru := range runs {
		data.Runs = append(data.Runs, graphRunOption{RunID: ru.RunID, Selected: ru.RunID == selected})
	}

	if selected != "" {
		if specJSON, err := s.store.GetGraphSpec(r.Context(), selected); err == nil && specJSON != "" {
			data.SpecJSON = specJSON
			// Reuse the run-detail renderer so the viewer's graph is
			// clickable too: each node links to its step, hover shows
			// duration + status.
			spans, _ := s.store.SpansForRun(r.Context(), selected)
			data.GraphSVG = s.renderRunGraphSVG(r.Context(), selected, spans)
		}
		data.NoSpec = data.GraphSVG == ""
	}

	s.renderTemplate(w, "graph.html", data)
}

// handleGraphSVG accepts a JSON-encoded graph.Spec via POST and
// returns the rendered SVG. Content type is image/svg+xml so the
// browser inlines the result as an image when called directly.
//
// On parse error a 400 is returned with a tiny SVG that displays the
// error message — easier to debug than a JSON envelope when the
// caller is rendering the response into an <img> or <object>.
func (s *Server) handleGraphSVG(w http.ResponseWriter, r *http.Request) {
	defer func() { _ = r.Body.Close() }()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxGraphSVGBody))
	if err != nil {
		errorSVG(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if len(body) == 0 {
		errorSVG(w, http.StatusBadRequest, "empty spec")
		return
	}
	var spec graph.Spec
	if err := json.Unmarshal(body, &spec); err != nil {
		errorSVG(w, http.StatusBadRequest, "decode spec: "+err.Error())
		return
	}
	if len(spec.Nodes) > maxGraphNodes {
		errorSVG(w, http.StatusBadRequest, "spec too large: node count exceeds limit")
		return
	}
	var buf bytes.Buffer
	if err := spec.RenderSVG(&buf); err != nil {
		errorSVG(w, http.StatusInternalServerError, "render: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(buf.Bytes())
}

// errorSVG writes a tiny SVG carrying the given error message at the
// requested status code. Useful when callers fetch the SVG directly
// into an <img> tag and a plain-text 400 would render as a broken image.
func errorSVG(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="600" height="60" viewBox="0 0 600 60"><rect width="100%" height="100%" fill="#fee2e2"/><text x="12" y="32" font-family="ui-sans-serif, system-ui" font-size="13" fill="#991b1b">` + template.HTMLEscapeString(msg) + `</text></svg>`))
}

type graphPageData struct {
	DBPath      string
	Runs        []graphRunOption // recent runs for the dropdown
	SelectedRun string           // run whose graph is shown ("" if none)
	GraphSVG    template.HTML    // pre-rendered SVG, "" when absent
	SpecJSON    string           // raw spec, pre-filled into the advanced textarea
	NoSpec      bool             // a run is selected but has no recorded spec
}

type graphRunOption struct {
	RunID    string
	Selected bool
}
