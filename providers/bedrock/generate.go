package bedrock

import (
	"context"
	"encoding/json"

	"github.com/YasserCR/galdor/pkg/provider"
)

// Generate implements provider.Provider.
func (p *Provider) Generate(ctx context.Context, req provider.Request) (*provider.Response, error) {
	in, err := buildConverseInput(req)
	if err != nil {
		return nil, err
	}

	out, err := p.client.Converse(ctx, in)
	if err != nil {
		return nil, normalizeAWSError(err)
	}

	// The SDK doesn't expose the raw HTTP body; serialize the decoded
	// output back to JSON so ProviderRaw still gives trace consumers a
	// stable, machine-readable form.
	raw, _ := json.Marshal(out)

	return responseFromConverse(out, raw), nil
}
