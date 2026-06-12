package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runCast(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := cast(context.Background(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestCast_EndToEnd(t *testing.T) {
	srv := fakeOpenAIServer(t, "Quito is the capital of Ecuador.")
	t.Setenv("OPENAI_API_KEY", "test-key")
	path := writeTemp(t, "agent.yaml", fmt.Sprintf(`version: 1
agent:
  provider: openai
  model: fake
  base_url: %s
  system: "Answer concisely."
`, srv.URL))

	code, out, errOut := runCast(t, path, "Capital of Ecuador?")
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "Quito") {
		t.Errorf("output = %q", out)
	}
}

func TestCast_StdinInput(t *testing.T) {
	srv := fakeOpenAIServer(t, "Lima.")
	t.Setenv("OPENAI_API_KEY", "test-key")
	path := writeTemp(t, "agent.yaml", fmt.Sprintf("version: 1\nagent: {provider: openai, model: fake, base_url: %s}\n", srv.URL))

	// Inject a piped stdin.
	orig := stdin
	t.Cleanup(func() { stdin = orig })
	stdin = strings.NewReader("Capital of Peru?")

	code, out, errOut := runCast(t, path) // no positional input → reads stdin
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "Lima") {
		t.Errorf("output = %q", out)
	}
}

func TestCast_Traced(t *testing.T) {
	srv := fakeOpenAIServer(t, "42.")
	t.Setenv("OPENAI_API_KEY", "test-key")
	dbPath := filepath.Join(t.TempDir(), "traces.db")
	path := writeTemp(t, "agent.yaml", fmt.Sprintf("version: 1\nagent: {provider: openai, model: fake, base_url: %s}\n", srv.URL))

	code, out, errOut := runCast(t, path, "What is 6*7?", "--trace", "--db", dbPath, "--run-id", "cast-test-1")
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "42") {
		t.Errorf("output = %q", out)
	}
	if !strings.Contains(errOut, "cast-test-1") {
		t.Errorf("stderr should mention the run id: %s", errOut)
	}
	// The trace must have been written to the store.
	if info, err := os.Stat(dbPath); err != nil || info.Size() == 0 {
		t.Errorf("span store not written: %v", err)
	}
}

func TestCast_MissingInput(t *testing.T) {
	// A file with no positional input and a non-piped stdin → usage error.
	path := writeTemp(t, "agent.yaml", "version: 1\nagent: {provider: openai, model: fake}\n")
	orig := stdin
	t.Cleanup(func() { stdin = orig })
	stdin = strings.NewReader("") // empty piped input

	code, _, errOut := runCast(t, path)
	if code != 64 {
		t.Fatalf("expected exit 64 for missing input, got %d", code)
	}
	if !strings.Contains(errOut, "no input") {
		t.Errorf("stderr = %q", errOut)
	}
}
