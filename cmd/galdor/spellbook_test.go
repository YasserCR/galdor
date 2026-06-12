package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func seedSpellbook(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, s := range []struct{ name, version, body string }{
		{"greet", "v1", "You are helpful."},
		{"greet", "v2", "You are a helpful, friendly assistant."},
		{"summarize", "v1", "Summarize {{.Topic}} in {{.N}} points."},
	} {
		d := filepath.Join(dir, s.name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, s.version+".md"), []byte(s.body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func runSpellbook(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := spellbookCmd(context.Background(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestSpellbook_List(t *testing.T) {
	t.Parallel()
	dir := seedSpellbook(t)
	code, out, errOut := runSpellbook(t, "list", "--dir", dir)
	if code != 0 {
		t.Fatalf("exit %d; %s", code, errOut)
	}
	if !strings.Contains(out, "greet") || !strings.Contains(out, "v2") || !strings.Contains(out, "summarize") {
		t.Errorf("list output = %q", out)
	}
}

func TestSpellbook_ShowLatestAndVersion(t *testing.T) {
	t.Parallel()
	dir := seedSpellbook(t)
	// No version → latest (lexically greatest = v2).
	_, out, _ := runSpellbook(t, "show", "greet", "--dir", dir)
	if !strings.Contains(out, "friendly") {
		t.Errorf("latest show = %q", out)
	}
	// Explicit version.
	_, out, _ = runSpellbook(t, "show", "greet", "v1", "--dir", dir)
	if strings.Contains(out, "friendly") || !strings.Contains(out, "You are helpful.") {
		t.Errorf("v1 show = %q", out)
	}
}

func TestSpellbook_Diff(t *testing.T) {
	t.Parallel()
	dir := seedSpellbook(t)
	code, out, _ := runSpellbook(t, "diff", "greet", "v1", "v2", "--dir", dir)
	if code != 0 {
		t.Fatal("diff exit nonzero")
	}
	if !strings.Contains(out, "- You are helpful.") || !strings.Contains(out, "+ You are a helpful") {
		t.Errorf("diff output = %q", out)
	}
}

func TestSpellbook_Render(t *testing.T) {
	t.Parallel()
	dir := seedSpellbook(t)
	code, out, errOut := runSpellbook(t, "render", "summarize", "v1", "--dir", dir, "--data", `{"Topic":"Go","N":3}`)
	if code != 0 {
		t.Fatalf("exit %d; %s", code, errOut)
	}
	if !strings.Contains(out, "Summarize Go in 3 points.") {
		t.Errorf("render = %q", out)
	}
}

func TestSpellbook_MissingSpell(t *testing.T) {
	t.Parallel()
	dir := seedSpellbook(t)
	code, _, errOut := runSpellbook(t, "show", "ghost", "--dir", dir)
	if code != 1 {
		t.Fatalf("expected exit 1 for missing spell, got %d", code)
	}
	if !strings.Contains(errOut, "not found") {
		t.Errorf("stderr = %q", errOut)
	}
}

// TestEffectiveSystem_FromSpell covers the agent-block integration: an
// agent's system prompt can come from the spellbook instead of inline.
func TestEffectiveSystem_FromSpell(t *testing.T) {
	dir := seedSpellbook(t)
	t.Setenv("GALDOR_SPELLBOOK", dir)

	// Latest version via env-default dir.
	got, err := effectiveSystem(AgentBlock{SystemSpell: &SpellRef{Name: "greet"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "friendly") {
		t.Errorf("latest spell system = %q", got)
	}

	// Specific version + explicit dir.
	got, err = effectiveSystem(AgentBlock{SystemSpell: &SpellRef{Name: "greet", Version: "v1", Dir: dir}})
	if err != nil {
		t.Fatal(err)
	}
	if got != "You are helpful." {
		t.Errorf("v1 spell system = %q", got)
	}

	// Inline system still works.
	got, _ = effectiveSystem(AgentBlock{System: "inline"})
	if got != "inline" {
		t.Errorf("inline = %q", got)
	}

	// Both set → error.
	if _, err := effectiveSystem(AgentBlock{System: "x", SystemSpell: &SpellRef{Name: "greet"}}); err == nil {
		t.Error("system + system_spell should conflict")
	}
}

// TestCast_SystemSpell drives cast end to end with a spellbook-sourced
// system prompt.
func TestCast_SystemSpell(t *testing.T) {
	srv := fakeOpenAIServer(t, "ok")
	t.Setenv("OPENAI_API_KEY", "test-key")
	sbDir := seedSpellbook(t)
	agentPath := writeTemp(t, "agent.yaml", `version: 1
agent:
  provider: openai
  model: fake
  base_url: `+srv.URL+`
  system_spell: {name: greet, version: v1, dir: `+sbDir+`}
`)
	code, out, errOut := runCast(t, agentPath, "hi")
	if code != 0 {
		t.Fatalf("exit %d; %s", code, errOut)
	}
	if !strings.Contains(out, "ok") {
		t.Errorf("out = %q", out)
	}
}
