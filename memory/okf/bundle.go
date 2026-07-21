package okf

import (
	"io/fs"
	"maps"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"

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
	// MetaSection labels a chunk carved from a conventional OKF body
	// section (§4.2, §8): "schema", "examples" or "citations". Only set on
	// per-section chunks (i.e. when a concept is large enough to split by
	// heading); filter on it with Query.Filter[MetaSection].
	MetaSection = "section"
	// MetaExtraPrefix prefixes producer-defined frontmatter keys the spec
	// doesn't standardize (§4.1 Extensions): a concept with `env: prod`
	// carries Metadata["fm.env"] = "prod". List values keep the inline
	// `[a, b]` form. The prefix keeps producer keys from colliding with
	// this package's own metadata keys, and Marshal writes them back so
	// round-tripping preserves unknown keys, as §4.1 requires of consumers.
	MetaExtraPrefix = "fm."
)

// standardFrontmatterKeys are the §4.1 fields with dedicated metadata keys;
// everything else in a concept's frontmatter is a producer extension.
var standardFrontmatterKeys = map[string]bool{
	MetaType: true, MetaTitle: true, MetaDesc: true,
	MetaTags: true, MetaResource: true, MetaTimestamp: true,
}

// chunkCharThreshold: concept bodies longer than this get split by their
// top-level `#` headings so deep sections stay independently retrievable.
// Matches the OKF reference engine's default.
const chunkCharThreshold = 1200

var (
	linkRe     = regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)
	headingRe  = regexp.MustCompile(`(?m)^#\s+(.+)$`)
	nonSlugRe  = regexp.MustCompile(`[^a-z0-9]+`)
	externalRe = regexp.MustCompile(`^(?:https?://|mailto:)`)
)

// Load reads an OKF bundle from a directory on disk. It returns the
// concepts as memory.Documents (one per non-reserved .md file) plus any
// non-fatal warnings (missing recommended fields, broken links). Only an
// unreadable bundle root is a hard error.
//
// Load reads only the concepts. Use LoadBundle when you also need the
// spec's navigational material — index.md progressive disclosure, log.md
// history, the declared okf_version and the link graph.
func Load(root string) ([]memory.Document, []string, error) {
	return LoadFS(os.DirFS(root), ".")
}

// LoadFS is Load over an arbitrary fs.FS, so a bundle can be embedded
// (go:embed) or served from any filesystem. root is the path within fsys
// to treat as the bundle root ("." for the FS root).
func LoadFS(fsys fs.FS, root string) ([]memory.Document, []string, error) {
	w, err := walkBundle(fsys, root)
	if err != nil {
		return nil, nil, err
	}
	docs, warnings, _ := buildConcepts(fsys, root, w.concepts)
	return docs, warnings, nil
}

// bundleWalk holds the classified markdown paths of a bundle: concept
// documents versus the reserved index.md / log.md files.
type bundleWalk struct {
	concepts []string
	indexes  []string
	logs     []string
}

// walkBundle walks the bundle tree once and classifies every .md file by
// its role. Concept files feed retrieval; index.md and log.md are reserved
// (§3.1) and handled separately by LoadBundle.
func walkBundle(fsys fs.FS, root string) (bundleWalk, error) {
	var w bundleWalk
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		switch path.Base(p) {
		case "index.md":
			w.indexes = append(w.indexes, p)
		case "log.md":
			w.logs = append(w.logs, p)
		default:
			w.concepts = append(w.concepts, p)
		}
		return nil
	})
	if err != nil {
		return bundleWalk{}, err
	}
	sort.Strings(w.concepts)
	sort.Strings(w.indexes)
	sort.Strings(w.logs)
	return w, nil
}

// buildConcepts reads and parses the concept files into memory.Documents.
// It also returns the ids of concepts lacking a frontmatter block (a §9
// rule-1 conformance fact Validate reports). mdPaths must already be
// sorted for a deterministic document order.
func buildConcepts(fsys fs.FS, root string, mdPaths []string) ([]memory.Document, []string, []string) {
	type raw struct {
		conceptID string
		docDir    string
		fm        map[string]any
		body      string
	}
	var raws []raw
	idSet := make(map[string]bool)
	var warnings []string
	var noFM []string

	for _, p := range mdPaths {
		data, readErr := fs.ReadFile(fsys, p)
		if readErr != nil {
			warnings = append(warnings, p+": "+readErr.Error())
			continue
		}
		rel := relTo(root, p)
		conceptID := strings.TrimSuffix(rel, ".md")
		fmText, body, present := splitFrontmatter(string(data))
		if !present {
			noFM = append(noFM, conceptID)
		}
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
		// Producer-defined keys (§4.1 Extensions) ride along under the
		// fm. prefix so round-tripping through Marshal preserves them.
		for key, val := range r.fm {
			if standardFrontmatterKeys[key] {
				continue
			}
			meta[MetaExtraPrefix+key] = encodeExtra(val)
		}
		docs = append(docs, memory.Document{
			ID:       r.conceptID,
			Source:   r.conceptID + ".md",
			Text:     r.body,
			Metadata: meta,
		})
	}
	return docs, warnings, noFM
}

// encodeExtra flattens a frontmatter value (string or list) into the
// string-keyed metadata contract. Lists keep the YAML inline form so
// Marshal can write them back verbatim and they reparse as lists.
func encodeExtra(v any) string {
	switch t := v.(type) {
	case []string:
		return "[" + strings.Join(t, ", ") + "]"
	case string:
		return t
	default:
		return ""
	}
}

// Bundle is a fully-parsed OKF bundle: the concepts plus the navigational
// and historical material the spec layers on top of them — per-directory
// index.md progressive disclosure (§6), log.md change history (§7) and the
// declared okf_version (§11). Load/Open remain the concept-only entry
// points; LoadBundle is the whole picture, and its methods (in graph.go /
// nav.go) expose the link graph and directory hierarchy.
type Bundle struct {
	// Version is the okf_version declared in the root index.md (§11), or
	// "" when the bundle declares none. Best-effort, never fatal.
	Version string

	// Concepts are the retrievable documents, exactly what Load returns.
	Concepts []memory.Document

	// Indexes maps a bundle-relative directory ("" = root) to its parsed
	// index.md. Absent directories have no entry; synthesize one on demand
	// with SynthesizeIndex.
	Indexes map[string]Index

	// Logs maps a bundle-relative directory ("" = root) to its parsed
	// log.md change history.
	Logs map[string]Log

	// Warnings collects every non-fatal issue found while loading:
	// missing types, broken links, missing okf_version, unreadable files.
	Warnings []string

	// Link graph, derived on first use from the concepts' resolved
	// outlinks (see graph.go). outAdj is forward edges, inAdj reverse.
	graphOnce sync.Once
	outAdj    map[string][]string
	inAdj     map[string][]string

	// Concept lookup by id, derived on first use (see nav.go).
	conceptOnce sync.Once
	conceptByID map[string]memory.Document

	// noFrontmatter lists concepts lacking a frontmatter block — a §9
	// rule-1 conformance violation that Validate reports as an error.
	noFrontmatter []string
}

// LoadBundle reads a complete OKF bundle from a directory on disk:
// concepts, index.md / log.md reserved files, and the declared spec
// version. Only an unreadable bundle root is a hard error; everything else
// is surfaced through Bundle.Warnings (permissive consumption, §9).
func LoadBundle(root string) (*Bundle, error) {
	return LoadBundleFS(os.DirFS(root), ".")
}

// LoadBundleFS is LoadBundle over an arbitrary fs.FS (embedded bundles,
// etc.). root is the path within fsys to treat as the bundle root.
func LoadBundleFS(fsys fs.FS, root string) (*Bundle, error) {
	w, err := walkBundle(fsys, root)
	if err != nil {
		return nil, err
	}
	docs, warnings, noFM := buildConcepts(fsys, root, w.concepts)
	b := &Bundle{
		Concepts:      docs,
		Indexes:       make(map[string]Index, len(w.indexes)),
		Logs:          make(map[string]Log, len(w.logs)),
		noFrontmatter: noFM,
	}
	for _, p := range w.indexes {
		idx, warns := parseIndexFile(fsys, root, p)
		b.Indexes[idx.Dir] = idx
		warnings = append(warnings, warns...)
	}
	for _, p := range w.logs {
		lg, warns := parseLogFile(fsys, root, p)
		b.Logs[lg.Dir] = lg
		warnings = append(warnings, warns...)
	}
	// okf_version lives in the root index.md (§11); its absence or an
	// unfamiliar major is a best-effort miss, not an error.
	if rootIdx, ok := b.Indexes[""]; ok {
		b.Version = rootIdx.Version
		switch {
		case rootIdx.Version == "":
			warnings = append(warnings, "index.md: missing okf_version")
		case majorOf(rootIdx.Version) != supportedMajor:
			warnings = append(warnings, "index.md: okf_version "+rootIdx.Version+
				" has an unsupported major (this module targets "+supportedMajor+".x)")
		}
	} else {
		warnings = append(warnings, "no root index.md: okf_version unknown")
	}
	b.Warnings = warnings
	return b, nil
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
			// A conventional section heading (# Schema / # Examples /
			// # Citations) tags the chunk so callers can filter by section.
			// Only then do we copy the metadata; ordinary chunks keep
			// sharing the document's map.
			meta := d.Metadata
			if sc := conventionalSection(sec.heading); sc != "" {
				meta = cloneMeta(d.Metadata)
				meta[MetaSection] = sc
			}
			chunks = append(chunks, memory.Chunk{
				ID:         d.ID + "#" + slug,
				DocumentID: d.ID,
				Index:      idx,
				Text:       header + "\n" + crumb + "\n" + sec.text,
				Metadata:   meta,
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
	h := title + ". " + desc + " tags: " + strings.ReplaceAll(tags, ",", ", ")
	// Fold the resource URI in too, so a query naming the physical resource
	// (a warehouse table, a URL) matches the concept even though the URI
	// lives in frontmatter, not the body.
	if res := d.Metadata[MetaResource]; res != "" {
		h += " resource: " + res
	}
	return h
}

// conventionalSection maps a top-level heading to its OKF conventional
// section label (§4.2, §8), or "" when the heading is not one of them.
func conventionalSection(heading string) string {
	switch strings.ToLower(strings.TrimSpace(heading)) {
	case "schema":
		return "schema"
	case "examples":
		return "examples"
	case "citations":
		return "citations"
	}
	return ""
}

// cloneMeta copies a metadata map so a per-chunk key can be added without
// mutating the map shared by the document and its other chunks.
func cloneMeta(m map[string]string) map[string]string {
	out := make(map[string]string, len(m)+1)
	maps.Copy(out, m)
	return out
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
