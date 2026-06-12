package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigFile_RequiresVersion(t *testing.T) {
	t.Parallel()
	path := writeTemp(t, "s.yaml", "dataset: {name: x}\n")
	var tc TrialConfig
	err := loadConfigFile(path, &tc)
	if err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("expected a version error, got %v", err)
	}
}

func TestLoadConfigFile_StrictRejectsUnknownField(t *testing.T) {
	t.Parallel()
	path := writeTemp(t, "s.yaml", "version: 1\nbogus: true\ndataset: {name: x}\n")
	var tc TrialConfig
	err := loadConfigFile(path, &tc)
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("strict mode should reject unknown field, got %v", err)
	}
}

func TestLoadConfigFile_Valid(t *testing.T) {
	t.Parallel()
	path := writeTemp(t, "s.yaml", `version: 1
dataset:
  name: geo
  cases:
    - {id: c1, input: "Capital of Ecuador?", expected: Quito}
subject:
  provider: anthropic
  model: claude-haiku-4-5
scorers:
  - {type: contains}
min_pass: 0.8
`)
	var tc TrialConfig
	if err := loadConfigFile(path, &tc); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if tc.Dataset.Name != "geo" || len(tc.Dataset.Cases) != 1 {
		t.Errorf("dataset = %+v", tc.Dataset)
	}
	if tc.Subject.Provider != "anthropic" || tc.Subject.Model != "claude-haiku-4-5" {
		t.Errorf("subject = %+v", tc.Subject)
	}
	if tc.MinPass == nil || *tc.MinPass != 0.8 {
		t.Errorf("min_pass = %v", tc.MinPass)
	}
}

func TestResolveScorers(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key") // llm_judge constructs its provider eagerly
	scorers, err := resolveScorers([]ScorerBlock{
		{Type: "contains"},
		{Type: "exact"},
		{Type: "regex", Pattern: `\d+`},
		{Type: "llm_judge", Judge: &AgentBlock{Provider: "openai", Model: "gpt-4o"}, Rubric: "r"},
	})
	if err != nil {
		t.Fatalf("resolveScorers: %v", err)
	}
	if len(scorers) != 4 {
		t.Fatalf("got %d scorers", len(scorers))
	}
}

func TestResolveScorer_Errors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		block ScorerBlock
		want  string
	}{
		{"empty type", ScorerBlock{}, "type is required"},
		{"unknown type", ScorerBlock{Type: "vibes"}, "unknown scorer type"},
		{"regex without pattern", ScorerBlock{Type: "regex"}, "requires a pattern"},
		{"judge without block", ScorerBlock{Type: "llm_judge"}, "requires a judge block"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveScorer(tc.block)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestBuiltinByName_Guards(t *testing.T) {
	t.Parallel()
	// file_read without base_dir → error.
	if _, err := builtinByName("file_read", ToolsBlock{}); err == nil {
		t.Error("file_read without base_dir should error")
	}
	// http_get without allowlist → error.
	if _, err := builtinByName("http_get", ToolsBlock{}); err == nil {
		t.Error("http_get without allowlist should error")
	}
	// time/math always work.
	if _, err := builtinByName("math", ToolsBlock{}); err != nil {
		t.Errorf("math: %v", err)
	}
	// file_read with base_dir works.
	if _, err := builtinByName("file_read", ToolsBlock{BaseDir: t.TempDir()}); err != nil {
		t.Errorf("file_read with base_dir: %v", err)
	}
	// unknown → error.
	if _, err := builtinByName("teleport", ToolsBlock{}); err == nil {
		t.Error("unknown builtin should error")
	}
}

func TestResolveProvider_Validates(t *testing.T) {
	t.Parallel()
	if _, err := resolveProvider(AgentBlock{Model: "m"}); err == nil {
		t.Error("missing provider should error")
	}
	if _, err := resolveProvider(AgentBlock{Provider: "anthropic"}); err == nil {
		t.Error("missing model should error")
	}
}

func TestResolveAPIKey_Order(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "from-convention")
	t.Setenv("CUSTOM_KEY", "from-override")
	t.Setenv("LLM_API_KEY", "from-generic")

	// Convention: <PROVIDER>_API_KEY.
	if got := resolveAPIKey(AgentBlock{Provider: "anthropic"}); got != "from-convention" {
		t.Errorf("convention key = %q", got)
	}
	// Explicit override wins.
	if got := resolveAPIKey(AgentBlock{Provider: "anthropic", APIKeyEnv: "CUSTOM_KEY"}); got != "from-override" {
		t.Errorf("override key = %q", got)
	}
	// Generic fallback when no convention var.
	if got := resolveAPIKey(AgentBlock{Provider: "unknownprov"}); got != "from-generic" {
		t.Errorf("generic key = %q", got)
	}
}

func TestResolveToolRegistry_Empty(t *testing.T) {
	t.Parallel()
	reg, cleanup, err := resolveToolRegistry(context.Background(), ToolsBlock{}, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if reg != nil {
		t.Error("no tools declared should yield a nil registry")
	}
}
