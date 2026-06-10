package anthropic

import (
	"net"
	"net/http"
	"time"
)

// defaultResponseHeaderTimeout bounds how long the adapter waits for the
// server to start responding (time-to-first-byte of the response
// headers) when the caller does not supply their own *http.Client.
const defaultResponseHeaderTimeout = 60 * time.Second

// streamSafeHTTPClient builds the default HTTP client. It deliberately
// leaves http.Client.Timeout unset: that field caps the ENTIRE exchange
// including the response-body read, so any SSE stream — or an
// extended-thinking generation — longer than the timeout is aborted
// mid-flight with a misleading "Client.Timeout exceeded while reading
// body". Instead the transport bounds only the connection and
// time-to-first-byte phases; the body's lifetime is governed by the
// request context, matching the Config doc's streaming contract.
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
