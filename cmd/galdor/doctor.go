package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// doctor runs environment sanity checks and prints a checklist: the Go
// toolchain, whether go-installed binaries land on PATH, which provider
// credentials are present, and whether the trace store is reachable. It
// exits 1 if any check is a hard error (✗), 0 otherwise (✓/⚠ only).
func doctor(_ context.Context, args []string, w io.Writer, errW io.Writer) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		_, _ = fmt.Fprintln(w, "galdor doctor — check your environment for common setup problems.\n\nUsage:\n  galdor doctor")
		return 0
	}

	checks := []checkResult{
		checkGoToolchain(),
		checkGoBinOnPath(),
		checkProviderCredentials(),
		checkTraceStore(),
	}

	_, _ = fmt.Fprintln(w, "galdor doctor")
	hardFail := false
	for _, c := range checks {
		_, _ = fmt.Fprintf(w, "  %s %s\n", c.status, c.title)
		for _, d := range c.details {
			_, _ = fmt.Fprintf(w, "      %s\n", d)
		}
		if c.status == statusFail {
			hardFail = true
		}
	}
	if hardFail {
		_, _ = fmt.Fprintln(errW, "\ndoctor: one or more checks failed (✗)")
		return 1
	}
	return 0
}

const (
	statusOK   = "✓"
	statusWarn = "⚠"
	statusFail = "✗"
)

type checkResult struct {
	status  string
	title   string
	details []string
}

// checkGoToolchain reports the Go used to build this binary and, when
// `go` is on PATH, the version available for building/installing.
func checkGoToolchain() checkResult {
	r := checkResult{status: statusOK, title: "Go toolchain"}
	r.details = append(r.details, "built with "+runtime.Version())
	if path, err := exec.LookPath("go"); err == nil {
		out, _ := exec.Command(path, "version").Output() // #nosec G204 -- fixed argument, resolved go binary
		if v := strings.TrimSpace(string(out)); v != "" {
			r.details = append(r.details, "available: "+v)
		}
	} else {
		r.status = statusWarn
		r.details = append(r.details, "`go` is not on PATH — fine if you only use the prebuilt binary, but you can't `go install` or build from source")
	}
	return r
}

// checkGoBinOnPath reports whether the directory `go install` writes to is
// on PATH, so a freshly installed `galdor` is found.
func checkGoBinOnPath() checkResult {
	r := checkResult{title: "go install bin on PATH"}
	bin := goBinDir()
	if bin == "" {
		r.status = statusWarn
		r.details = append(r.details, "could not resolve GOBIN/GOPATH")
		return r
	}
	if onPath(bin) {
		r.status = statusOK
		r.details = append(r.details, bin+" is on PATH")
	} else {
		r.status = statusWarn
		r.details = append(r.details, bin+" is NOT on PATH")
		r.details = append(r.details, "add it so `go install …/cmd/galdor@latest` is found: export PATH=\""+bin+":$PATH\"")
	}
	return r
}

// checkProviderCredentials reports which provider credentials are present
// in the environment (values are never printed).
func checkProviderCredentials() checkResult {
	r := checkResult{title: "provider credentials"}
	// provider name -> the env var that authenticates it.
	envByProvider := []struct{ provider, env string }{
		{"anthropic", "ANTHROPIC_API_KEY"},
		{"openai", "OPENAI_API_KEY"},
		{"google", "GOOGLE_API_KEY"},
		{"groq", "GROQ_API_KEY"},
		{"together", "TOGETHER_API_KEY"},
		{"mistral", "MISTRAL_API_KEY"},
		{"deepseek", "DEEPSEEK_API_KEY"},
		{"minimax", "MINIMAX_API_KEY"},
	}
	var found []string
	for _, e := range envByProvider {
		if os.Getenv(e.env) != "" {
			found = append(found, e.provider+" ("+e.env+")")
		}
	}
	// Generic providerset env + AWS for bedrock.
	if os.Getenv("LLM_PROVIDER") != "" && os.Getenv("LLM_API_KEY") != "" {
		found = append(found, "providerset (LLM_PROVIDER + LLM_API_KEY)")
	}
	if os.Getenv("AWS_ACCESS_KEY_ID") != "" || os.Getenv("AWS_PROFILE") != "" {
		found = append(found, "bedrock (AWS credentials)")
	}

	if len(found) == 0 {
		r.status = statusWarn
		r.details = append(r.details, "none found — set one to run agents, e.g. export ANTHROPIC_API_KEY=…")
		return r
	}
	r.status = statusOK
	for _, f := range found {
		r.details = append(r.details, "set: "+f)
	}
	return r
}

// checkTraceStore reports the default trace store path and whether its
// directory is writable (commands create the DB on first use).
func checkTraceStore() checkResult {
	r := checkResult{title: "trace store"}
	path, err := resolveDBPath("")
	if err != nil {
		r.status = statusFail
		r.details = append(r.details, "cannot resolve trace store path: "+err.Error())
		return r
	}
	r.details = append(r.details, "path: "+path+" (override with --db or $GALDOR_DB)")
	dir := filepath.Dir(path)
	if _, statErr := os.Stat(path); statErr == nil {
		r.status = statusOK
		r.details = append(r.details, "database exists")
		return r
	}
	// No DB yet — that's fine as long as the directory is (or can be) writable.
	if writableDir(dir) {
		r.status = statusOK
		r.details = append(r.details, "not created yet; "+dir+" is writable (a command will create it)")
	} else {
		r.status = statusFail
		r.details = append(r.details, dir+" is not writable")
	}
	return r
}

// goBinDir resolves where `go install` writes binaries: $GOBIN, else
// $GOPATH/bin, else ~/go/bin.
func goBinDir() string {
	if b := os.Getenv("GOBIN"); b != "" {
		return b
	}
	if gp := os.Getenv("GOPATH"); gp != "" {
		// GOPATH may be a list; the first entry wins.
		first := strings.Split(gp, string(os.PathListSeparator))[0]
		return filepath.Join(first, "bin")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "go", "bin")
}

// onPath reports whether dir is one of the entries in $PATH.
func onPath(dir string) bool {
	for _, p := range strings.Split(os.Getenv("PATH"), string(os.PathListSeparator)) {
		if p == dir {
			return true
		}
	}
	return false
}

// writableDir reports whether dir — or, when it doesn't exist yet, its
// nearest existing ancestor — is a writable directory (so the trace store
// could be created there).
func writableDir(dir string) bool {
	for d := dir; ; {
		info, err := os.Stat(d)
		if err == nil {
			return info.IsDir() && tryWrite(d)
		}
		parent := filepath.Dir(d)
		if parent == d {
			return false
		}
		d = parent
	}
}

// tryWrite reports whether a file can be created in dir, by creating and
// immediately removing a temp file.
func tryWrite(dir string) bool {
	f, err := os.CreateTemp(dir, ".galdor-doctor-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}
