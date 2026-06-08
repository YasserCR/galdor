package google

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// buildRequest translates a galdor provider.Request into the Gemini
// generateContent shape. It hoists the system prompt into the dedicated
// systemInstruction field, folds tool-role messages back onto a "user"
// content block with functionResponse parts, and pre-builds the
// generationConfig section from sampling parameters.
func buildRequest(req provider.Request) (*generateRequest, error) {
	if req.Model == "" {
		return nil, fmt.Errorf("%w: Model is required", provider.ErrInvalidRequest)
	}

	out := &generateRequest{}

	// Look up tool calls by ID across the assistant messages in this
	// request so a later tool result can recover the function name.
	// Gemini's functionResponse is matched by name, not by ID.
	toolIDToName := map[string]string{}
	for _, m := range req.Messages {
		if m.Role != schema.RoleAssistant {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.ID != "" {
				toolIDToName[tc.ID] = tc.Name
			}
		}
	}

	for _, m := range req.Messages {
		switch m.Role {
		case schema.RoleSystem:
			out.SystemInstruction = &wireContent{
				Parts: []wirePart{{Text: m.Text()}},
			}
		case schema.RoleUser:
			parts, err := partsToWire(m.Content)
			if err != nil {
				return nil, err
			}
			out.Contents = append(out.Contents, wireContent{Role: "user", Parts: parts})
		case schema.RoleAssistant:
			parts, err := partsToWire(m.Content)
			if err != nil {
				return nil, err
			}
			for _, tc := range m.ToolCalls {
				parts = append(parts, wirePart{FunctionCall: &wireFunctionCall{
					Name: tc.Name,
					Args: tc.Arguments,
				}})
			}
			out.Contents = append(out.Contents, wireContent{Role: "model", Parts: parts})
		case schema.RoleTool:
			name := toolIDToName[m.ToolCallID]
			if name == "" {
				// Fallback: some callers may pass the function name directly
				// in ToolCallID. Don't fail — the API will reject if it's
				// invalid anyway, and we want to be tolerant.
				name = m.ToolCallID
			}
			respJSON := toolResponseJSON(m.Text())
			block := wirePart{FunctionResponse: &wireFunctionResponse{
				Name:     name,
				Response: respJSON,
			}}
			// Function responses live in a "user"-role content block.
			// Merge into the trailing user message when possible so
			// parallel tool results stay grouped.
			if n := len(out.Contents); n > 0 && out.Contents[n-1].Role == "user" {
				out.Contents[n-1].Parts = append(out.Contents[n-1].Parts, block)
			} else {
				out.Contents = append(out.Contents, wireContent{
					Role:  "user",
					Parts: []wirePart{block},
				})
			}
		default:
			return nil, fmt.Errorf("%w: unknown role %q", provider.ErrInvalidRequest, m.Role)
		}
	}

	out.GenerationConfig = buildGenerationConfig(req)

	if len(req.Tools) > 0 {
		decls := make([]wireFuncDecl, 0, len(req.Tools))
		for _, t := range req.Tools {
			decls = append(decls, wireFuncDecl{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Schema,
			})
		}
		out.Tools = []wireTool{{FunctionDeclarations: decls}}
	}

	if tc := toolConfigFromChoice(req.ToolChoice); tc != nil {
		out.ToolConfig = tc
	}

	return out, nil
}

func buildGenerationConfig(req provider.Request) *wireGenerationCfg {
	cfg := &wireGenerationCfg{
		Temperature:     req.Temperature,
		TopP:            req.TopP,
		MaxOutputTokens: req.MaxTokens,
		StopSequences:   req.StopSequences,
	}
	if rf := req.ResponseFormat; rf != nil {
		switch rf.Type {
		case provider.ResponseFormatJSONObject:
			cfg.ResponseMIMEType = "application/json"
		case provider.ResponseFormatJSONSchema:
			cfg.ResponseMIMEType = "application/json"
			cfg.ResponseSchema = rf.Schema
		}
	}
	if rc := req.Reasoning; rc != nil && rc.Enabled {
		// Gemini is budget-based: ask for thought summaries and, when a
		// budget is given, cap the reasoning tokens. Effort is ignored.
		tc := &wireThinkingCfg{IncludeThoughts: true}
		if rc.Budget > 0 {
			b := rc.Budget
			tc.ThinkingBudget = &b
		}
		cfg.ThinkingConfig = tc
	}
	// Return nil when nothing was set, to keep the request body small.
	if cfg.Temperature == nil && cfg.TopP == nil && cfg.MaxOutputTokens == nil &&
		len(cfg.StopSequences) == 0 && cfg.ResponseMIMEType == "" && len(cfg.ResponseSchema) == 0 &&
		cfg.ThinkingConfig == nil {
		return nil
	}
	return cfg
}

func partsToWire(parts []schema.ContentPart) ([]wirePart, error) {
	out := make([]wirePart, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case schema.ContentTypeText:
			if p.Text == "" {
				continue
			}
			out = append(out, wirePart{Text: p.Text})
		case schema.ContentTypeImage:
			if p.Image == nil {
				return nil, fmt.Errorf("%w: image part with nil Image", provider.ErrInvalidRequest)
			}
			blob, err := imageToWire(p.Image)
			if err != nil {
				return nil, err
			}
			out = append(out, wirePart{InlineData: blob})
		case schema.ContentTypeThinking:
			// Reasoning parts are model output, not input: never echo
			// them back on the request (Gemini has no inbound thought
			// part, and resending would be rejected). Skipping keeps a
			// captured assistant turn safe to feed into a later call.
			continue
		default:
			return nil, fmt.Errorf("%w: unsupported content type %q", provider.ErrInvalidRequest, p.Type)
		}
	}
	return out, nil
}

// imageToWire returns a Gemini inlineData blob. Gemini does not accept
// http(s) URLs directly; for URL-based images, callers must download
// them beforehand or use the File API. The URL path is rejected here
// rather than silently failing on the server.
func imageToWire(img *schema.ImageContent) (*wireBlob, error) {
	switch {
	case len(img.Data) > 0:
		if img.Media == "" {
			return nil, fmt.Errorf("%w: inline image missing Media (MIME type)", provider.ErrInvalidRequest)
		}
		return &wireBlob{
			MimeType: img.Media,
			Data:     base64.StdEncoding.EncodeToString(img.Data),
		}, nil
	case img.URL != "":
		return nil, fmt.Errorf("%w: Gemini does not accept image URLs in inline content; fetch the bytes or upload via the File API", provider.ErrInvalidRequest)
	default:
		return nil, fmt.Errorf("%w: image part with no Data", provider.ErrInvalidRequest)
	}
}

func toolConfigFromChoice(c provider.ToolChoice) *wireToolConfig {
	switch c {
	case provider.ToolChoiceAuto:
		return &wireToolConfig{FunctionCallingConfig: &wireFCCfg{Mode: "AUTO"}}
	case provider.ToolChoiceNone:
		return &wireToolConfig{FunctionCallingConfig: &wireFCCfg{Mode: "NONE"}}
	case provider.ToolChoiceRequired:
		return &wireToolConfig{FunctionCallingConfig: &wireFCCfg{Mode: "ANY"}}
	default:
		return nil
	}
}

// toolResponseJSON wraps a plain text tool result into the JSON object
// shape Gemini's functionResponse.response expects. If text already
// looks like a JSON object, it is passed through verbatim.
func toolResponseJSON(text string) json.RawMessage {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		return json.RawMessage(trimmed)
	}
	b, err := json.Marshal(map[string]string{"result": text})
	if err != nil {
		// Unreachable for the controlled input above, but keep a safe fallback.
		return json.RawMessage(`{"result":""}`)
	}
	return b
}

// synthTooID builds a stable, parseable ID for a function call coming
// back from Gemini. The format embeds the function name so a later
// ToolResultMessage can recover it (galdor's schema.ToolCall carries
// both ID and Name in the assistant turn, so this is belt-and-braces).
func synthToolID(name string, partIndex int) string {
	return fmt.Sprintf("gfc_%d_%s", partIndex, name)
}

// responseFromWire collapses a non-streaming Gemini response into a
// galdor provider.Response.
func responseFromWire(r *generateResponse, raw []byte) *provider.Response {
	msg := schema.Message{Role: schema.RoleAssistant}
	stopReason := schema.StopReason("")

	if len(r.Candidates) > 0 {
		c := r.Candidates[0]
		stopReason = normalizeFinishReason(c.FinishReason)
		for i, p := range c.Content.Parts {
			switch {
			case p.FunctionCall != nil:
				msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
					ID:        synthToolID(p.FunctionCall.Name, i),
					Name:      p.FunctionCall.Name,
					Arguments: p.FunctionCall.Args,
				})
			case p.Thought && p.Text != "":
				// Thought summaries (returned only when Request.Reasoning
				// asked for them) are surfaced as a separate thinking
				// part. Message.Text() skips it, so the reply stays clean.
				msg.Content = append(msg.Content, schema.ThinkingPart(p.Text))
			case p.Text != "":
				msg.Content = append(msg.Content, schema.TextPart(p.Text))
			}
		}
	}

	return &provider.Response{
		Message:     msg,
		StopReason:  stopReason,
		Usage:       usageFromWire(r.UsageMetadata),
		Model:       r.ModelVersion,
		ProviderRaw: raw,
	}
}

func usageFromWire(u wireUsage) schema.Usage {
	return schema.Usage{
		InputTokens:     u.PromptTokenCount,
		OutputTokens:    u.CandidatesTokenCount + u.ThoughtsTokenCount,
		CacheReadTokens: u.CachedContentTokenCount,
	}
}

func normalizeFinishReason(s string) schema.StopReason {
	switch s {
	case "STOP":
		return schema.StopReasonEndTurn
	case "MAX_TOKENS":
		return schema.StopReasonMaxTokens
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII":
		return schema.StopReasonRefusal
	case "":
		return ""
	default:
		return schema.StopReason(strings.ToLower(s))
	}
}
