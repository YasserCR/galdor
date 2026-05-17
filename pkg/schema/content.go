package schema

// ContentType discriminates the variants of ContentPart.
type ContentType string

const (
	// ContentTypeText is a UTF-8 text fragment.
	ContentTypeText ContentType = "text"

	// ContentTypeImage is an image, either inline (Data + Media) or by URL.
	ContentTypeImage ContentType = "image"
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

// ImagePartURL returns a ContentPart referencing an image by URL.
func ImagePartURL(url string) ContentPart {
	return ContentPart{Type: ContentTypeImage, Image: &ImageContent{URL: url}}
}

// ImagePartData returns a ContentPart holding inline image bytes with the
// given MIME type.
func ImagePartData(data []byte, media string) ContentPart {
	return ContentPart{Type: ContentTypeImage, Image: &ImageContent{Data: data, Media: media}}
}
