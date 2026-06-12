package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/YasserCR/galdor/pkg/spellbook"
)

// spellbookCmd is the `galdor spellbook` verb: inspect a directory of
// versioned prompt templates (the same store an agent block's
// system_spell reads).
func spellbookCmd(_ context.Context, args []string, w io.Writer, errW io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(errW, spellbookUsage)
		return 64
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list", "ls":
		return spellbookList(rest, w, errW)
	case "show", "cat":
		return spellbookShow(rest, w, errW)
	case "diff":
		return spellbookDiff(rest, w, errW)
	case "render":
		return spellbookRender(rest, w, errW)
	case "-h", "--help", "help":
		_, _ = fmt.Fprintln(w, spellbookUsage)
		return 0
	default:
		_, _ = fmt.Fprintf(errW, "galdor spellbook: unknown subcommand %q\n\n%s\n", sub, spellbookUsage)
		return 64
	}
}

const spellbookUsage = `galdor spellbook — manage versioned prompt templates.

Usage:
  galdor spellbook list  [--dir DIR]
  galdor spellbook show  <name> [version] [--dir DIR]
  galdor spellbook diff  <name> <v1> <v2> [--dir DIR]
  galdor spellbook render <name> [version] [--dir DIR] [--data JSON]

The store is a directory of dir/<name>/<version>.md files (each file's
content is the raw template). --dir defaults to $GALDOR_SPELLBOOK, then
./spells. With no version, show/render use the lexically-greatest one.
An agent block can reference a spell with "system_spell: {name, version}".`

// dirFlag adds the shared --dir flag and returns its resolved value after
// parsing. The remaining positionals are returned too.
func parseDirAndArgs(fs *flag.FlagSet, args []string) (dir string, positional []string, err error) {
	d := fs.String("dir", "", "spellbook directory (default $GALDOR_SPELLBOOK or ./spells)")
	// Partition so flags after positionals are still honored.
	flags, pos := partitionArgs(args, map[string]bool{"dir": true, "data": true})
	if perr := fs.Parse(flags); perr != nil {
		return "", nil, perr
	}
	dir = *d
	if dir == "" {
		dir = spellbookDir()
	}
	return dir, pos, nil
}

func spellbookList(args []string, w io.Writer, errW io.Writer) int {
	fs := flag.NewFlagSet("spellbook list", flag.ContinueOnError)
	fs.SetOutput(errW)
	dir, _, err := parseDirAndArgs(fs, args)
	if helpRequested(err) {
		_, _ = fmt.Fprintln(w, spellbookUsage)
		return 0
	}
	if err != nil {
		return 64
	}
	book, err := spellbook.Open(dir)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "spellbook: %v\n", err)
		return 70
	}
	spells, err := book.List()
	if err != nil {
		_, _ = fmt.Fprintf(errW, "spellbook: %v\n", err)
		return 70
	}
	if len(spells) == 0 {
		_, _ = fmt.Fprintf(w, "(no spells in %s)\n", dir)
		return 0
	}
	for _, s := range spells {
		_, _ = fmt.Fprintf(w, "%-24s %s\n", s.Name, s.Version)
	}
	return 0
}

func spellbookShow(args []string, w io.Writer, errW io.Writer) int {
	fs := flag.NewFlagSet("spellbook show", flag.ContinueOnError)
	fs.SetOutput(errW)
	dir, pos, err := parseDirAndArgs(fs, args)
	if helpRequested(err) {
		_, _ = fmt.Fprintln(w, spellbookUsage)
		return 0
	}
	if err != nil {
		return 64
	}
	if len(pos) < 1 {
		_, _ = fmt.Fprintf(errW, "spellbook show: <name> is required\n")
		return 64
	}
	s, code := lookupSpell(dir, pos[0], versionArg(pos), errW)
	if code != 0 {
		return code
	}
	_, _ = fmt.Fprint(w, s.Template)
	if !strings.HasSuffix(s.Template, "\n") {
		_, _ = fmt.Fprintln(w)
	}
	return 0
}

func spellbookRender(args []string, w io.Writer, errW io.Writer) int {
	fs := flag.NewFlagSet("spellbook render", flag.ContinueOnError)
	fs.SetOutput(errW)
	data := fs.String("data", "", "JSON object of template variables")
	dir, pos, err := parseDirAndArgs(fs, args)
	if helpRequested(err) {
		_, _ = fmt.Fprintln(w, spellbookUsage)
		return 0
	}
	if err != nil {
		return 64
	}
	if len(pos) < 1 {
		_, _ = fmt.Fprintf(errW, "spellbook render: <name> is required\n")
		return 64
	}
	s, code := lookupSpell(dir, pos[0], versionArg(pos), errW)
	if code != 0 {
		return code
	}
	var vars map[string]any
	if strings.TrimSpace(*data) != "" {
		if jerr := json.Unmarshal([]byte(*data), &vars); jerr != nil {
			_, _ = fmt.Fprintf(errW, "spellbook render: --data is not valid JSON: %v\n", jerr)
			return 64
		}
	}
	out, err := s.Render(vars)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "spellbook render: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(w, out)
	return 0
}

func spellbookDiff(args []string, w io.Writer, errW io.Writer) int {
	fs := flag.NewFlagSet("spellbook diff", flag.ContinueOnError)
	fs.SetOutput(errW)
	dir, pos, err := parseDirAndArgs(fs, args)
	if helpRequested(err) {
		_, _ = fmt.Fprintln(w, spellbookUsage)
		return 0
	}
	if err != nil {
		return 64
	}
	if len(pos) != 3 {
		_, _ = fmt.Fprintf(errW, "spellbook diff: usage: diff <name> <v1> <v2>\n")
		return 64
	}
	name, v1, v2 := pos[0], pos[1], pos[2]
	book, err := spellbook.Open(dir)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "spellbook: %v\n", err)
		return 70
	}
	a, err := book.Get(name, v1)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "spellbook: %v\n", err)
		return 1
	}
	b, err := book.Get(name, v2)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "spellbook: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(w, "--- %s@%s\n+++ %s@%s\n", name, v1, name, v2)
	for _, line := range lineDiff(a.Template, b.Template) {
		_, _ = fmt.Fprintln(w, line)
	}
	return 0
}

// lookupSpell fetches a spell (specific version, or latest when version is
// empty) and maps a miss to a CLI exit code.
func lookupSpell(dir, name, version string, errW io.Writer) (spellbook.Spell, int) {
	book, err := spellbook.Open(dir)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "spellbook: %v\n", err)
		return spellbook.Spell{}, 70
	}
	var s spellbook.Spell
	if version != "" {
		s, err = book.Get(name, version)
	} else {
		s, err = book.Latest(name)
	}
	if err != nil {
		_, _ = fmt.Fprintf(errW, "spellbook: %v\n", err)
		return spellbook.Spell{}, 1
	}
	return s, 0
}

// versionArg returns the optional version positional (the 2nd token) or
// "" when only a name was given.
func versionArg(pos []string) string {
	if len(pos) >= 2 {
		return pos[1]
	}
	return ""
}

// lineDiff returns a unified-style line diff of a vs b using a longest-
// common-subsequence backtrace. Lines only in a are prefixed "-", lines
// only in b "+", common lines " ".
func lineDiff(a, b string) []string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	// LCS length table.
	lcs := make([][]int, len(al)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(bl)+1)
	}
	for i := len(al) - 1; i >= 0; i-- {
		for j := len(bl) - 1; j >= 0; j-- {
			switch {
			case al[i] == bl[j]:
				lcs[i][j] = lcs[i+1][j+1] + 1
			case lcs[i+1][j] >= lcs[i][j+1]:
				lcs[i][j] = lcs[i+1][j]
			default:
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var out []string
	i, j := 0, 0
	for i < len(al) && j < len(bl) {
		switch {
		case al[i] == bl[j]:
			out = append(out, "  "+al[i])
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, "- "+al[i])
			i++
		default:
			out = append(out, "+ "+bl[j])
			j++
		}
	}
	for ; i < len(al); i++ {
		out = append(out, "- "+al[i])
	}
	for ; j < len(bl); j++ {
		out = append(out, "+ "+bl[j])
	}
	return out
}
