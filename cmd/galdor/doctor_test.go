package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runDoctor(t *testing.T) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := doctor(context.Background(), nil, &out, &errOut)
	return code, out.String(), errOut.String()
}

// clearProviderEnv blanks every credential var doctor inspects so a test
// machine's real environment doesn't leak into assertions.
func clearProviderEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{
		"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GOOGLE_API_KEY", "GROQ_API_KEY",
		"TOGETHER_API_KEY", "MISTRAL_API_KEY", "DEEPSEEK_API_KEY", "MINIMAX_API_KEY",
		"LLM_PROVIDER", "LLM_API_KEY", "AWS_ACCESS_KEY_ID", "AWS_PROFILE", "GALDOR_DB",
	} {
		t.Setenv(v, "")
	}
}

func TestDoctor_DetectsCredential(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "secret-value")

	code, out, _ := runDoctor(t)
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(out, "anthropic (ANTHROPIC_API_KEY)") {
		t.Errorf("should report the detected credential:\n%s", out)
	}
	// The secret value must never be printed.
	if strings.Contains(out, "secret-value") {
		t.Error("doctor leaked the credential value")
	}
}

func TestDoctor_NoCredentialsWarns(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("HOME", t.TempDir())

	code, out, _ := runDoctor(t)
	if code != 0 {
		t.Fatalf("exit %d, want 0 (no creds is a warning, not a failure)", code)
	}
	if !strings.Contains(out, "none found") {
		t.Errorf("should warn about missing credentials:\n%s", out)
	}
}

func TestDoctor_GoBinOnPath(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("HOME", t.TempDir())
	bin := filepath.Join(t.TempDir(), "gobin")
	t.Setenv("GOBIN", bin)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, out, _ := runDoctor(t)
	if !strings.Contains(out, bin+" is on PATH") {
		t.Errorf("GOBIN on PATH should be reported ✓:\n%s", out)
	}
}

func TestDoctor_TraceStoreWritable(t *testing.T) {
	clearProviderEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	code, out, _ := runDoctor(t)
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(out, "trace store") || !strings.Contains(out, "writable") {
		t.Errorf("trace store check missing:\n%s", out)
	}
}

func TestDoctor_TraceStoreUnwritableFails(t *testing.T) {
	clearProviderEnv(t)
	// Point the store at a path under a read-only directory.
	ro := t.TempDir()
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Skip("cannot make a read-only dir on this platform")
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })
	t.Setenv("GALDOR_DB", filepath.Join(ro, "sub", "traces.db"))

	code, _, errOut := runDoctor(t)
	if code != 1 {
		t.Fatalf("an unwritable store dir should fail (exit 1), got %d", code)
	}
	if !strings.Contains(errOut, "failed") {
		t.Errorf("stderr should note the failure: %s", errOut)
	}
}
