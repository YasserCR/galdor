package builtins

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/YasserCR/galdor/pkg/tool"
)

// HTTPGetIn is the input shape of the http_get tool.
type HTTPGetIn struct {
	URL string `json:"url" jsonschema:"Absolute http(s) URL to fetch"`
}

// HTTPGetOut is the response surface. Body is truncated to the
// constructor's MaxBytes; Truncated reports whether that happened.
type HTTPGetOut struct {
	Status      int    `json:"status"`
	ContentType string `json:"content_type,omitempty"`
	Body        string `json:"body"`
	Truncated   bool   `json:"truncated,omitempty"`
	URL         string `json:"url"`
}

// HTTPGetOptions tunes the http_get tool's safety profile. Defaults
// are conservative; callers explicitly opt into broader behavior.
type HTTPGetOptions struct {
	// HTTPClient lets callers inject a custom *http.Client (timeouts,
	// proxies, observability middleware). If nil, an internal client
	// with the Timeout below is used.
	HTTPClient *http.Client

	// Timeout caps the entire request. Default 10s.
	Timeout time.Duration

	// MaxBytes caps how many bytes of body are returned to the LLM.
	// Default 1 MiB. Bodies larger than this are truncated and
	// Truncated=true on the output.
	MaxBytes int64

	// AllowedHosts, when non-empty, restricts the tool to URLs whose
	// host matches one of the entries. Matching is exact on lowercase
	// host (no wildcards). An empty list allows any host — which is
	// usually wrong for an agent talking to an LLM; configure this
	// unless you really mean "open the entire web".
	AllowedHosts []string

	// AllowHTTP, when true, allows plain http:// URLs. By default the
	// tool requires https:// to avoid trivial MITM exposure.
	AllowHTTP bool
}

// NewHTTPGetTool returns an http_get tool configured with opts. The
// tool only supports GET; agents that need POST/PUT/DELETE should
// register custom tools to keep blast radius small.
func NewHTTPGetTool(opts HTTPGetOptions) (tool.Tool[HTTPGetIn, HTTPGetOut], error) {
	cfg := opts.normalize()
	return tool.NewTool("http_get",
		"Fetch the body of an https URL (capped, allowlist-gated). Returns status, content-type, body.",
		cfg.run)
}

// MustNewHTTPGetTool is the panicking variant.
func MustNewHTTPGetTool(opts HTTPGetOptions) tool.Tool[HTTPGetIn, HTTPGetOut] {
	t, err := NewHTTPGetTool(opts)
	if err != nil {
		panic(err)
	}
	return t
}

type httpGetConfig struct {
	client       *http.Client
	maxBytes     int64
	allowedHosts map[string]struct{}
	allowHTTP    bool
}

func (o HTTPGetOptions) normalize() httpGetConfig {
	c := httpGetConfig{
		client:    o.HTTPClient,
		maxBytes:  o.MaxBytes,
		allowHTTP: o.AllowHTTP,
	}
	if c.maxBytes <= 0 {
		c.maxBytes = 1 << 20 // 1 MiB
	}
	if len(o.AllowedHosts) > 0 {
		c.allowedHosts = make(map[string]struct{}, len(o.AllowedHosts))
		for _, h := range o.AllowedHosts {
			// Normalize entries to a portless hostname so the stored key
			// matches the request-host comparison below — an entry with a
			// port could otherwise never match.
			c.allowedHosts[hostKey(h)] = struct{}{}
		}
	}
	if c.client == nil {
		timeout := o.Timeout
		if timeout == 0 {
			timeout = 10 * time.Second
		}
		allowed := c.allowedHosts
		allowHTTP := c.allowHTTP
		c.client = &http.Client{
			Timeout: timeout,
			// Re-validate scheme + host on EVERY redirect hop. The initial
			// allowlist check is otherwise bypassed by a redirect to an
			// un-vetted host (SSRF).
			CheckRedirect: func(req *http.Request, _ []*http.Request) error {
				return validateURL(req.URL, allowHTTP, allowed)
			},
		}
	}
	return c
}

// hostKey normalizes a host or allowlist entry to a lowercase, portless
// hostname for comparison.
func hostKey(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))
	if i := strings.IndexByte(h, ':'); i >= 0 {
		h = h[:i]
	}
	return h
}

// validateURL enforces the scheme rule and the host allowlist. It is used
// both for the initial request and (via CheckRedirect) every redirect hop.
func validateURL(u *url.URL, allowHTTP bool, allowed map[string]struct{}) error {
	switch strings.ToLower(u.Scheme) {
	case "https":
		// always allowed
	case "http":
		if !allowHTTP {
			return fmt.Errorf("http_get: refusing plain http; pass AllowHTTP=true to opt in")
		}
	default:
		return fmt.Errorf("http_get: unsupported scheme %q", u.Scheme)
	}
	if allowed != nil {
		h := strings.ToLower(u.Hostname())
		if _, ok := allowed[h]; !ok {
			return fmt.Errorf("%w: %q", ErrHostNotAllowed, h)
		}
	}
	return nil
}

// ErrHostNotAllowed is returned when the requested URL's host is not
// in the configured allowlist.
var ErrHostNotAllowed = errors.New("http_get: host not in allowlist")

func (c httpGetConfig) run(ctx context.Context, in HTTPGetIn) (HTTPGetOut, error) {
	u, err := url.Parse(in.URL)
	if err != nil {
		return HTTPGetOut{}, fmt.Errorf("http_get: invalid URL: %w", err)
	}
	if verr := validateURL(u, c.allowHTTP, c.allowedHosts); verr != nil {
		return HTTPGetOut{}, verr
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return HTTPGetOut{}, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return HTTPGetOut{}, fmt.Errorf("http_get: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read at most maxBytes+1 so we can detect truncation reliably.
	limited := io.LimitReader(resp.Body, c.maxBytes+1)
	buf, err := io.ReadAll(limited)
	if err != nil {
		return HTTPGetOut{}, fmt.Errorf("http_get: read body: %w", err)
	}
	truncated := false
	if int64(len(buf)) > c.maxBytes {
		buf = buf[:c.maxBytes]
		truncated = true
	}

	return HTTPGetOut{
		Status:      resp.StatusCode,
		ContentType: resp.Header.Get("content-type"),
		Body:        string(buf),
		Truncated:   truncated,
		URL:         u.String(),
	}, nil
}
