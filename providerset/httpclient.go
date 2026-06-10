package providerset

import (
	"net"
	"net/http"
	"time"
)

// streamSafeHTTPClient builds the client installed when LLM_HTTP_TIMEOUT
// is set. The configured duration maps to the transport's
// ResponseHeaderTimeout (time-to-first-byte), NOT to http.Client.Timeout:
// the latter caps the whole exchange including the response body, which
// would abort long SSE streams and extended-thinking generations. With
// this shape a stuck connection still times out, but a legitimate long
// stream is bounded only by the request context.
func streamSafeHTTPClient(headerTimeout time.Duration) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ResponseHeaderTimeout: headerTimeout,
		},
	}
}
