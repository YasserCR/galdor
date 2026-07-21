package okf

import (
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/YasserCR/galdor/pkg/memory"
)

// Authoring-side validation. The loader is deliberately permissive (§9): it
// never rejects a bundle, only records Warnings. Validate is its strict
// counterpart — the check a producer or CI runs before publishing a bundle.
// It reports every issue as a Problem; an empty result means the bundle is
// clean against OKF v0.1's required and recommended rules.

// supportedMajor is the OKF spec major version this module targets (v0.1).
// A bundle declaring a newer major still loads (permissive), but Validate
// and the loader flag it so a consumer knows it may be reading a format it
// only partially understands.
const supportedMajor = "0"

// Severity classifies a validation Problem.
type Severity string

const (
	// SeverityError marks a violation of a hard OKF requirement (e.g. a
	// concept with no type).
	SeverityError Severity = "error"
	// SeverityWarning marks a violation of a recommendation (a missing
	// recommended field, a broken link, an unknown spec version).
	SeverityWarning Severity = "warning"
)

// Problem is one issue found by Validate.
type Problem struct {
	// Where locates the problem: a concept id, or a reserved path such as
	// "index.md".
	Where string
	// Severity is error (hard requirement) or warning (recommendation).
	Severity Severity
	// Message is a human-readable description.
	Message string
}

func (p Problem) String() string {
	return string(p.Severity) + " " + p.Where + ": " + p.Message
}

// isoTimestampRe matches an OKF timestamp: an ISO-8601 date, optionally with
// a time and timezone (§4.1).
var isoTimestampRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}([T ]\d{2}:\d{2}(:\d{2})?(\.\d+)?(Z|[+-]\d{2}:?\d{2})?)?$`)

// HasErrors reports whether any Problem in the slice is an error (as opposed
// to only warnings). Convenient for gating a publish/CI step.
func HasErrors(ps []Problem) bool {
	for _, p := range ps {
		if p.Severity == SeverityError {
			return true
		}
	}
	return false
}

// Validate checks the bundle against OKF v0.1's conformance rules (§9) and
// authoring recommendations, returning every problem found, concepts in id
// order:
//
//   - §9.1: every concept must carry a parseable frontmatter block (error
//     if absent).
//   - §9.2: type is required on every concept (error if missing).
//   - §9.3: reserved files must follow their structure when present —
//     frontmatter in a non-root index.md (§6/§11) and non-ISO date
//     headings in a log.md (§7 MUST) are errors; index entries without a
//     markdown link and log items outside any date group are warnings.
//   - description and timestamp are recommended (warning if missing); a
//     present timestamp must be ISO-8601 (warning otherwise).
//   - cross-links must resolve to a concept in the bundle (warning per
//     concept for each broken target; the spec tolerates them, §5.3).
//   - the root index.md should declare an okf_version this module supports
//     (warning if missing or a newer major).
//
// title is intentionally not checked: §4.1 makes it optional (derive from
// the filename). Validate never mutates the bundle.
func (b *Bundle) Validate() []Problem {
	var ps []Problem

	// Spec version (declared in the root index.md, §11).
	switch {
	case b.Version == "":
		ps = append(ps, Problem{"index.md", SeverityWarning, "missing okf_version"})
	case majorOf(b.Version) != supportedMajor:
		ps = append(ps, Problem{"index.md", SeverityWarning,
			"okf_version " + b.Version + " has an unsupported major (this module targets " + supportedMajor + ".x)"})
	}

	// §9.1: concepts without a frontmatter block.
	noFM := make(map[string]bool, len(b.noFrontmatter))
	for _, id := range b.noFrontmatter {
		noFM[id] = true
	}

	idSet := b.idSet()
	for _, c := range sortedConcepts(b) {
		id := c.Metadata[MetaConceptID]
		if noFM[id] {
			ps = append(ps, Problem{id, SeverityError, "missing frontmatter block"})
		}
		if strings.TrimSpace(c.Metadata[MetaType]) == "" {
			ps = append(ps, Problem{id, SeverityError, "missing required field: type"})
		}
		if c.Metadata[MetaDesc] == "" {
			ps = append(ps, Problem{id, SeverityWarning, "missing recommended field: description"})
		}
		if ts := c.Metadata[MetaTimestamp]; ts == "" {
			ps = append(ps, Problem{id, SeverityWarning, "missing recommended field: timestamp"})
		} else if !isoTimestampRe.MatchString(ts) {
			ps = append(ps, Problem{id, SeverityWarning, "timestamp is not ISO-8601: " + ts})
		}
		if _, broken := resolveLinks(c.Text, dirOfID(id), idSet, id); len(broken) > 0 {
			for _, t := range broken {
				ps = append(ps, Problem{id, SeverityWarning, "broken link -> " + t})
			}
		}
	}

	ps = append(ps, b.validateReserved()...)
	return ps
}

// validateReserved checks §9's third rule: reserved files must follow the
// structure of §6 (index.md) and §7 (log.md) when present.
func (b *Bundle) validateReserved() []Problem {
	var ps []Problem
	for _, dir := range sortedKeys(b.Indexes) {
		idx := b.Indexes[dir]
		where := path.Join(dir, "index.md")
		// Frontmatter is permitted only in the bundle root's index (§11).
		if idx.fmPresent && dir != "" {
			ps = append(ps, Problem{where, SeverityError,
				"frontmatter is only permitted in the bundle-root index.md"})
		}
		// §6 entries are list items carrying a markdown link.
		for _, item := range listItems(idx.Body) {
			if !linkRe.MatchString(item) {
				ps = append(ps, Problem{where, SeverityWarning,
					"index entry without a markdown link: " + item})
			}
		}
	}
	for _, dir := range sortedKeys(b.Logs) {
		lg := b.Logs[dir]
		where := path.Join(dir, "log.md")
		for _, d := range lg.badDates {
			ps = append(ps, Problem{where, SeverityError,
				"log date heading is not ISO 8601 (YYYY-MM-DD): " + d})
		}
		if lg.undated > 0 {
			ps = append(ps, Problem{where, SeverityWarning,
				"log entries outside any date heading: " + itoa(lg.undated)})
		}
	}
	return ps
}

// listItems returns the trimmed content of every markdown list item
// ("- ..." / "* ...") in a body.
func listItems(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if m := logItemRe.FindStringSubmatch(line); m != nil {
			if item := strings.TrimSpace(m[1]); item != "" {
				out = append(out, item)
			}
		}
	}
	return out
}

// sortedKeys returns a map's keys sorted, for deterministic reports.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// itoa converts a small non-negative count to its decimal string.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf []byte
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}

// sortedConcepts returns the bundle's concepts ordered by id, so Validate's
// output is deterministic regardless of how the Bundle was built.
func sortedConcepts(b *Bundle) []memory.Document {
	out := append([]memory.Document(nil), b.Concepts...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Metadata[MetaConceptID] < out[j].Metadata[MetaConceptID]
	})
	return out
}

// idSet returns the set of concept ids in the bundle, for link resolution.
func (b *Bundle) idSet() map[string]bool {
	set := make(map[string]bool, len(b.Concepts))
	for _, c := range b.Concepts {
		set[c.Metadata[MetaConceptID]] = true
	}
	return set
}

// majorOf returns the major component of a dotted version ("0.1" -> "0").
func majorOf(version string) string {
	major, _, _ := strings.Cut(version, ".")
	return major
}
