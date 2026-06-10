package schema

// ContentType discriminates the variants of ContentPart.
type ContentType string

const (
	// ContentTypeText is a UTF-8 text fragment.
	ContentTypeText ContentType = "text"

	// ContentTypeImage is an image, either inline (Data + Media) or by URL.
	ContentTypeImage ContentType = "image"

	// ContentTypeThinking is a model reasoning / chain-of-thought
	// fragment. It is kept separate from ContentTypeText so that
	// Message.Text() — and every downstream consumer that reads it —
	// ignores reasoning by default, while observability and UIs can
	// surface it deliberately. Providers populate it from inline
	// <think> markers (via provider.ExtractThinkingBlocks) or from
	// native reasoning blocks (Gemini thoughts, Anthropic extended
	// thinking, etc.).
	ContentTypeThinking ContentType = "thinking"

	// ContentTypeRedactedThinking is an ENCRYPTED reasoning block a provider
	// returns when its safety systems redact the chain-of-thought (Anthropic
	// "redacted_thinking"). The plaintext is unavailable; the opaque blob is
	// carried in Signature and must be echoed back verbatim on a follow-up
	// turn so a Reasoning+tools loop can continue. Like ContentTypeThinking,
	// it is ignored by Message.Text().
	ContentTypeRedactedThinking ContentType = "redacted_thinking"
)

// ContentPart is a single fragment of a Message's body. A Message can carry
// multiple parts to support multimodal inputs (text + image, etc.). The
// active variant is determined by Type; only the fields relevant to that
// variant are populated.
type ContentPart struct {
	Type ContentType `json:"type"`

	// Text is populated when Type == ContentTypeText.
	Text string `json:"text,omitempty"`

	// Image is populated when Type == ContentTypeImage.
	Image *ImageContent `json:"image,omitempty"`

	// Signature is an opaque provider token some models attach to a
	// reasoning block (e.g. Anthropic extended thinking) and require
	// echoed back verbatim on a follow-up turn. Populated only on
	// ContentTypeThinking parts; empty otherwise.
	Signature string `json:"signature,omitempty"`
}

// ImageContent describes an image attachment. Provide either URL or Data;
// when Data is used, Media must hold a valid MIME type (e.g. "image/png").
type ImageContent struct {
	URL   string `json:"url,omitempty"`
	Data  []byte `json:"data,omitempty"`
	Media string `json:"media,omitempty"`
}

// TextPart returns a ContentPart holding the given text.
func TextPart(text string) ContentPart {
	return ContentPart{Type: ContentTypeText, Text: text}
}

// ThinkingPart returns a ContentPart holding model reasoning text.
// Message.Text() skips it, so adding one never changes the textual
// answer seen by downstream consumers.
func ThinkingPart(text string) ContentPart {
	return ContentPart{Type: ContentTypeThinking, Text: text}
}

// ImagePartURL returns a ContentPart referencing an image by URL.
func ImagePartURL(url string) ContentPart {
	return ContentPart{Type: ContentTypeImage, Image: &ImageContent{URL: url}}
}

// ImagePartData returns a ContentPart holding inline image bytes with the
// given MIME type.
func ImagePartData(data []byte, media string) ContentPart {
	return ContentPart{Type: ContentTypeImage, Image: &ImageContent{Data: data, Media: media}}
}
