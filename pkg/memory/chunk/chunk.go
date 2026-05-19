package chunk

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/YasserCR/galdor/pkg/memory"
)

// Chunker splits a Document into Chunks. Implementations are
// stateless and safe to share across goroutines.
type Chunker interface {
	Chunk(doc memory.Document) ([]memory.Chunk, error)
}

// FixedSize splits Text into chunks of at most Size runes, with
// Overlap runes carrying over from one chunk to the next. Overlap
// must be < Size.
type FixedSize struct {
	Size    int
	Overlap int
}

// Chunk implements Chunker for FixedSize.
func (f FixedSize) Chunk(doc memory.Document) ([]memory.Chunk, error) {
	if f.Size <= 0 {
		return nil, errors.New("chunk: FixedSize.Size must be > 0")
	}
	if f.Overlap < 0 || f.Overlap >= f.Size {
		return nil, fmt.Errorf("chunk: FixedSize.Overlap must be in [0, Size); got %d (size=%d)", f.Overlap, f.Size)
	}
	runes := []rune(doc.Text)
	if len(runes) == 0 {
		return nil, nil
	}
	step := f.Size - f.Overlap
	out := make([]memory.Chunk, 0, (len(runes)+step-1)/step)
	idx := 0
	for start := 0; start < len(runes); start += step {
		end := start + f.Size
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, memory.Chunk{
			DocumentID: doc.ID,
			Index:      idx,
			Text:       string(runes[start:end]),
			Metadata:   copyMetadata(doc.Metadata),
		})
		idx++
		if end == len(runes) {
			break
		}
	}
	return out, nil
}

// Recursive splits Text by trying each Separator in order; if a
// resulting segment still exceeds Size, the next separator is
// applied to it. The last separator should be "" (empty string),
// which falls back to a FixedSize split at the rune level.
type Recursive struct {
	Size       int
	Overlap    int
	Separators []string
}

// DefaultSeparators is a reasonable list for prose: paragraphs first,
// then sentences (rough English punctuation), then words, then chars.
var DefaultSeparators = []string{"\n\n", "\n", ". ", "? ", "! ", " ", ""}

// Chunk implements Chunker for Recursive.
func (r Recursive) Chunk(doc memory.Document) ([]memory.Chunk, error) {
	if r.Size <= 0 {
		return nil, errors.New("chunk: Recursive.Size must be > 0")
	}
	if r.Overlap < 0 || r.Overlap >= r.Size {
		return nil, fmt.Errorf("chunk: Recursive.Overlap must be in [0, Size); got %d (size=%d)", r.Overlap, r.Size)
	}
	seps := r.Separators
	if len(seps) == 0 {
		seps = DefaultSeparators
	}
	if doc.Text == "" {
		return nil, nil
	}
	pieces := recursiveSplit(doc.Text, r.Size, seps)
	merged := mergePieces(pieces, r.Size, r.Overlap)
	out := make([]memory.Chunk, 0, len(merged))
	for i, m := range merged {
		out = append(out, memory.Chunk{
			DocumentID: doc.ID,
			Index:      i,
			Text:       m,
			Metadata:   copyMetadata(doc.Metadata),
		})
	}
	return out, nil
}

func recursiveSplit(text string, size int, seps []string) []string {
	if runeLen(text) <= size {
		return []string{text}
	}
	if len(seps) == 0 {
		return forceSplit(text, size)
	}
	sep := seps[0]
	rest := seps[1:]
	var parts []string
	if sep == "" {
		parts = forceSplit(text, size)
	} else {
		raw := strings.Split(text, sep)
		// Re-attach the separator to keep semantic boundaries readable.
		for i, p := range raw {
			if i < len(raw)-1 {
				p += sep
			}
			parts = append(parts, p)
		}
	}
	var out []string
	for _, p := range parts {
		if runeLen(p) <= size {
			out = append(out, p)
			continue
		}
		out = append(out, recursiveSplit(p, size, rest)...)
	}
	return out
}

// mergePieces concatenates adjacent pieces into chunks up to size,
// inserting overlap by carrying the tail of the previous chunk into
// the head of the next.
func mergePieces(pieces []string, size, overlap int) []string {
	var out []string
	var cur strings.Builder
	curLen := 0
	for _, p := range pieces {
		pLen := runeLen(p)
		if curLen+pLen > size && curLen > 0 {
			out = append(out, cur.String())
			cur.Reset()
			curLen = 0
			if overlap > 0 {
				tail := tailRunes(out[len(out)-1], overlap)
				cur.WriteString(tail)
				curLen = runeLen(tail)
			}
		}
		cur.WriteString(p)
		curLen += pLen
	}
	if curLen > 0 {
		out = append(out, cur.String())
	}
	return out
}

func forceSplit(text string, size int) []string {
	runes := []rune(text)
	var out []string
	for i := 0; i < len(runes); i += size {
		end := i + size
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[i:end]))
	}
	return out
}

func tailRunes(s string, n int) string {
	r := []rune(s)
	if n >= len(r) {
		return s
	}
	return string(r[len(r)-n:])
}

func runeLen(s string) int {
	return len([]rune(s))
}

// Sentence packs whole sentences into chunks of at most Size runes.
// Sentence boundaries are detected via Unicode terminal punctuation
// ('.', '!', '?') followed by whitespace or end-of-text. A sentence
// that exceeds Size by itself is emitted as its own chunk (it is not
// split further; that's the caller's call to make via Recursive).
type Sentence struct {
	Size int
}

// Chunk implements Chunker for Sentence.
func (s Sentence) Chunk(doc memory.Document) ([]memory.Chunk, error) {
	if s.Size <= 0 {
		return nil, errors.New("chunk: Sentence.Size must be > 0")
	}
	if doc.Text == "" {
		return nil, nil
	}
	sentences := splitSentences(doc.Text)
	out := make([]memory.Chunk, 0)
	var cur strings.Builder
	curLen := 0
	emit := func() {
		if curLen == 0 {
			return
		}
		out = append(out, memory.Chunk{
			DocumentID: doc.ID,
			Index:      len(out),
			Text:       strings.TrimSpace(cur.String()),
			Metadata:   copyMetadata(doc.Metadata),
		})
		cur.Reset()
		curLen = 0
	}
	for _, sent := range sentences {
		l := runeLen(sent)
		if curLen+l > s.Size && curLen > 0 {
			emit()
		}
		cur.WriteString(sent)
		curLen += l
	}
	emit()
	return out, nil
}

func splitSentences(text string) []string {
	var out []string
	var cur strings.Builder
	runes := []rune(text)
	for i, r := range runes {
		cur.WriteRune(r)
		if r == '.' || r == '!' || r == '?' {
			next := rune(0)
			if i+1 < len(runes) {
				next = runes[i+1]
			}
			if next == 0 || unicode.IsSpace(next) {
				out = append(out, cur.String())
				cur.Reset()
			}
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func copyMetadata(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
