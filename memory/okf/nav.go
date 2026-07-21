package okf

import (
	"context"
	"sort"

	"github.com/YasserCR/galdor/pkg/memory"
	"github.com/YasserCR/galdor/pkg/tool"
)

// Directory hierarchy (§3). A concept id like "references/metrics/mrr"
// encodes its place in a tree; these methods expose that tree so a consumer
// can navigate parent/child instead of only querying a flat set. All dir
// arguments are bundle-relative and normalized ("", ".", "/" all mean root).

// Concept returns the loaded document for a concept id, and whether it
// exists in the bundle.
func (b *Bundle) Concept(id string) (memory.Document, bool) {
	b.buildConceptIndex()
	d, ok := b.conceptByID[id]
	return d, ok
}

func (b *Bundle) buildConceptIndex() {
	b.conceptOnce.Do(func() {
		b.conceptByID = make(map[string]memory.Document, len(b.Concepts))
		for _, c := range b.Concepts {
			b.conceptByID[c.Metadata[MetaConceptID]] = c
		}
	})
}

// Children returns the ids of concepts that live directly in dir (not in a
// subdirectory), sorted.
func (b *Bundle) Children(dir string) []string {
	dir = normalizeDir(dir)
	var out []string
	for _, c := range b.Concepts {
		id := c.Metadata[MetaConceptID]
		if dirOfID(id) == dir {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// Dirs returns the immediate subdirectories of dir, sorted. A directory
// exists if any concept, index.md or log.md lives beneath it.
func (b *Bundle) Dirs(dir string) []string {
	base := normalizeDir(dir)
	all := b.allDirs()
	var out []string
	for d := range all {
		if dirOfID(d) == base {
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

// Parent returns the directory that directly contains conceptID
// ("" = root).
func (b *Bundle) Parent(conceptID string) string {
	return dirOfID(conceptID)
}

// allDirs collects every directory in the bundle: each concept's ancestors
// plus any directory that carries an index.md or log.md.
func (b *Bundle) allDirs() map[string]bool {
	dirs := make(map[string]bool)
	add := func(d string) {
		for d != "" {
			dirs[d] = true
			d = dirOfID(d)
		}
	}
	for _, c := range b.Concepts {
		add(dirOfID(c.Metadata[MetaConceptID]))
	}
	for d := range b.Indexes {
		add(d)
	}
	for d := range b.Logs {
		add(d)
	}
	return dirs
}

// BrowseInput is the argument schema for the tool from NewBrowseTool. Dir
// is bundle-relative; empty means the bundle root.
type BrowseInput struct {
	Dir string `json:"dir,omitempty"`
}

// BrowseChild is one concept listed directly under the browsed directory.
type BrowseChild struct {
	ConceptID   string `json:"concept_id"`
	Title       string `json:"title"`
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
}

// BrowseOutput is what NewBrowseTool returns: the directory's index (real
// or synthesized), its subdirectories, and the concepts directly inside it.
type BrowseOutput struct {
	Dir         string `json:"dir"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	// Synthesized is true when no index.md exists for Dir and the index was
	// generated on the fly (§6 progressive disclosure).
	Synthesized bool          `json:"synthesized"`
	Subdirs     []string      `json:"subdirs,omitempty"`
	Concepts    []BrowseChild `json:"concepts,omitempty"`
}

// NewBrowseTool wraps a Bundle as a ReAct-callable navigation tool: given a
// directory, it returns that directory's progressive-disclosure index, its
// subdirectories and its direct concepts. It is the browsing complement to
// NewSearchTool — search finds concepts by text, browse walks the tree.
func NewBrowseTool(b *Bundle) (tool.Tool[BrowseInput, BrowseOutput], error) {
	return tool.NewTool(
		"okf_browse",
		"Browse the Open Knowledge Format bundle's directory tree. Given a "+
			"directory (empty for the bundle root), returns its subdirectories, "+
			"the concepts directly inside it, and the directory's index. Use it "+
			"to explore what a bundle contains before searching.",
		func(ctx context.Context, in BrowseInput) (BrowseOutput, error) {
			dir := normalizeDir(in.Dir)
			idx := b.IndexFor(dir)
			out := BrowseOutput{
				Dir:         dir,
				Title:       idx.Title,
				Description: idx.Description,
				Synthesized: idx.Synthesized,
				Subdirs:     b.Dirs(dir),
			}
			for _, id := range b.Children(dir) {
				child := BrowseChild{ConceptID: id, Title: lastSegment(id)}
				if c, ok := b.Concept(id); ok {
					child.Title = c.Metadata[MetaTitle]
					child.Type = c.Metadata[MetaType]
					child.Description = c.Metadata[MetaDesc]
				}
				out.Concepts = append(out.Concepts, child)
			}
			return out, nil
		},
	)
}
