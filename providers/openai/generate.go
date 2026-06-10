package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/YasserCR/galdor/pkg/provider"
)

// Generate implements provider.Provider.
func (p *Provider) Generate(ctx context.Context, req provider.Request) (*provider.Response, error) {
	if err := p.Capabilities().ValidateRequest(req); err != nil {
		return nil, err
	}
	wire, err := buildRequest(req, false)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}

	httpReq, err := p.newRequest(ctx, http.MethodPost, "/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, normalizeHTTPError(resp)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var msg chatResponse
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil, provider.Classify(&provider.APIError{
			Provider: providerName,
			Kind:     provider.ErrServer,
			Message:  "decode response: " + err.Error(),
		})
	}
	return responseFromWire(&msg, raw)
}
