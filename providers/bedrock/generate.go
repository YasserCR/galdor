package bedrock

import (
	"context"
	"encoding/json"

	"github.com/YasserCR/galdor/pkg/provider"
)

// Generate implements provider.Provider.
func (p *Provider) Generate(ctx context.Context, req provider.Request) (*provider.Response, error) {
	if err := p.Capabilities().ValidateRequest(req); err != nil {
		return nil, err
	}
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

	resp := responseFromConverse(out, raw)
	resp.Model = req.Model
	// Bedrock's Converse has no "none" tool choice, so the model can still
	// emit tool_use even when ToolChoiceNone was requested. Honor the
	// cross-provider contract (ToolChoiceNone -> no tool calls produced)
	// by stripping them from the response.
	enforceToolChoiceNone(resp, req.ToolChoice)
	return resp, nil
}

// enforceToolChoiceNone strips any tool calls from resp when the caller
// asked for ToolChoiceNone, which Bedrock's Converse API can't express
// natively.
func enforceToolChoiceNone(resp *provider.Response, choice provider.ToolChoice) {
	if resp != nil && choice == provider.ToolChoiceNone {
		resp.Message.ToolCalls = nil
	}
}
