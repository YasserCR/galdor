package spellbook_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YasserCR/galdor/pkg/spellbook"
)

func TestMemBook_GetLatestList(t *testing.T) {
	t.Parallel()
	b := spellbook.New(
		spellbook.Spell{Name: "greet", Version: "v1", Template: "Hi {{.Name}}"},
		spellbook.Spell{Name: "greet", Version: "v2", Template: "Hello {{.Name}}"},
		spellbook.Spell{Name: "bye", Version: "v1", Template: "Bye"},
	)

	got, err := b.Get("greet", "v1")
	if err != nil || got.Template != "Hi {{.Name}}" {
		t.Fatalf("Get = %+v, %v", got, err)
	}

	latest, err := b.Latest("greet")
	if err != nil || latest.Version != "v2" {
		t.Fatalf("Latest = %+v, %v", latest, err)
	}

	all, err := b.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].Name != "bye" || all[1].Name != "greet" {
		t.Fatalf("List not sorted by name/version: %+v", all)
	}
}

func TestMemBook_NotFound(t *testing.T) {
	t.Parallel()
	b := spellbook.New()
	if _, err := b.Get("nope", "v1"); !errors.Is(err, spellbook.ErrNotFound) {
		t.Errorf("Get missing should be ErrNotFound, got %v", err)
	}
	if _, err := b.Latest("nope"); !errors.Is(err, spellbook.ErrNotFound) {
		t.Errorf("Latest missing should be ErrNotFound, got %v", err)
	}
}

func TestSpell_Render(t *testing.T) {
	t.Parallel()
	s := spellbook.Spell{Name: "t", Template: "Summarize {{.Topic}} in {{.N}} points."}
	out, err := s.Render(map[string]any{"Topic": "Go", "N": 3})
	if err != nil {
		t.Fatal(err)
	}
	if out != "Summarize Go in 3 points." {
		t.Errorf("render = %q", out)
	}
	// A missing key is an error (missingkey=error), so a typo'd template
	// fails loudly instead of rendering "<no value>".
	if _, err := s.Render(map[string]any{"Topic": "Go"}); err == nil {
		t.Error("missing key should error")
	}
}

func TestFileBook_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeSpell(t, dir, "summarize", "v1", "Summarize:\n{{.Input}}")
	writeSpell(t, dir, "summarize", "2024-06-01", "Summarize concisely:\n{{.Input}}")
	writeSpell(t, dir, "translate", "v1", "Translate to {{.Lang}}")

	b, err := spellbook.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	got, err := b.Get("summarize", "v1")
	if err != nil || !strings.HasPrefix(got.Template, "Summarize:") {
		t.Fatalf("Get = %+v, %v", got, err)
	}

	// Lexically greatest: "v1" > "2024-06-01".
	latest, err := b.Latest("summarize")
	if err != nil || latest.Version != "v1" {
		t.Fatalf("Latest = %+v, %v", latest, err)
	}

	all, err := b.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("List len = %d, want 3", len(all))
	}
}

func TestFileBook_NotFound(t *testing.T) {
	t.Parallel()
	b, err := spellbook.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Get("ghost", "v1"); !errors.Is(err, spellbook.ErrNotFound) {
		t.Errorf("got %v", err)
	}
}

func TestOpen_RejectsNonDir(t *testing.T) {
	t.Parallel()
	f := filepath.Join(t.TempDir(), "afile")
	_ = os.WriteFile(f, []byte("x"), 0o600)
	if _, err := spellbook.Open(f); err == nil {
		t.Error("Open on a file should error")
	}
}

func TestFileBook_RejectsPathTraversal(t *testing.T) {
	t.Parallel()
	b, _ := spellbook.Open(t.TempDir())
	for _, bad := range []string{"../etc", "a/b", "..", "", "x\x00y"} {
		if _, err := b.Get(bad, "v1"); err == nil {
			t.Errorf("Get(%q) should reject traversal", bad)
		}
		if _, err := b.Get("ok", bad); err == nil {
			t.Errorf("Get version %q should reject traversal", bad)
		}
	}
}

func writeSpell(t *testing.T, dir, name, version, content string) {
	t.Helper()
	d := filepath.Join(dir, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, version+".md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
