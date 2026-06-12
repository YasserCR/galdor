// Package spellbook is galdor's prompt registry: versioned prompt
// templates with a diff-friendly storage format, retrievable by name and
// version from agents and the CLI.
//
// "Spellbook" is one of the few themed package names; a prompt registry
// stores incantations — galdors in the project's namesake sense — that are
// named, versioned, and chanted at the model. The constructors are
// unthemed and Go-idiomatic (New, Open, Book.Get) so application code
// doesn't have to think about the theming.
//
// The package is stdlib-only by design: it lives in the core module, which
// stays dependency-light. The file store keeps each spell version as a
// plain text file, so prompts are reviewed in code review the same way Go
// source is.
package spellbook

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
)

// ErrNotFound is returned by Book lookups when a spell (or version) does
// not exist.
var ErrNotFound = errors.New("spellbook: spell not found")

// Spell is one versioned prompt template.
type Spell struct {
	Name     string
	Version  string
	Template string
	Metadata map[string]string
}

// Render executes the spell's template against data (a map or struct),
// using Go text/template syntax (e.g. {{.Input}}). A spell with no
// template actions returns its text verbatim.
func (s Spell) Render(data any) (string, error) {
	tmpl, err := template.New(s.Name).Option("missingkey=error").Parse(s.Template)
	if err != nil {
		return "", fmt.Errorf("spellbook: parse %q: %w", s.Name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("spellbook: render %q: %w", s.Name, err)
	}
	return buf.String(), nil
}

// Book is a registry of spells.
//
// Get fetches a specific (name, version). Latest returns the
// lexically-greatest version of a name — version strings are opaque
// labels, so "latest" means the greatest under string ordering (use
// zero-padded or date-prefixed versions if you need a particular order).
// List returns every spell, sorted by (name, version).
type Book interface {
	Get(name, version string) (Spell, error)
	Latest(name string) (Spell, error)
	List() ([]Spell, error)
}

// New returns an in-memory Book holding the given spells. A later spell
// with the same (name, version) replaces an earlier one.
func New(spells ...Spell) Book {
	m := make(map[string]Spell, len(spells))
	b := &memBook{spells: m}
	for _, s := range spells {
		m[key(s.Name, s.Version)] = s
	}
	return b
}

type memBook struct {
	spells map[string]Spell
}

func (b *memBook) Get(name, version string) (Spell, error) {
	s, ok := b.spells[key(name, version)]
	if !ok {
		return Spell{}, fmt.Errorf("%w: %q@%q", ErrNotFound, name, version)
	}
	return s, nil
}

func (b *memBook) Latest(name string) (Spell, error) {
	var versions []string
	for _, s := range b.spells {
		if s.Name == name {
			versions = append(versions, s.Version)
		}
	}
	if len(versions) == 0 {
		return Spell{}, fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	sort.Strings(versions)
	return b.Get(name, versions[len(versions)-1])
}

func (b *memBook) List() ([]Spell, error) {
	out := make([]Spell, 0, len(b.spells))
	for _, s := range b.spells {
		out = append(out, s)
	}
	sortSpells(out)
	return out, nil
}

// spellExt is the file extension for spell version files in the file
// store. The file content is the raw template.
const spellExt = ".md"

// Open returns a file-backed Book rooted at dir. The layout is
//
//	dir/<name>/<version>.md
//
// where each file's content is the raw prompt template. Names and
// versions are the file/directory names. Metadata is not stored by the
// file backend (the field is reserved for in-memory and future use).
func Open(dir string) (Book, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("spellbook: open %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("spellbook: %s is not a directory", dir)
	}
	return &fileBook{dir: dir}, nil
}

type fileBook struct {
	dir string
}

func (b *fileBook) Get(name, version string) (Spell, error) {
	if err := safeName(name); err != nil {
		return Spell{}, err
	}
	if err := safeName(version); err != nil {
		return Spell{}, err
	}
	path := filepath.Join(b.dir, name, version+spellExt)
	data, err := os.ReadFile(path) // #nosec G304 -- path is built from validated name/version segments under b.dir
	if err != nil {
		if os.IsNotExist(err) {
			return Spell{}, fmt.Errorf("%w: %q@%q", ErrNotFound, name, version)
		}
		return Spell{}, fmt.Errorf("spellbook: read %q@%q: %w", name, version, err)
	}
	return Spell{Name: name, Version: version, Template: string(data)}, nil
}

func (b *fileBook) Latest(name string) (Spell, error) {
	versions, err := b.versionsOf(name)
	if err != nil {
		return Spell{}, err
	}
	if len(versions) == 0 {
		return Spell{}, fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	sort.Strings(versions)
	return b.Get(name, versions[len(versions)-1])
}

func (b *fileBook) List() ([]Spell, error) {
	entries, err := os.ReadDir(b.dir)
	if err != nil {
		return nil, fmt.Errorf("spellbook: list %s: %w", b.dir, err)
	}
	var out []Spell
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		versions, verr := b.versionsOf(name)
		if verr != nil {
			return nil, verr
		}
		for _, v := range versions {
			s, gerr := b.Get(name, v)
			if gerr != nil {
				return nil, gerr
			}
			out = append(out, s)
		}
	}
	sortSpells(out)
	return out, nil
}

// versionsOf returns the version labels recorded for name (the .md files
// in dir/<name>/), without extensions.
func (b *fileBook) versionsOf(name string) ([]string, error) {
	if err := safeName(name); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(b.dir, name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("spellbook: list %q: %w", name, err)
	}
	var versions []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), spellExt) {
			continue
		}
		versions = append(versions, strings.TrimSuffix(e.Name(), spellExt))
	}
	return versions, nil
}

func key(name, version string) string { return name + "\x00" + version }

func sortSpells(s []Spell) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].Name != s[j].Name {
			return s[i].Name < s[j].Name
		}
		return s[i].Version < s[j].Version
	})
}

// safeName rejects spell/version labels that could escape the store
// directory (path separators, "..", empty). The file store joins these
// into a path, so they must be single, non-traversing segments.
func safeName(s string) error {
	if s == "" {
		return fmt.Errorf("spellbook: name/version must not be empty")
	}
	if s == "." || s == ".." || strings.ContainsAny(s, `/\`) || strings.Contains(s, "..") {
		return fmt.Errorf("spellbook: invalid name/version %q (no path separators or '..')", s)
	}
	return nil
}
