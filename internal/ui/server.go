package ui

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"time"

	"github.com/YasserCR/galdor/internal/store"
)

//go:embed templates/*.html static/*
var assets embed.FS

// Server is the embedded HTTP surface over a span store. It owns
// the mux, the parsed templates and the static asset sub-FS — but
// does NOT own the *store.Store: the caller passes one in and
// retains responsibility for closing it.
type Server struct {
	store     *store.Store
	mux       *http.ServeMux
	templates *template.Template
	dbPath    string // displayed in the page footer; informational only
}

// Options configures a Server. Zero values are sensible defaults.
type Options struct {
	// DBPath is shown in the footer so users running multiple
	// dashboards can tell which trace store they're looking at.
	// Optional.
	DBPath string
}

// NewServer wires up the mux and parses templates. The returned
// Server implements http.Handler and is safe for concurrent use.
//
// Returning a *Server (rather than just an http.Handler) lets
// callers reach for ListenAndServe and for future hooks (graceful
// shutdown, live reload) without changing the public signature.
func NewServer(s *store.Store, opts Options) (*Server, error) {
	if s == nil {
		return nil, errors.New("ui: nil store")
	}
	tpl, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	srv := &Server{
		store:     s,
		mux:       http.NewServeMux(),
		templates: tpl,
		dbPath:    opts.DBPath,
	}
	srv.registerRoutes()
	return srv, nil
}

// ServeHTTP makes Server an http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// ListenAndServe binds to addr and serves until ctx is cancelled.
// When ctx is cancelled the server is shut down gracefully with a
// 5-second deadline. If addr ends with ":0" the kernel picks a free
// port; resolved is invoked with the chosen address before the
// blocking serve, so test callers can learn the URL.
func (s *Server) ListenAndServe(ctx context.Context, addr string, resolved func(string)) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("ui: listen %s: %w", addr, err)
	}
	if resolved != nil {
		resolved(ln.Addr().String())
	}
	httpSrv := &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.Serve(ln) }()

	select {
	case <-ctx.Done():
		// Parent ctx is already cancelled — Shutdown needs its own
		// deadline to finish draining in-flight requests.
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx) //nolint:contextcheck // intentional fresh ctx; parent is already done
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) registerRoutes() {
	// Go 1.22+ ServeMux patterns: `{name}` captures a path segment,
	// `{$}` anchors the path so "/" doesn't swallow everything.
	s.mux.HandleFunc("GET /{$}", s.handleRoot)
	s.mux.HandleFunc("GET /runs/{runID}", s.handleRun)
	s.mux.HandleFunc("GET /runs/{runID}/spans/{spanID}", s.handleSpan)
	s.mux.HandleFunc("GET /api/runs", s.handleAPIRuns)
	s.mux.HandleFunc("GET /api/runs/{runID}/spans", s.handleAPIRunSpans)
	s.mux.HandleFunc("GET /api/runs/{runID}/spans/{spanID}", s.handleAPISpan)
	s.mux.HandleFunc("GET /api/stream/runs", s.handleStreamRuns)
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	static, err := fs.Sub(assets, "static")
	if err == nil {
		s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	}
}

func parseTemplates() (*template.Template, error) {
	tpl := template.New("").Funcs(templateFuncs())
	tpl, err := tpl.ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("ui: parse templates: %w", err)
	}
	return tpl, nil
}
