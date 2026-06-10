package ui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YasserCR/galdor/internal/store"
)

// openTestStore returns a temp Store seeded with one happy run and
// one error run. Cleanup is registered automatically.
func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(context.Background(), filepath.Join(dir, "traces.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if seedErr := s.InsertSpans(context.Background(), []store.Span{
		{
			SpanID: "r1", TraceID: "t1", Name: "galdor.graph.run",
			StartTimeUnixNano: 1000, EndTimeUnixNano: 2000,
			StatusCode: "ok", RunID: "run-happy",
			Attributes: map[string]any{"galdor.run.id": "run-happy"},
		},
		{
			SpanID: "a1", TraceID: "t1", ParentSpanID: "r1",
			Name:              "galdor.provider.generate",
			StartTimeUnixNano: 1100, EndTimeUnixNano: 1200,
			StatusCode: "ok",
			Attributes: map[string]any{
				"galdor.provider.name":       "anthropic",
				"gen_ai.request.model":       "claude-haiku-4-5",
				"gen_ai.usage.input_tokens":  10,
				"gen_ai.usage.output_tokens": 4,
			},
		},
		{
			SpanID: "r2", TraceID: "t2", Name: "galdor.graph.run",
			StartTimeUnixNano: 3000, EndTimeUnixNano: 4000,
			StatusCode: "error", StatusMessage: "rate limited", RunID: "run-bad",
			Attributes: map[string]any{"galdor.run.id": "run-bad"},
		},
	}); seedErr != nil {
		t.Fatal(seedErr)
	}
	return s
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	srv, err := NewServer(openTestStore(t), Options{DBPath: "/tmp/test.db"})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func TestNewServer_RejectsNilStore(t *testing.T) {
	t.Parallel()
	if _, err := NewServer(nil, Options{}); err == nil {
		t.Fatal("expected error for nil store")
	}
}

func TestRunsPage(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	req := loopbackReq(http.MethodGet, "/", nil)
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	wantSubstrs := []string{
		"galdor scry",
		"run-happy",
		"run-bad",
		"badge ok",
		"badge err",
		"/tmp/test.db",
	}
	for _, s := range wantSubstrs {
		if !strings.Contains(body, s) {
			t.Errorf("body missing %q", s)
		}
	}
}

func TestRunsPage_EmptyStore(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := store.Open(context.Background(), filepath.Join(dir, "traces.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	srv, err := NewServer(s, Options{})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, loopbackReq(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "No runs recorded") {
		t.Errorf("empty state missing: %s", rec.Body.String())
	}
}

// TestRunsPage_OrphanWarning verifies the dashboard surfaces a
// warning banner when the store contains spans but none of their
// traces carry galdor.run.id — the pragma retro failure mode.
func TestRunsPage_OrphanWarning(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := store.Open(context.Background(), filepath.Join(dir, "traces.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// Insert spans with no run_id set — the silent-failure case.
	if insertErr := s.InsertSpans(context.Background(), []store.Span{{
		SpanID:            "orphan-1",
		TraceID:           "orphan-trace",
		Name:              "raw.span",
		StartTimeUnixNano: 1,
		EndTimeUnixNano:   2,
		StatusCode:        "ok",
	}}); insertErr != nil {
		t.Fatal(insertErr)
	}

	srv, err := NewServer(s, Options{})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, loopbackReq(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "orphan span") {
		t.Errorf("orphan banner missing: %s", body)
	}
	if !strings.Contains(body, "galdor.run.id") {
		t.Errorf("banner should reference the attribute name: %s", body)
	}
}

func TestRunPage_RendersTree(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, loopbackReq(http.MethodGet, "/runs/run-happy", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	wantSubstrs := []string{
		"galdor.graph.run",
		"galdor.provider.generate",
		"provider=",
		"anthropic",
		"in=",
		"10",
	}
	for _, s := range wantSubstrs {
		if !strings.Contains(body, s) {
			t.Errorf("body missing %q", s)
		}
	}
}

// TestRunPage_RendersGraphSVG verifies that when a graph spec has
// been recorded for a run (via observability.RecordGraphSpec), the
// run-detail page inlines the rendered DAG SVG inside the
// "graph topology" panel.
func TestRunPage_RendersGraphSVG(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	specJSON := `{"entry":"inc","nodes":[{"name":"inc"}],"static_edges":[{"from":"__START__","to":"inc"},{"from":"inc","to":"__END__"}],"conditional_edges":[]}`
	if err := s.SetGraphSpec(context.Background(), "run-happy", []byte(specJSON)); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(s, Options{})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, loopbackReq(http.MethodGet, "/runs/run-happy", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "graph topology") {
		t.Errorf("graph-panel summary missing")
	}
	if !strings.Contains(body, "<svg") {
		t.Errorf("rendered SVG missing: %s", body)
	}
}

// TestRunPage_NoGraphSVGWhenSpecAbsent verifies the panel is hidden
// when no spec is recorded — backwards-compatible for runs created
// before RecordGraphSpec was wired in.
func TestRunPage_NoGraphSVGWhenSpecAbsent(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, loopbackReq(http.MethodGet, "/runs/run-happy", nil))
	if strings.Contains(rec.Body.String(), "graph topology") {
		t.Errorf("graph-panel should be hidden when spec absent")
	}
}

func TestRunPage_NotFound(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, loopbackReq(http.MethodGet, "/runs/does-not-exist", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "no spans recorded") {
		t.Errorf("error body: %s", rec.Body.String())
	}
}

func TestUnknownPath_NotFound(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	for _, path := range []string{"/runs/", "/runs/a/b", "/totally-unknown"} {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, loopbackReq(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d", path, rec.Code)
		}
	}
}

func TestAPIRuns(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, loopbackReq(http.MethodGet, "/api/runs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	var runs []store.RunSummary
	if err := json.NewDecoder(rec.Body).Decode(&runs); err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("len = %d, want 2", len(runs))
	}
}

func TestAPIRunSpans(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, loopbackReq(http.MethodGet, "/api/runs/run-happy/spans", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var spans []store.Span
	if err := json.NewDecoder(rec.Body).Decode(&spans); err != nil {
		t.Fatal(err)
	}
	if len(spans) != 2 {
		t.Fatalf("len = %d, want 2", len(spans))
	}
}

func TestAPIRunSpans_MissingRun(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, loopbackReq(http.MethodGet, "/api/runs/nope/spans", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestSpanDetailPage(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, loopbackReq(http.MethodGet, "/runs/run-happy/spans/a1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"galdor.provider.generate",
		"anthropic",            // attribute table value
		"claude-haiku-4-5",     // request model
		"gen_ai.request.model", // attribute key
		"run-happy",            // breadcrumb
	} {
		if !strings.Contains(body, want) {
			t.Errorf("span page missing %q", want)
		}
	}
}

func TestSpanDetailPage_RendersCapturedMessages(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := store.Open(context.Background(), filepath.Join(dir, "traces.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// Hand-craft a span carrying the gen_ai.prompt + gen_ai.completion
	// attributes that InstrumentProvider would have produced with
	// WithCaptureContent on.
	prompt := `[{"role":"user","content":[{"type":"text","text":"what is 2+3?"}]}]`
	completion := `{"role":"assistant","content":[{"type":"text","text":"the answer is 5"}]}`
	if insErr := s.InsertSpans(context.Background(), []store.Span{
		{
			SpanID: "rcap", TraceID: "tcap", Name: "galdor.graph.run",
			StartTimeUnixNano: 100, EndTimeUnixNano: 200,
			StatusCode: "ok", RunID: "run-cap",
			Attributes: map[string]any{"galdor.run.id": "run-cap"},
		},
		{
			SpanID: "acap", TraceID: "tcap", ParentSpanID: "rcap",
			Name:              "galdor.provider.generate",
			StartTimeUnixNano: 110, EndTimeUnixNano: 190,
			StatusCode: "ok",
			Attributes: map[string]any{
				"galdor.provider.name": "anthropic",
				"gen_ai.prompt":        prompt,
				"gen_ai.completion":    completion,
			},
		},
	}); insErr != nil {
		t.Fatal(insErr)
	}
	srv, err := NewServer(s, Options{})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, loopbackReq(http.MethodGet, "/runs/run-cap/spans/acap", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Messages",        // section heading
		"what is 2",       // user prompt text (escaping aside, the substring is enough)
		"3?",              // tail of the same prompt
		"the answer is 5", // assistant completion
		"msg-user",        // role-specific class
		"msg-assistant",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("captured-messages page missing %q", want)
		}
	}
	// Ensure the prompt/completion attribute keys are NOT also rendered
	// in the attribute table (buildSpanPage skips them).
	if strings.Contains(body, ">gen_ai.prompt<") {
		t.Error("prompt key should be excluded from attribute table")
	}
}

func TestSpanDetailPage_NotFound(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, loopbackReq(http.MethodGet, "/runs/run-happy/spans/does-not-exist", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestAPISpan(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, loopbackReq(http.MethodGet, "/api/runs/run-happy/spans/a1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var sp store.Span
	if err := json.NewDecoder(rec.Body).Decode(&sp); err != nil {
		t.Fatal(err)
	}
	if sp.SpanID != "a1" {
		t.Errorf("SpanID = %q", sp.SpanID)
	}
}

func TestRunPage_RowsAreLinks(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, loopbackReq(http.MethodGet, "/runs/run-happy", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `href="/runs/run-happy/spans/a1"`) {
		t.Errorf("expected span row to link to its detail page; body=%s", body[:300])
	}
}

func TestStaticCSS(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, loopbackReq(http.MethodGet, "/static/style.css", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "galdor scry") {
		t.Errorf("css body unexpected: %s", rec.Body.String()[:120])
	}
}

func TestHealthz(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, loopbackReq(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestParseLimit(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want int
	}{
		{"", 50},
		{"10", 10},
		{"0", 50},
		{"-5", 50},
		{"abc", 50},
		{"9999", 500},
	}
	for _, c := range cases {
		if got := parseLimit(c.in, 50); got != c.want {
			t.Errorf("parseLimit(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestListenAndServe_ShutsDownOnContextCancel(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addrCh := make(chan string, 1)
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- srv.ListenAndServe(ctx, "127.0.0.1:0", func(a string) { addrCh <- a })
	}()

	var addr string
	select {
	case addr = <-addrCh:
	case <-time.After(3 * time.Second):
		t.Fatal("server never reported listen addr")
	}

	// Make a real request to confirm the server is live.
	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz status = %d", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-doneCh:
		if err != nil {
			t.Errorf("serve returned %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not shut down after ctx cancel")
	}
}

// Regression for audit M23: the dashboard must reject requests whose Host
// header is a domain name (the DNS-rebinding vector — a malicious site
// resolves its domain to 127.0.0.1 and reaches the loopback dashboard via
// the victim's browser). Direct IP / localhost access is unaffected.
func TestServeHTTP_RejectsDNSRebindingHost(t *testing.T) {
	srv, err := NewServer(openTestStore(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	// A domain Host must be rejected.
	req := loopbackReq(http.MethodGet, "/api/runs", nil)
	req.Host = "evil.example"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("domain Host must be 403 (regression of M23), got %d", rec.Code)
	}
	// Loopback / IP / localhost hosts must still be served.
	for _, h := range []string{"127.0.0.1:7777", "localhost:7777", "[::1]:7777"} {
		r := loopbackReq(http.MethodGet, "/api/runs", nil)
		r.Host = h
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		if w.Code == http.StatusForbidden {
			t.Errorf("host %q must be allowed, got 403", h)
		}
	}
}

// loopbackReq builds a test request with a loopback Host so it passes the
// dashboard's DNS-rebinding guard (httptest.NewRequest defaults Host to
// "example.com", a domain, which the guard rejects by design).
func loopbackReq(method, target string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, target, body)
	r.Host = "127.0.0.1:7777"
	return r
}

// Regression (audit low): a template render failure must yield a clean 500,
// not a 200 with a half-written body. renderTemplate buffers, so an error
// (here: an unknown template name) is caught before any byte is committed.
func TestRenderTemplate_FailureIsClean500(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.renderTemplate(rec, "does-not-exist.html", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (a render error must not commit a 200)", rec.Code)
	}
}
