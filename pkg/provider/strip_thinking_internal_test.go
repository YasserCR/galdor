package provider

import (
	"strings"
	"testing"
)

// Regression for audit H5: the streaming strip must not delete the
// whitespace of legitimate text that precedes a <think> block. The old
// no-close exit ran TrimLeftFunc over `out`, which holds the pre-tag
// text, so "Hello <think>x</think>world" streamed as "Helloworld".
func TestStripThinkingStream_PreservesWhitespaceAroundBlock(t *testing.T) {
	s := &stripThinkingStream{}
	var got strings.Builder
	for _, chunk := range []string{"Hello", " <think>x", "</think>world"} {
		got.WriteString(s.feed(chunk))
	}
	if got.String() != "Hello world" {
		t.Fatalf("streaming strip corrupted whitespace (regression of H5): got %q, want %q", got.String(), "Hello world")
	}
}
