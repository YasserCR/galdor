package openai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// buildRequest translates a galdor provider.Request into the OpenAI Chat
// Completions wire shape. Unlike Anthropic, OpenAI carries the system
// prompt as a role=system message, and tool results as role=tool messages
// with tool_call_id.
func buildRequest(req provider.Request, stream bool) (*chatRequest, error) {
	if req.Model == "" {
		return nil, fmt.Errorf("%w: Model is required", provider.ErrInvalidRequest)
	}

	out := &chatRequest{
		Model:       req.Model,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stop:        req.StopSequences,
		Stream:      stream,
	}
	if stream {
		out.StreamOptions = &wireStreamOpts{IncludeUsage: true}
	}

	for _, m := range req.Messages {
		wm, err := messageToWire(m)
		if err != nil {
			return nil, err
		}
		out.Messages = append(out.Messages, wm)
	}

	if len(req.Tools) > 0 {
		out.Tools = make([]wireTool, 0, len(req.Tools))
		for _, t := range req.Tools {
			out.Tools = append(out.Tools, wireTool{
				Type: "function",
				Function: wireFuncDecl{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.Schema,
				},
			})
		}
	}

	if tc := toolChoiceToWire(req.ToolChoice); tc != nil {
		out.ToolChoice = tc
	}

	if rf := responseFormatToWire(req.ResponseFormat); rf != nil {
		out.ResponseFormat = rf
	}

	if rc := req.Reasoning; rc != nil && rc.Enabled {
		// OpenAI is effort-based (o-series): map the effort level,
		// defaulting to medium. Budget is ignored.
		effort := string(rc.Effort)
		if effort == "" {
			effort = string(provider.ReasoningEffortMedium)
		}
		out.ReasoningEffort = effort
	}

	if uid, ok := req.Metadata["user_id"]; ok && uid != "" {
		out.User = uid
	}

	return out, nil
}

// messageToWire converts a single schema.Message into its wire shape,
// choosing the string form of content when possible to keep payloads small.
func messageToWire(m schema.Message) (wireMessage, error) {
	role, err := roleToWire(m.Role)
	if err != nil {
		return wireMessage{}, err
	}
	wm := wireMessage{
		Role:       role,
		Name:       m.Name,
		ToolCallID: m.ToolCallID,
	}

	// Assistant tool calls.
	for _, tc := range m.ToolCalls {
		wm.ToolCalls = append(wm.ToolCalls, wireToolCall{
			ID:   tc.ID,
			Type: "function",
			Function: wireFuncCall{
				Name:      tc.Name,
				Arguments: string(tc.Arguments),
			},
		})
	}

	// Content. Prefer the plain-string form when all parts are text; use
	// the array form when any non-text part is present.
	allText := true
	for _, p := range m.Content {
		if p.Type != schema.ContentTypeText {
			allText = false
			break
		}
	}

	switch {
	case len(m.Content) == 0 && len(wm.ToolCalls) > 0:
		// Assistant tool-call-only messages omit content. OpenAI accepts
		// this when tool_calls is present.
		wm.Content = nil
	case allText:
		text := m.Text()
		raw, err := json.Marshal(text)
		if err != nil {
			return wireMessage{}, err
		}
		wm.Content = raw
	default:
		parts, err := partsToWire(m.Content)
		if err != nil {
			return wireMessage{}, err
		}
		raw, err := json.Marshal(parts)
		if err != nil {
			return wireMessage{}, err
		}
		wm.Content = raw
	}

	return wm, nil
}

func roleToWire(r schema.Role) (string, error) {
	switch r {
	case schema.RoleSystem:
		return "system", nil
	case schema.RoleUser:
		return "user", nil
	case schema.RoleAssistant:
		return "assistant", nil
	case schema.RoleTool:
		return "tool", nil
	default:
		return "", fmt.Errorf("%w: unknown role %q", provider.ErrInvalidRequest, r)
	}
}

func partsToWire(parts []schema.ContentPart) ([]wireContentPart, error) {
	out := make([]wireContentPart, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case schema.ContentTypeText:
			out = append(out, wireContentPart{Type: "text", Text: p.Text})
		case schema.ContentTypeImage:
			if p.Image == nil {
				return nil, fmt.Errorf("%w: image part with nil Image", provider.ErrInvalidRequest)
			}
			url, err := imageToURL(p.Image)
			if err != nil {
				return nil, err
			}
			out = append(out, wireContentPart{Type: "image_url", ImageURL: &wireImageURL{URL: url}})
		case schema.ContentTypeThinking:
			// Reasoning parts are model output, not input: never echo
			// them back on the request. Skipping keeps a captured
			// assistant turn safe to feed into a later call.
			continue
		default:
			return nil, fmt.Errorf("%w: unsupported content type %q", provider.ErrInvalidRequest, p.Type)
		}
	}
	return out, nil
}

// imageToURL returns a string suitable for OpenAI's image_url.url field —
// either a direct URL or a data: URL for inline bytes.
func imageToURL(img *schema.ImageContent) (string, error) {
	switch {
	case img.URL != "":
		return img.URL, nil
	case len(img.Data) > 0:
		if img.Media == "" {
			return "", fmt.Errorf("%w: inline image missing Media (MIME type)", provider.ErrInvalidRequest)
		}
		return "data:" + img.Media + ";base64," + base64.StdEncoding.EncodeToString(img.Data), nil
	default:
		return "", fmt.Errorf("%w: image part with no URL or Data", provider.ErrInvalidRequest)
	}
}

func toolChoiceToWire(c provider.ToolChoice) json.RawMessage {
	switch c {
	case provider.ToolChoiceAuto:
		return json.RawMessage(`"auto"`)
	case provider.ToolChoiceNone:
		return json.RawMessage(`"none"`)
	case provider.ToolChoiceRequired:
		return json.RawMessage(`"required"`)
	default:
		return nil
	}
}

func responseFormatToWire(rf *provider.ResponseFormat) *wireRespFormat {
	if rf == nil {
		return nil
	}
	switch rf.Type {
	case provider.ResponseFormatJSONObject:
		return &wireRespFormat{Type: "json_object"}
	case provider.ResponseFormatJSONSchema:
		return &wireRespFormat{
			Type: "json_schema",
			JSONSchema: &wireJSONSchema{
				Name:   rf.Name,
				Strict: true,
				Schema: rf.Schema,
			},
		}
	default:
		return nil
	}
}

// responseFromWire collapses a non-streaming Chat Completions response
// into a galdor provider.Response.
func responseFromWire(r *chatResponse, raw []byte) *provider.Response {
	msg := schema.Message{Role: schema.RoleAssistant}
	stopReason := schema.StopReason("")

	if len(r.Choices) > 0 {
		c := r.Choices[0]
		stopReason = normalizeFinishReason(c.FinishReason)

		if c.Message.ReasoningContent != "" {
			// Reasoning from an OpenAI-compatible model (e.g. DeepSeek).
			// Message.Text() skips it, so the answer stays clean.
			msg.Content = append(msg.Content, schema.ThinkingPart(c.Message.ReasoningContent))
		}
		if len(c.Message.Content) > 0 {
			text, err := decodeContent(c.Message.Content)
			if err == nil && text != "" {
				msg.Content = append(msg.Content, schema.TextPart(text))
			}
		}
		for _, tc := range c.Message.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: json.RawMessage(tc.Function.Arguments),
			})
		}
	}

	return &provider.Response{
		Message:     msg,
		StopReason:  stopReason,
		Usage:       usageFromWire(r.Usage),
		Model:       r.Model,
		ProviderRaw: raw,
	}
}

// decodeContent handles both content forms: a plain JSON string and an
// array of {type, text}/{type, image_url} parts. Image parts in responses
// are unusual but tolerated; only text is returned.
func decodeContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	// Try plain string first (cheap, common path).
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var parts []wireContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", err
	}
	out := ""
	for _, p := range parts {
		if p.Type == "text" {
			out += p.Text
		}
	}
	return out, nil
}

func usageFromWire(u wireUsage) schema.Usage {
	out := schema.Usage{
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
	}
	if u.PromptTokensDetails != nil {
		out.CacheReadTokens = u.PromptTokensDetails.CachedTokens
	}
	return out
}

func normalizeFinishReason(s string) schema.StopReason {
	switch s {
	case "stop":
		return schema.StopReasonEndTurn
	case "length":
		return schema.StopReasonMaxTokens
	case "tool_calls", "function_call":
		return schema.StopReasonToolUse
	case "content_filter":
		return schema.StopReasonRefusal
	case "":
		return ""
	default:
		return schema.StopReason(s)
	}
}
