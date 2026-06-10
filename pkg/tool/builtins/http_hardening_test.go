package builtins

import (
	"errors"
	"net/http"
	"testing"
)

// Regression for audit M2: redirects must be re-validated against the
// host allowlist. The default client must install a CheckRedirect that
// rejects a hop to a non-allowlisted host (SSRF via redirect).
func TestHTTPGet_RedirectRevalidatesHost(t *testing.T) {
	c := HTTPGetOptions{AllowedHosts: []string{"good.example"}}.normalize()
	if c.client.CheckRedirect == nil {
		t.Fatal("default client must set CheckRedirect to re-validate redirect hops (regression of M2)")
	}
	bad, _ := http.NewRequest(http.MethodGet, "https://evil.example/x", nil)
	if err := c.client.CheckRedirect(bad, nil); !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("a redirect to a non-allowlisted host must be rejected (regression of M2), got %v", err)
	}
	ok, _ := http.NewRequest(http.MethodGet, "https://good.example/y", nil)
	if err := c.client.CheckRedirect(ok, nil); err != nil {
		t.Fatalf("a redirect to the allowlisted host must pass, got %v", err)
	}
}

// Regression for audit M3: an allowlist entry that includes a port used
// to never match, because the lookup stripped the port from the request
// host but the entry was stored verbatim. Entries must normalize to a
// bare, lowercase host.
func TestHTTPGet_AllowedHostEntryWithPortNormalizes(t *testing.T) {
	c := HTTPGetOptions{AllowedHosts: []string{"Example.com:8443"}}.normalize()
	if _, ok := c.allowedHosts["example.com"]; !ok {
		t.Fatalf("an allowlist entry with a port must normalize to the bare host (regression of M3); got %v", c.allowedHosts)
	}
}
