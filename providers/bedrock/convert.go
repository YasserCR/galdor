package bedrock

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	bedrockdoc "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// buildConverseInput translates a galdor provider.Request into the SDK's
// ConverseInput. The system prompt is hoisted into a dedicated System
// slice (Bedrock matches Anthropic in this respect — system is not a
// role). Tool-role messages are folded onto a trailing user-role
// Message as ToolResultBlock content.
func buildConverseInput(req provider.Request) (*bedrockruntime.ConverseInput, error) {
	if req.Model == "" {
		return nil, fmt.Errorf("%w: Model is required", provider.ErrInvalidRequest)
	}
	in := &bedrockruntime.ConverseInput{
		ModelId: aws.String(req.Model),
	}

	for _, m := range req.Messages {
		switch m.Role {
		case schema.RoleSystem:
			in.System = append(in.System, &brtypes.SystemContentBlockMemberText{Value: m.Text()})
		case schema.RoleUser:
			blocks, err := partsToBlocks(m.Content)
			if err != nil {
				return nil, err
			}
			in.Messages = append(in.Messages, brtypes.Message{
				Role:    brtypes.ConversationRoleUser,
				Content: blocks,
			})
		case schema.RoleAssistant:
			blocks, err := partsToBlocks(m.Content)
			if err != nil {
				return nil, err
			}
			for _, tc := range m.ToolCalls {
				doc, err := decodeToolArgs(tc.Arguments)
				if err != nil {
					return nil, err
				}
				blocks = append(blocks, &brtypes.ContentBlockMemberToolUse{
					Value: brtypes.ToolUseBlock{
						ToolUseId: aws.String(tc.ID),
						Name:      aws.String(tc.Name),
						Input:     doc,
					},
				})
			}
			in.Messages = append(in.Messages, brtypes.Message{
				Role:    brtypes.ConversationRoleAssistant,
				Content: blocks,
			})
		case schema.RoleTool:
			block := &brtypes.ContentBlockMemberToolResult{
				Value: brtypes.ToolResultBlock{
					ToolUseId: aws.String(m.ToolCallID),
					Content: []brtypes.ToolResultContentBlock{
						&brtypes.ToolResultContentBlockMemberText{Value: m.Text()},
					},
				},
			}
			if n := len(in.Messages); n > 0 && in.Messages[n-1].Role == brtypes.ConversationRoleUser {
				in.Messages[n-1].Content = append(in.Messages[n-1].Content, block)
			} else {
				in.Messages = append(in.Messages, brtypes.Message{
					Role:    brtypes.ConversationRoleUser,
					Content: []brtypes.ContentBlock{block},
				})
			}
		default:
			return nil, fmt.Errorf("%w: unknown role %q", provider.ErrInvalidRequest, m.Role)
		}
	}

	in.InferenceConfig = buildInferenceConfig(req)

	if len(req.Tools) > 0 {
		tc, err := buildToolConfig(req.Tools, req.ToolChoice)
		if err != nil {
			return nil, err
		}
		in.ToolConfig = tc
	}

	return in, nil
}

func buildInferenceConfig(req provider.Request) *brtypes.InferenceConfiguration {
	cfg := &brtypes.InferenceConfiguration{}
	set := false
	if req.MaxTokens != nil {
		v := int32(*req.MaxTokens)
		cfg.MaxTokens = &v
		set = true
	}
	if req.Temperature != nil {
		v := float32(*req.Temperature)
		cfg.Temperature = &v
		set = true
	}
	if req.TopP != nil {
		v := float32(*req.TopP)
		cfg.TopP = &v
		set = true
	}
	if len(req.StopSequences) > 0 {
		cfg.StopSequences = req.StopSequences
		set = true
	}
	if !set {
		return nil
	}
	return cfg
}

func buildToolConfig(tools []schema.ToolDef, choice provider.ToolChoice) (*brtypes.ToolConfiguration, error) {
	out := &brtypes.ToolConfiguration{}
	for _, t := range tools {
		doc, err := decodeJSONSchemaDoc(t.Schema)
		if err != nil {
			return nil, fmt.Errorf("%w: tool %q schema: %w", provider.ErrInvalidRequest, t.Name, err)
		}
		out.Tools = append(out.Tools, &brtypes.ToolMemberToolSpec{
			Value: brtypes.ToolSpecification{
				Name:        aws.String(t.Name),
				Description: aws.String(t.Description),
				InputSchema: &brtypes.ToolInputSchemaMemberJson{Value: doc},
			},
		})
	}
	switch choice {
	case provider.ToolChoiceAuto:
		out.ToolChoice = &brtypes.ToolChoiceMemberAuto{}
	case provider.ToolChoiceNone:
		// Bedrock has no explicit "none"; omit the tools entirely. The
		// caller's expectation is "do not call tools", so we drop the
		// tool config.
		return nil, nil
	case provider.ToolChoiceRequired:
		out.ToolChoice = &brtypes.ToolChoiceMemberAny{}
	}
	return out, nil
}

// partsToBlocks converts galdor content parts into Bedrock ContentBlocks.
// Bedrock requires inline image bytes (no URLs); URL-based images are
// rejected at build time rather than failing on the server.
func partsToBlocks(parts []schema.ContentPart) ([]brtypes.ContentBlock, error) {
	out := make([]brtypes.ContentBlock, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case schema.ContentTypeText:
			if p.Text == "" {
				continue
			}
			out = append(out, &brtypes.ContentBlockMemberText{Value: p.Text})
		case schema.ContentTypeImage:
			if p.Image == nil {
				return nil, fmt.Errorf("%w: image part with nil Image", provider.ErrInvalidRequest)
			}
			if len(p.Image.Data) == 0 {
				return nil, fmt.Errorf("%w: Bedrock requires inline image bytes; URL-only images are not accepted", provider.ErrInvalidRequest)
			}
			if p.Image.Media == "" {
				return nil, fmt.Errorf("%w: inline image missing Media (MIME type)", provider.ErrInvalidRequest)
			}
			fmt_, err := imageFormatFromMIME(p.Image.Media)
			if err != nil {
				return nil, err
			}
			out = append(out, &brtypes.ContentBlockMemberImage{
				Value: brtypes.ImageBlock{
					Format: fmt_,
					Source: &brtypes.ImageSourceMemberBytes{Value: p.Image.Data},
				},
			})
		default:
			return nil, fmt.Errorf("%w: unsupported content type %q", provider.ErrInvalidRequest, p.Type)
		}
	}
	return out, nil
}

// imageFormatFromMIME maps a MIME type to the Bedrock ImageFormat enum.
// Bedrock accepts only a small allowlist (png, jpeg, gif, webp).
func imageFormatFromMIME(mime string) (brtypes.ImageFormat, error) {
	switch strings.ToLower(mime) {
	case "image/png":
		return brtypes.ImageFormatPng, nil
	case "image/jpeg", "image/jpg":
		return brtypes.ImageFormatJpeg, nil
	case "image/gif":
		return brtypes.ImageFormatGif, nil
	case "image/webp":
		return brtypes.ImageFormatWebp, nil
	default:
		return "", fmt.Errorf("%w: Bedrock does not support image MIME %q", provider.ErrInvalidRequest, mime)
	}
}

// decodeJSONSchemaDoc turns a JSON Schema document into the
// bedrockruntime document.Interface representation. NewLazyDocument
// from the SDK accepts any JSON-marshalable Go value.
func decodeJSONSchemaDoc(raw json.RawMessage) (bedrockdoc.Interface, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty schema")
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return bedrockdoc.NewLazyDocument(v), nil
}

// decodeToolArgs turns a galdor ToolCall.Arguments into a bedrockruntime
// document. Empty arguments encode as an empty JSON object so the SDK
// has something to serialize.
func decodeToolArgs(raw json.RawMessage) (bedrockdoc.Interface, error) {
	if len(raw) == 0 {
		return bedrockdoc.NewLazyDocument(map[string]any{}), nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("%w: tool arguments are not valid JSON: %w", provider.ErrInvalidRequest, err)
	}
	return bedrockdoc.NewLazyDocument(v), nil
}

// responseFromConverse collapses a non-streaming ConverseOutput into a
// galdor provider.Response.
func responseFromConverse(out *bedrockruntime.ConverseOutput, raw []byte) *provider.Response {
	msg := schema.Message{Role: schema.RoleAssistant}
	stopReason := normalizeStopReason(string(out.StopReason))

	if outMsg, ok := out.Output.(*brtypes.ConverseOutputMemberMessage); ok {
		for _, b := range outMsg.Value.Content {
			switch v := b.(type) {
			case *brtypes.ContentBlockMemberText:
				if v.Value != "" {
					msg.Content = append(msg.Content, schema.TextPart(v.Value))
				}
			case *brtypes.ContentBlockMemberToolUse:
				args, _ := encodeToolInput(v.Value.Input)
				msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
					ID:        aws.ToString(v.Value.ToolUseId),
					Name:      aws.ToString(v.Value.Name),
					Arguments: args,
				})
			}
		}
	}

	usage := schema.Usage{}
	if out.Usage != nil {
		usage.InputTokens = int(aws.ToInt32(out.Usage.InputTokens))
		usage.OutputTokens = int(aws.ToInt32(out.Usage.OutputTokens))
		if out.Usage.CacheReadInputTokens != nil {
			usage.CacheReadTokens = int(*out.Usage.CacheReadInputTokens)
		}
		if out.Usage.CacheWriteInputTokens != nil {
			usage.CacheCreationTokens = int(*out.Usage.CacheWriteInputTokens)
		}
	}

	resp := &provider.Response{
		Message:     msg,
		StopReason:  stopReason,
		Usage:       usage,
		ProviderRaw: raw,
	}
	return resp
}

func encodeToolInput(doc bedrockdoc.Interface) (json.RawMessage, error) {
	if doc == nil {
		return json.RawMessage("{}"), nil
	}
	// The bedrockruntime document type wraps either a Go value (set by
	// the SDK on decode) or a raw payload. The Smithy document interface
	// has UnmarshalSmithyDocument which writes the bytes; here we just
	// JSON-marshal whatever the SDK produced.
	var v any
	if err := doc.UnmarshalSmithyDocument(&v); err != nil {
		return json.RawMessage("{}"), err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("{}"), err
	}
	return b, nil
}

func normalizeStopReason(s string) schema.StopReason {
	switch strings.ToLower(s) {
	case "end_turn":
		return schema.StopReasonEndTurn
	case "max_tokens":
		return schema.StopReasonMaxTokens
	case "tool_use":
		return schema.StopReasonToolUse
	case "stop_sequence":
		return schema.StopReasonStopSequence
	case "guardrail_intervened", "content_filtered":
		return schema.StopReasonRefusal
	case "":
		return ""
	default:
		return schema.StopReason(strings.ToLower(s))
	}
}
