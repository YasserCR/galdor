package okf

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/YasserCR/galdor/pkg/memory"
)

// Producer side: rendering OKF back to disk. Marshal is the inverse of the
// loader for one concept; WriteBundle writes a whole Bundle (concepts plus
// the index.md / log.md reserved files). Together with Load/LoadBundle this
// makes memory/okf a read-and-write OKF implementation, not just a reader.
//
// Round-trip fidelity covers the standard frontmatter fields (type, title,
// description, tags, resource, timestamp), producer-defined extension keys
// (§4.1 — carried under MetaExtraPrefix and written back, as the spec asks
// of round-tripping consumers) and the body verbatim.

// Marshal renders a concept document to its OKF markdown form: a YAML
// frontmatter block followed by the body. It is the inverse of the loader:
// standard fields first, then any producer-defined extension keys, sorted
// for determinism.
func Marshal(doc memory.Document) []byte {
	var b strings.Builder
	b.WriteString("---\n")
	writeScalar(&b, "type", doc.Metadata[MetaType])
	writeScalar(&b, "title", doc.Metadata[MetaTitle])
	writeScalar(&b, "description", doc.Metadata[MetaDesc])
	if tags := splitCSV(doc.Metadata[MetaTags]); len(tags) > 0 {
		b.WriteString("tags: [")
		b.WriteString(strings.Join(tags, ", "))
		b.WriteString("]\n")
	}
	writeScalar(&b, "resource", doc.Metadata[MetaResource])
	writeScalar(&b, "timestamp", doc.Metadata[MetaTimestamp])
	for _, key := range extraKeys(doc.Metadata) {
		val := doc.Metadata[MetaExtraPrefix+key]
		if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
			// Inline list form: written verbatim, reparses as a list.
			b.WriteString(key)
			b.WriteString(": ")
			b.WriteString(val)
			b.WriteString("\n")
			continue
		}
		writeScalar(&b, key, val)
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimRight(doc.Text, "\n"))
	b.WriteString("\n")
	return []byte(b.String())
}

// extraKeys returns the producer-defined frontmatter keys carried in a
// metadata map (without their MetaExtraPrefix), sorted.
func extraKeys(meta map[string]string) []string {
	var keys []string
	for k := range meta {
		if rest, ok := strings.CutPrefix(k, MetaExtraPrefix); ok {
			keys = append(keys, rest)
		}
	}
	sort.Strings(keys)
	return keys
}

// WriteBundle writes an entire bundle under root as OKF markdown: one file
// per concept (at concept_id + ".md", creating directories as needed), plus
// each directory's index.md and log.md when the bundle carries them.
// Synthesized indexes are not written (they hold no on-disk state). It is
// the inverse of LoadBundle for the standard fields.
func WriteBundle(root string, b *Bundle) error {
	for _, doc := range b.Concepts {
		id := doc.Metadata[MetaConceptID]
		if id == "" {
			id = doc.ID
		}
		if err := writeFile(root, id+".md", Marshal(doc)); err != nil {
			return err
		}
	}
	for dir, idx := range b.Indexes {
		if err := writeFile(root, path.Join(dir, "index.md"), marshalIndex(idx)); err != nil {
			return err
		}
	}
	for dir, lg := range b.Logs {
		if err := writeFile(root, path.Join(dir, "log.md"), marshalLog(lg)); err != nil {
			return err
		}
	}
	return nil
}

// writeFile writes data to root/rel (rel uses forward slashes), creating
// parent directories. World-readable permissions are correct here: an OKF
// bundle is shareable knowledge meant to be committed to git and
// distributed (§3), not a secret.
func writeFile(root, rel string, data []byte) error {
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil { // #nosec G301 -- bundle dirs must be traversable; OKF bundles are git-distributed knowledge, not secrets
		return err
	}
	return os.WriteFile(p, data, 0o644) // #nosec G306 -- 0644 is correct; OKF concepts are git-committed shareable markdown, not secrets
}

// marshalIndex renders an index.md. Frontmatter is written only for the
// bundle root — the only place the spec permits it (§11) — carrying
// okf_version plus any title/description the loaded file really had.
// Non-root indexes are body-only, per §6.
func marshalIndex(idx Index) []byte {
	var b strings.Builder
	isRoot := idx.Dir == ""
	if isRoot && (idx.Version != "" || idx.fmTitle != "" || idx.fmDesc != "") {
		b.WriteString("---\n")
		writeScalar(&b, okfVersionKey, idx.Version)
		writeScalar(&b, "title", idx.fmTitle)
		writeScalar(&b, "description", idx.fmDesc)
		b.WriteString("---\n\n")
	}
	b.WriteString(strings.TrimRight(idx.Body, "\n"))
	b.WriteString("\n")
	return []byte(b.String())
}

// marshalLog renders a log.md in §7's date-grouped form: `## YYYY-MM-DD`
// headings (in entry order, which convention keeps newest first), each
// followed by its `* **Kind**: text` list items. Entries without a
// timestamp are written before any date heading; an entry carrying a full
// datetime is grouped under its date part.
func marshalLog(lg Log) []byte {
	var b strings.Builder
	b.WriteString("# Update Log\n")
	currentDate := ""
	for _, e := range lg.Entries {
		d := dateOf(e.Timestamp)
		if d != currentDate && d != "" {
			b.WriteString("\n## ")
			b.WriteString(d)
			b.WriteString("\n")
			currentDate = d
		}
		b.WriteString("* ")
		if e.Kind != "" {
			b.WriteString("**")
			b.WriteString(e.Kind)
			b.WriteString("**: ")
		}
		b.WriteString(e.Text)
		b.WriteString("\n")
	}
	return []byte(b.String())
}

// dateOf reduces an ISO-8601 timestamp to its YYYY-MM-DD date part, or ""
// when the value doesn't start with one.
func dateOf(ts string) string {
	if len(ts) >= 10 && isoDateRe.MatchString(ts[:10]) {
		return ts[:10]
	}
	return ""
}

// writeScalar appends a `key: value` line, quoting the value only when a
// bare YAML plain scalar would be ambiguous. Empty values are skipped.
func writeScalar(b *strings.Builder, key, value string) {
	if value == "" {
		return
	}
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(yamlScalar(value))
	b.WriteString("\n")
}

// yamlScalar returns value as a YAML scalar, single-quoting it when it
// contains characters that a plain scalar cannot safely carry (a colon that
// could start a mapping, list/flow indicators, comment markers) or a
// leading digit that a parser might coerce to a number or date.
func yamlScalar(value string) string {
	// A comma is deliberately absent: in YAML block context (where these
	// scalars live) it needs no quoting, and the OKF fixtures keep such
	// descriptions bare.
	needsQuote := strings.ContainsAny(value, ":#[]{}&*!|>'\"%@`") ||
		strings.HasPrefix(value, " ") || strings.HasSuffix(value, " ") ||
		(value[0] >= '0' && value[0] <= '9')
	if !needsQuote {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
