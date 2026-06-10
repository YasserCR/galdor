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

// Regression (audit low): an open <think> tag with attributes whose '>' lands
// in a LATER delta must be fully held back, not leaked. The 16-byte tail cap
// used to emit the opening bytes of any tag longer than the cap.
func TestStripThinkingStream_OpenTagWithAttributesSplitAcrossDeltas(t *testing.T) {
	s := &stripThinkingStream{}
	var got strings.Builder
	// The open tag "<think data-reasoning=\"chain-of-thought\">" is 41 bytes
	// and is split mid-attribute across two deltas.
	for _, chunk := range []string{
		"answer ", `<think data-reasoning="chain`, `-of-thought">secret`, "</think> done",
	} {
		got.WriteString(s.feed(chunk))
	}
	out := got.String()
	if strings.Contains(out, "<think") || strings.Contains(out, "secret") {
		t.Fatalf("leaked an attribute-bearing open tag split across deltas: got %q", out)
	}
	if strings.TrimSpace(out) != "answer  done" && strings.TrimSpace(out) != "answer done" {
		t.Fatalf("unexpected stripped output: %q", out)
	}
}
