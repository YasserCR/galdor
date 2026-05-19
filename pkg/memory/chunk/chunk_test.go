package chunk_test

import (
	"strings"
	"testing"

	"github.com/YasserCR/galdor/pkg/memory"
	"github.com/YasserCR/galdor/pkg/memory/chunk"
)

func TestFixedSize_NoOverlap(t *testing.T) {
	t.Parallel()
	doc := memory.Document{ID: "d", Text: strings.Repeat("a", 25)}
	out, err := chunk.FixedSize{Size: 10}.Chunk(doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("got %d chunks, want 3", len(out))
	}
	if out[0].DocumentID != "d" || out[0].Index != 0 {
		t.Errorf("first chunk = %+v", out[0])
	}
	if len(out[0].Text) != 10 || len(out[2].Text) != 5 {
		t.Errorf("sizes = [%d, %d, %d]", len(out[0].Text), len(out[1].Text), len(out[2].Text))
	}
}

func TestFixedSize_WithOverlap(t *testing.T) {
	t.Parallel()
	doc := memory.Document{Text: "0123456789ABCDEFGHIJ"}
	out, err := chunk.FixedSize{Size: 8, Overlap: 3}.Chunk(doc)
	if err != nil {
		t.Fatal(err)
	}
	// step = 5; expect chunks starting at 0, 5, 10, 15
	if len(out) != 4 {
		t.Fatalf("got %d chunks: %+v", len(out), out)
	}
	if out[0].Text != "01234567" {
		t.Errorf("c0 = %q", out[0].Text)
	}
	if out[1].Text != "56789ABC" {
		t.Errorf("c1 = %q (expected overlap with c0)", out[1].Text)
	}
}

func TestFixedSize_RejectsBadParams(t *testing.T) {
	t.Parallel()
	if _, err := (chunk.FixedSize{Size: 0}).Chunk(memory.Document{Text: "x"}); err == nil {
		t.Error("expected error for Size=0")
	}
	if _, err := (chunk.FixedSize{Size: 5, Overlap: 5}).Chunk(memory.Document{Text: "x"}); err == nil {
		t.Error("expected error for Overlap == Size")
	}
}

func TestFixedSize_PreservesMetadata(t *testing.T) {
	t.Parallel()
	doc := memory.Document{
		ID:       "d",
		Text:     strings.Repeat("a", 15),
		Metadata: map[string]string{"source": "tests"},
	}
	out, _ := chunk.FixedSize{Size: 10}.Chunk(doc)
	if out[0].Metadata["source"] != "tests" {
		t.Errorf("metadata not carried: %+v", out[0].Metadata)
	}
	// Mutating the chunk's metadata must not affect the document's.
	out[0].Metadata["source"] = "mutated"
	if doc.Metadata["source"] != "tests" {
		t.Error("metadata aliased between doc and chunk")
	}
}

func TestRecursive_PrefersParagraphBoundaries(t *testing.T) {
	t.Parallel()
	text := "First paragraph about Quito.\n\nSecond paragraph about Bogotá.\n\nThird paragraph about Lima."
	out, err := chunk.Recursive{Size: 40}.Chunk(memory.Document{Text: text})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("no chunks")
	}
	// Each chunk should contain at least one of the three sentinel terms.
	for i, c := range out {
		if !(strings.Contains(c.Text, "Quito") || strings.Contains(c.Text, "Bogotá") || strings.Contains(c.Text, "Lima")) {
			t.Errorf("chunk %d does not contain any sentinel term: %q", i, c.Text)
		}
	}
}

func TestRecursive_FallsBackOnLongWord(t *testing.T) {
	t.Parallel()
	// One ultra-long token forces the chunker to drop to the
	// character-level fallback.
	long := strings.Repeat("x", 100)
	out, err := chunk.Recursive{Size: 25}.Chunk(memory.Document{Text: long})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 4 {
		t.Fatalf("expected at least 4 chunks, got %d", len(out))
	}
	for _, c := range out {
		if len([]rune(c.Text)) > 25 {
			t.Errorf("chunk exceeds Size: %q (%d runes)", c.Text, len([]rune(c.Text)))
		}
	}
}

func TestSentence_PacksSentences(t *testing.T) {
	t.Parallel()
	text := "First sentence. Second sentence here. Third one too. Fourth and final."
	out, err := chunk.Sentence{Size: 40}.Chunk(memory.Document{Text: text})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(out))
	}
	for _, c := range out {
		if !strings.HasSuffix(strings.TrimSpace(c.Text), ".") {
			t.Errorf("chunk ends mid-sentence: %q", c.Text)
		}
	}
}

func TestSentence_EmptyInput(t *testing.T) {
	t.Parallel()
	out, err := chunk.Sentence{Size: 10}.Chunk(memory.Document{Text: ""})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("expected 0 chunks for empty doc, got %d", len(out))
	}
}
