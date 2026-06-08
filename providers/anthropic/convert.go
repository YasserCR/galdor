package anthropic

import (
	"encoding/base64"
	"fmt"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// buildRequest translates a galdor provider.Request into the Anthropic
// wire shape. It hoists the system prompt into the dedicated `system`
// field, drops tool-role messages back into the user role with a
// tool_result content block, and normalizes tool choice.
func buildRequest(req provider.Request, stream bool) (*messageRequest, error) {
	if req.Model == "" {
		return nil, fmt.Errorf("%w: Model is required", provider.ErrInvalidRequest)
	}

	maxTokens := 1024
	if req.MaxTokens != nil {
		maxTokens = *req.MaxTokens
	}

	out := &messageRequest{
		Model:         req.Model,
		MaxTokens:     maxTokens,
		Temperature:   req.Temperature,
		TopP:          req.TopP,
		StopSequences: req.StopSequences,
		Stream:        stream,
	}

	if rc := req.Reasoning; rc != nil && rc.Enabled {
		// Anthropic is budget-based. budget_tokens must be >= 1024 and
		// strictly less than max_tokens; default to the minimum and grow
		// max_tokens if it can't fit both the reasoning and an answer.
		budget := rc.Budget
		if budget < 1024 {
			budget = 1024
		}
		if out.MaxTokens <= budget {
			out.MaxTokens = budget + maxTokens
		}
		out.Thinking = &wireThinking{Type: "enabled", BudgetTokens: budget}
		// Extended thinking is incompatible with temperature / top_p
		// tuning — drop them so the request isn't rejected.
		out.Temperature = nil
		out.TopP = nil
	}

	for _, m := range req.Messages {
		switch m.Role {
		case schema.RoleSystem:
			out.System = append(out.System, wireSystemBlock{
				Type:         "text",
				Text:         m.Text(),
				CacheControl: cacheControl(m.CacheControl),
			})
		case schema.RoleUser:
			wm, err := userMessageToWire(m)
			if err != nil {
				return nil, err
			}
			out.Messages = append(out.Messages, wm)
		case schema.RoleAssistant:
			wm, err := assistantMessageToWire(m)
			if err != nil {
				return nil, err
			}
			out.Messages = append(out.Messages, wm)
		case schema.RoleTool:
			// Anthropic carries tool results as content blocks under a
			// user-role message. Merge consecutive tool results into the
			// trailing user message when possible.
			block := wireContentBlock{
				Type:      "tool_result",
				ToolUseID: m.ToolCallID,
				Content:   []wireContentBlock{{Type: "text", Text: m.Text()}},
			}
			if n := len(out.Messages); n > 0 && out.Messages[n-1].Role == "user" {
				out.Messages[n-1].Content = append(out.Messages[n-1].Content, block)
			} else {
				out.Messages = append(out.Messages, wireMessage{
					Role:    "user",
					Content: []wireContentBlock{block},
				})
			}
		default:
			return nil, fmt.Errorf("%w: unknown role %q", provider.ErrInvalidRequest, m.Role)
		}
	}

	if len(req.Tools) > 0 {
		out.Tools = make([]wireTool, 0, len(req.Tools))
		for _, t := range req.Tools {
			out.Tools = append(out.Tools, wireTool{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: t.Schema,
			})
		}
	}

	if tc := toolChoiceToWire(req.ToolChoice); tc != nil {
		out.ToolChoice = tc
	}

	if uid, ok := req.Metadata["user_id"]; ok && uid != "" {
		out.Metadata = &wireMetadata{UserID: uid}
	}

	return out, nil
}

func userMessageToWire(m schema.Message) (wireMessage, error) {
	blocks, err := partsToWire(m.Content, m.CacheControl)
	if err != nil {
		return wireMessage{}, err
	}
	return wireMessage{Role: "user", Content: blocks}, nil
}

func assistantMessageToWire(m schema.Message) (wireMessage, error) {
	blocks, err := partsToWire(m.Content, m.CacheControl)
	if err != nil {
		return wireMessage{}, err
	}
	for _, tc := range m.ToolCalls {
		blocks = append(blocks, wireContentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Name,
			Input: tc.Arguments,
		})
	}
	return wireMessage{Role: "assistant", Content: blocks}, nil
}

func partsToWire(parts []schema.ContentPart, cc *schema.CacheControl) ([]wireContentBlock, error) {
	out := make([]wireContentBlock, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case schema.ContentTypeText:
			out = append(out, wireContentBlock{Type: "text", Text: p.Text})
		case schema.ContentTypeImage:
			if p.Image == nil {
				return nil, fmt.Errorf("%w: image part with nil Image", provider.ErrInvalidRequest)
			}
			src, err := imageToWire(p.Image)
			if err != nil {
				return nil, err
			}
			out = append(out, wireContentBlock{Type: "image", Source: src})
		case schema.ContentTypeThinking:
			// Reasoning parts are model output. Anthropic does accept
			// thinking blocks back (with their signature) for extended-
			// thinking continuations, but resending one without a valid
			// signature is rejected — so for now we skip them. This
			// keeps a captured assistant turn safe to feed back; native
			// thinking round-trip is a separate, later step.
			continue
		default:
			return nil, fmt.Errorf("%w: unsupported content type %q", provider.ErrInvalidRequest, p.Type)
		}
	}
	// Attach the cache_control hint to the LAST block of the message, which
	// matches Anthropic's "cache up to this point" semantics.
	if cc != nil && len(out) > 0 {
		out[len(out)-1].CacheControl = cacheControl(cc)
	}
	return out, nil
}

func imageToWire(img *schema.ImageContent) (*wireImageSource, error) {
	switch {
	case img.URL != "":
		return &wireImageSource{Type: "url", URL: img.URL}, nil
	case len(img.Data) > 0:
		if img.Media == "" {
			return nil, fmt.Errorf("%w: inline image missing Media (MIME type)", provider.ErrInvalidRequest)
		}
		return &wireImageSource{
			Type:      "base64",
			MediaType: img.Media,
			Data:      base64.StdEncoding.EncodeToString(img.Data),
		}, nil
	default:
		return nil, fmt.Errorf("%w: image part with no URL or Data", provider.ErrInvalidRequest)
	}
}

func cacheControl(cc *schema.CacheControl) *wireCacheControl {
	if cc == nil {
		return nil
	}
	return &wireCacheControl{Type: cc.Type}
}

func toolChoiceToWire(c provider.ToolChoice) *wireToolChoice {
	switch c {
	case provider.ToolChoiceNone:
		return &wireToolChoice{Type: "none"}
	case provider.ToolChoiceRequired:
		return &wireToolChoice{Type: "any"}
	case provider.ToolChoiceAuto:
		return &wireToolChoice{Type: "auto"}
	default:
		return nil
	}
}

// responseFromWire collapses a non-streaming Anthropic response into a
// galdor provider.Response.
func responseFromWire(r *messageResponse, raw []byte) *provider.Response {
	msg := schema.Message{Role: schema.RoleAssistant}
	for _, b := range r.Content {
		switch b.Type {
		case "text":
			if b.Text != "" {
				msg.Content = append(msg.Content, schema.TextPart(b.Text))
			}
		case "tool_use":
			msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
				ID:        b.ID,
				Name:      b.Name,
				Arguments: b.Input,
			})
		case "thinking":
			// Extended-thinking block (returned only when Request.Reasoning
			// asked for it). Keep the signature for a future round-trip;
			// Message.Text() skips this part, so the answer stays clean.
			if b.Thinking != "" {
				msg.Content = append(msg.Content, schema.ContentPart{
					Type:      schema.ContentTypeThinking,
					Text:      b.Thinking,
					Signature: b.Signature,
				})
			}
		}
	}
	return &provider.Response{
		Message:     msg,
		StopReason:  normalizeStopReason(r.StopReason),
		Usage:       usageFromWire(r.Usage),
		Model:       r.Model,
		ProviderRaw: raw,
	}
}

func usageFromWire(u wireUsage) schema.Usage {
	return schema.Usage{
		InputTokens:         u.InputTokens,
		OutputTokens:        u.OutputTokens,
		CacheCreationTokens: u.CacheCreationInputTokens,
		CacheReadTokens:     u.CacheReadInputTokens,
	}
}

func normalizeStopReason(s string) schema.StopReason {
	switch s {
	case "end_turn":
		return schema.StopReasonEndTurn
	case "max_tokens":
		return schema.StopReasonMaxTokens
	case "tool_use":
		return schema.StopReasonToolUse
	case "stop_sequence":
		return schema.StopReasonStopSequence
	case "refusal":
		return schema.StopReasonRefusal
	case "":
		return ""
	default:
		return schema.StopReason(s)
	}
}
