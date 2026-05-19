package ui

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleGraphPage_RendersHTML(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/graph", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("Content-Type = %q", got)
	}
	body := rec.Body.String()
	for _, want := range []string{"Graph viewer", "graph.Spec JSON", "Render SVG"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestHandleGraphSVG_RendersFromSpec(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	body := []byte(`{
		"entry": "model",
		"nodes": [{"name": "model"}, {"name": "tools", "interrupt": true}],
		"static_edges": [
			{"from": "__start__", "to": "model"},
			{"from": "tools", "to": "model"}
		],
		"conditional_edges": [{"from": "model"}]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graph/svg", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "image/svg+xml") {
		t.Errorf("Content-Type = %q", got)
	}
	out := rec.Body.String()
	if !strings.HasPrefix(out, "<svg") {
		t.Errorf("response does not start with <svg: %s", out[:min(40, len(out))])
	}
	for _, want := range []string{"model", "tools", "START", "END", "router"} {
		if !strings.Contains(out, want) {
			t.Errorf("SVG missing %q", want)
		}
	}
}

func TestHandleGraphSVG_EmptyBodyReturns400SVG(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/graph/svg", strings.NewReader(""))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "image/svg+xml") {
		t.Errorf("error response should also be SVG; got %q", got)
	}
	if !strings.Contains(rec.Body.String(), "empty spec") {
		t.Errorf("error SVG missing message: %s", rec.Body.String())
	}
}

func TestHandleGraphSVG_MalformedJSONReturns400(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/graph/svg", strings.NewReader("{not json"))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "decode spec") {
		t.Errorf("error SVG missing decode hint: %s", rec.Body.String())
	}
}

// Ensure the io.Reader interface is still available (we read the body in handler).
var _ io.Reader = (*bytes.Reader)(nil)
