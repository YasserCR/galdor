package ui

import (
	"bytes"
	"encoding/json"
	"html/template"
	"io"
	"net/http"

	"github.com/YasserCR/galdor/pkg/graph"
)

// handleGraphPage serves the static DAG viewer at /graph. Users paste
// a JSON-encoded graph.Spec into the textarea and the page renders it
// as SVG via the /api/graph/svg endpoint.
//
// The page is intentionally untied to a specific run: graph topology
// is a per-Runnable property, not a per-run one. Persisting specs
// alongside runs (so the run-detail page can show "this is the graph
// that ran") is a separate, larger change tracked for a follow-up.
func (s *Server) handleGraphPage(w http.ResponseWriter, _ *http.Request) {
	s.renderTemplate(w, "graph.html", graphPageData{DBPath: s.dbPath})
}

// handleGraphSVG accepts a JSON-encoded graph.Spec via POST and
// returns the rendered SVG. Content type is image/svg+xml so the
// browser inlines the result as an image when called directly.
//
// On parse error a 400 is returned with a tiny SVG that displays the
// error message — easier to debug than a JSON envelope when the
// caller is rendering the response into an <img> or <object>.
func (s *Server) handleGraphSVG(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
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
	DBPath string
}
