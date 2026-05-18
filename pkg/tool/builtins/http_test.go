package builtins

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestHTTPGet_HappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "hello world")
	}))
	defer srv.Close()
	host := mustHost(t, srv.URL)

	tt := MustNewHTTPGetTool(HTTPGetOptions{
		AllowHTTP:    true,
		AllowedHosts: []string{host},
	})
	out, err := tt.Execute(context.Background(), HTTPGetIn{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != 200 || out.Body != "hello world" {
		t.Errorf("out = %+v", out)
	}
	if !strings.HasPrefix(out.ContentType, "text/plain") {
		t.Errorf("content-type: %q", out.ContentType)
	}
}

func TestHTTPGet_Truncates(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("a", 1000)))
	}))
	defer srv.Close()
	host := mustHost(t, srv.URL)

	tt := MustNewHTTPGetTool(HTTPGetOptions{
		AllowHTTP:    true,
		AllowedHosts: []string{host},
		MaxBytes:     100,
	})
	out, err := tt.Execute(context.Background(), HTTPGetIn{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Body) != 100 || !out.Truncated {
		t.Errorf("expected truncation: len=%d truncated=%v", len(out.Body), out.Truncated)
	}
}

func TestHTTPGet_RejectsHTTPByDefault(t *testing.T) {
	t.Parallel()
	tt := MustNewHTTPGetTool(HTTPGetOptions{})
	_, err := tt.Execute(context.Background(), HTTPGetIn{URL: "http://example.com"})
	if err == nil || !strings.Contains(err.Error(), "plain http") {
		t.Fatalf("err = %v", err)
	}
}

func TestHTTPGet_RejectsUnknownScheme(t *testing.T) {
	t.Parallel()
	tt := MustNewHTTPGetTool(HTTPGetOptions{})
	_, err := tt.Execute(context.Background(), HTTPGetIn{URL: "ftp://example.com"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestHTTPGet_AllowlistEnforced(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	// Allowlist deliberately excludes the test server's host.
	tt := MustNewHTTPGetTool(HTTPGetOptions{
		AllowHTTP:    true,
		AllowedHosts: []string{"forbidden.example.com"},
	})
	_, err := tt.Execute(context.Background(), HTTPGetIn{URL: srv.URL})
	if !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("err = %v, want ErrHostNotAllowed", err)
	}
}

func TestHTTPGet_InvalidURL(t *testing.T) {
	t.Parallel()
	tt := MustNewHTTPGetTool(HTTPGetOptions{AllowHTTP: true})
	_, err := tt.Execute(context.Background(), HTTPGetIn{URL: "://broken"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestHTTPGet_TimeoutHonored(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()
	host := mustHost(t, srv.URL)
	tt := MustNewHTTPGetTool(HTTPGetOptions{
		AllowHTTP:    true,
		AllowedHosts: []string{host},
		Timeout:      50 * time.Millisecond,
	})
	_, err := tt.Execute(context.Background(), HTTPGetIn{URL: srv.URL})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func mustHost(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	host := u.Host
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	return host
}
