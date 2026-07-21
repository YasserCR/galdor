package okf

import (
	"regexp"
	"strconv"
	"strings"
)

// Citations (§8). A concept sourcing claims from external material lists
// them under a `# Citations` heading, numbered `[1] [text](url)`. Citation
// links may be absolute URLs, bundle-relative paths, or paths into a
// references/ subdirectory. The section is plain markdown — this parser
// gives consumers the structured view.

// Citation is one entry of a concept's `# Citations` section.
type Citation struct {
	// Number is the citation's [n] index, or 0 when the entry wasn't
	// numbered (tolerated).
	Number int
	// Title is the link text.
	Title string
	// Target is the link destination: an external URL or a bundle path.
	Target string
}

// citationRe matches a citation line: an optional list bullet, an optional
// [n] number, then a markdown link.
var citationRe = regexp.MustCompile(`^(?:[-*]\s+)?(?:\[(\d+)\]\s*)?\[([^\]]*)\]\(([^)]+)\)`)

// Citations returns the parsed `# Citations` section of a concept, in
// order, or nil when the concept has none. Lines in the section that don't
// carry a markdown link are ignored.
func (b *Bundle) Citations(conceptID string) []Citation {
	c, ok := b.Concept(conceptID)
	if !ok {
		return nil
	}
	return ParseCitations(c.Text)
}

// ParseCitations extracts the citations from a concept body: every linked
// line under a top-level `# Citations` heading (§8).
func ParseCitations(body string) []Citation {
	var out []Citation
	for _, sec := range splitByHeading(body) {
		if conventionalSection(sec.heading) != "citations" {
			continue
		}
		for _, line := range strings.Split(sec.text, "\n") {
			m := citationRe.FindStringSubmatch(strings.TrimSpace(line))
			if m == nil {
				continue
			}
			n := 0
			if m[1] != "" {
				n, _ = strconv.Atoi(m[1])
			}
			out = append(out, Citation{Number: n, Title: m[2], Target: m[3]})
		}
	}
	return out
}
