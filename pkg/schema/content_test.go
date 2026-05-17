package schema

import (
	"bytes"
	"testing"
)

func TestTextPart(t *testing.T) {
	t.Parallel()
	p := TextPart("hello")
	if p.Type != ContentTypeText {
		t.Errorf("Type = %q, want %q", p.Type, ContentTypeText)
	}
	if p.Text != "hello" {
		t.Errorf("Text = %q", p.Text)
	}
	if p.Image != nil {
		t.Errorf("Image must be nil for a text part")
	}
}

func TestImagePartURL(t *testing.T) {
	t.Parallel()
	p := ImagePartURL("https://example.com/x.png")
	if p.Type != ContentTypeImage {
		t.Errorf("Type = %q", p.Type)
	}
	if p.Image == nil || p.Image.URL != "https://example.com/x.png" {
		t.Errorf("Image URL not preserved: %+v", p.Image)
	}
}

func TestImagePartData(t *testing.T) {
	t.Parallel()
	data := []byte{0x89, 0x50, 0x4e, 0x47}
	p := ImagePartData(data, "image/png")
	if p.Image == nil {
		t.Fatal("Image nil")
	}
	if !bytes.Equal(p.Image.Data, data) {
		t.Errorf("Data not preserved")
	}
	if p.Image.Media != "image/png" {
		t.Errorf("Media = %q", p.Image.Media)
	}
}
