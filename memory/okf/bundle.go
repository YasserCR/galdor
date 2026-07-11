package okf

import (
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/YasserCR/galdor/pkg/memory"
)

// Metadata keys populated on every Document/Chunk loaded from a bundle.
// They mirror the OKF frontmatter fields (plus the derived concept id and
// resolved outlinks). Values are strings; list-valued fields (tags,
// outlinks) are comma-joined so they round-trip through the string-keyed
// memory.Metadata contract.
const (
	MetaType      = "type"
	MetaTitle     = "title"
	MetaDesc      = "description"
	MetaTags      = "tags"      // comma-joined
	MetaResource  = "resource"  // resource:// URI
	MetaTimestamp = "timestamp" // ISO 8601
	MetaConceptID = "concept_id"
	MetaOutlinks  = "outlinks" // comma-joined concept ids
)

// chunkCharThreshold: concept bodies longer than this get split by their
// top-level `#` headings so deep sections stay independently retrievable.
// Matches the OKF reference engine's default.
const chunkCharThreshold = 1200

var (
	reserved   = map[string]bool{"index.md": true, "log.md": true}
	linkRe     = regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)
	headingRe  = regexp.MustCompile(`(?m)^#\s+(.+)$`)
	nonSlugRe  = regexp.MustCompile(`[^a-z0-9]+`)
	externalRe = regexp.MustCompile(`^(?:https?://|mailto:)`)
)

// Load reads an OKF bundle from a directory on disk. It returns the
// concepts as memory.Documents (one per non-reserved .md file) plus any
// non-fatal warnings (missing recommended fields, broken links). Only an
// unreadable bundle root is a hard error.
func Load(root string) ([]memory.Document, []string, error) {
	return LoadFS(os.DirFS(root), ".")
}

// LoadFS is Load over an arbitrary fs.FS, so a bundle can be embedded
// (go:embed) or served from any filesystem. root is the path within fsys
// to treat as the bundle root ("." for the FS root).
func LoadFS(fsys fs.FS, root string) ([]memory.Document, []string, error) {
	var mdPaths []string
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, ".md") && !reserved[path.Base(p)] {
			mdPaths = append(mdPaths, p)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(mdPaths)

	type raw struct {
		conceptID string
		docDir    string
		fm        map[string]any
		body      string
	}
	var raws []raw
	idSet := make(map[string]bool)
	var warnings []string

	for _, p := range mdPaths {
		data, readErr := fs.ReadFile(fsys, p)
		if readErr != nil {
			warnings = append(warnings, p+": "+readErr.Error())
			continue
		}
		rel := relTo(root, p)
		conceptID := strings.TrimSuffix(rel, ".md")
		fmText, body := splitFrontmatter(string(data))
		fm := parseFrontmatter(fmText)
		if asString(fm, MetaType) == "" {
			warnings = append(warnings, conceptID+": missing required frontmatter field: type")
		}
		raws = append(raws, raw{conceptID: conceptID, docDir: path.Dir(rel), fm: fm, body: body})
		idSet[conceptID] = true
	}

	docs := make([]memory.Document, 0, len(raws))
	for _, r := range raws {
		outlinks, broken := resolveLinks(r.body, r.docDir, idSet, r.conceptID)
		for _, b := range broken {
			warnings = append(warnings, r.conceptID+": broken link -> "+b)
		}
		tags := asList(r.fm, MetaTags)
		title := asString(r.fm, MetaTitle)
		if title == "" {
			title = lastSegment(r.conceptID)
		}
		meta := map[string]string{
			MetaType:      asString(r.fm, MetaType),
			MetaTitle:     title,
			MetaDesc:      asString(r.fm, MetaDesc),
			MetaTags:      strings.Join(tags, ","),
			MetaResource:  asString(r.fm, MetaResource),
			MetaTimestamp: asString(r.fm, MetaTimestamp),
			MetaConceptID: r.conceptID,
			MetaOutlinks:  strings.Join(outlinks, ","),
		}
		docs = append(docs, memory.Document{
			ID:       r.conceptID,
			Source:   r.conceptID + ".md",
			Text:     r.body,
			Metadata: meta,
		})
	}
	return docs, warnings, nil
}

// ChunkConcepts turns loaded documents into retrieval chunks, concept-first:
// one chunk per concept, unless the body is large, in which case it is split
// by top-level `#` headings. Every chunk's indexed Text is prefixed with the
// concept's title, description and tags so a lexical (BM25) index matches on
// those fields even though the store only indexes the chunk body.
func ChunkConcepts(docs []memory.Document) []memory.Chunk {
	var chunks []memory.Chunk
	for _, d := range docs {
		header := chunkHeader(d)
		if len(d.Text) <= chunkCharThreshold {
			chunks = append(chunks, memory.Chunk{
				ID:         d.ID,
				DocumentID: d.ID,
				Index:      0,
				Text:       header + "\n" + d.Text,
				Metadata:   d.Metadata,
			})
			continue
		}
		idx := 0
		for _, sec := range splitByHeading(d.Text) {
			if strings.TrimSpace(sec.text) == "" {
				continue
			}
			slug := nonSlugRe.ReplaceAllString(strings.ToLower(sec.heading), "-")
			slug = strings.Trim(slug, "-")
			if slug == "" {
				slug = "body"
			}
			crumb := d.ID
			if sec.heading != "" {
				crumb = d.ID + " > " + sec.heading
			}
			chunks = append(chunks, memory.Chunk{
				ID:         d.ID + "#" + slug,
				DocumentID: d.ID,
				Index:      idx,
				Text:       header + "\n" + crumb + "\n" + sec.text,
				Metadata:   d.Metadata,
			})
			idx++
		}
	}
	return chunks
}

func chunkHeader(d memory.Document) string {
	title := d.Metadata[MetaTitle]
	desc := d.Metadata[MetaDesc]
	tags := d.Metadata[MetaTags]
	return title + ". " + desc + " tags: " + strings.ReplaceAll(tags, ",", ", ")
}

type section struct {
	heading string
	text    string
}

func splitByHeading(body string) []section {
	locs := headingRe.FindAllStringSubmatchIndex(body, -1)
	if len(locs) == 0 {
		return []section{{heading: "", text: body}}
	}
	var out []section
	if locs[0][0] > 0 {
		if pre := strings.TrimSpace(body[:locs[0][0]]); pre != "" {
			out = append(out, section{heading: "", text: pre})
		}
	}
	for i, loc := range locs {
		heading := strings.TrimSpace(body[loc[2]:loc[3]])
		start := loc[1]
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		out = append(out, section{heading: heading, text: strings.TrimSpace(body[start:end])})
	}
	return out
}

// resolveLinks extracts markdown links from a body and resolves each to a
// concept id in the bundle. Returns (resolved outlinks, broken targets).
func resolveLinks(body, docDir string, idSet map[string]bool, self string) (outlinks, broken []string) {
	seen := make(map[string]bool)
	for _, m := range linkRe.FindAllStringSubmatch(body, -1) {
		target := resolveLink(m[1], docDir)
		if target == "" {
			continue
		}
		if idSet[target] {
			if target != self && !seen[target] {
				seen[target] = true
				outlinks = append(outlinks, target)
			}
		} else {
			broken = append(broken, target)
		}
	}
	return outlinks, broken
}

// resolveLink resolves a single markdown link target to a concept id, or
// "" if it is external or escapes the bundle. Supports both bundle-absolute
// (`/tables/x.md`) and document-relative (`../x.md`) forms.
func resolveLink(target, docDir string) string {
	t := target
	if i := strings.IndexByte(t, '#'); i >= 0 {
		t = t[:i]
	}
	t = strings.TrimSpace(t)
	if t == "" || externalRe.MatchString(t) {
		return ""
	}
	var abs string
	if strings.HasPrefix(t, "/") {
		abs = path.Clean(strings.TrimPrefix(t, "/"))
	} else {
		abs = path.Clean(path.Join(docDir, t))
	}
	if abs == ".." || strings.HasPrefix(abs, "../") {
		return ""
	}
	return strings.TrimSuffix(abs, ".md")
}

// relTo returns p relative to root, using forward slashes. When root is
// "." the path is returned unchanged.
func relTo(root, p string) string {
	if root == "." || root == "" {
		return p
	}
	rel := strings.TrimPrefix(p, root)
	return strings.TrimPrefix(rel, "/")
}

func lastSegment(id string) string {
	if i := strings.LastIndexByte(id, '/'); i >= 0 {
		return id[i+1:]
	}
	return id
}
