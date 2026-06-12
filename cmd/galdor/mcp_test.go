package main

import (
	"bytes"
	"context"
	"flag"
	"net"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestBuildServeRegistry_DefaultsToSafeTools(t *testing.T) {
	t.Parallel()
	// No guard flags: only the no-config-needed tools (time, math).
	_, served, err := buildServeRegistry("", nil, false, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(served, ","); got != "math,time" {
		t.Errorf("default served = %q, want math,time", got)
	}
}

func TestBuildServeRegistry_GuardFlagsEnableTools(t *testing.T) {
	t.Parallel()
	_, served, err := buildServeRegistry(t.TempDir(), []string{"example.com"}, false, false, "")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(served)
	if got := strings.Join(served, ","); got != "file_read,http_get,math,time" {
		t.Errorf("served = %q, want all four", got)
	}
}

func TestBuildServeRegistry_AllowAnyHostEnablesHTTPGet(t *testing.T) {
	t.Parallel()
	_, served, err := buildServeRegistry("", nil, true, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(served, "http_get") {
		t.Errorf("--allow-any-host should enable http_get, got %v", served)
	}
}

func TestBuildServeRegistry_ToolsAllowlistNarrows(t *testing.T) {
	t.Parallel()
	_, served, err := buildServeRegistry("", nil, false, false, "math")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(served, ","); got != "math" {
		t.Errorf("served = %q, want math", got)
	}
}

func TestBuildServeRegistry_RejectsUnconfiguredToolInAllowlist(t *testing.T) {
	t.Parallel()
	// file_read named without --base-dir is unavailable → error.
	_, _, err := buildServeRegistry("", nil, false, false, "file_read")
	if err == nil {
		t.Fatal("expected error naming file_read as not available")
	}
	if !strings.Contains(err.Error(), "file_read") {
		t.Errorf("error should name the tool, got %v", err)
	}
}

func TestParseClientArgs_SplitsAtDoubleDash(t *testing.T) {
	t.Parallel()
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	to := fs.Duration("timeout", time.Second, "")
	front, command, err := parseClientArgs(fs, []string{"--timeout", "5s", "math", "{}", "--", "galdor", "mcp", "serve"})
	if err != nil {
		t.Fatal(err)
	}
	if *to != 5*time.Second {
		t.Errorf("flag before -- not parsed: %v", *to)
	}
	if strings.Join(front, " ") != "math {}" {
		t.Errorf("front = %v, want [math {}]", front)
	}
	if strings.Join(command, " ") != "galdor mcp serve" {
		t.Errorf("command = %v", command)
	}
}

func TestParseClientArgs_URLForm(t *testing.T) {
	t.Parallel()
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	front, command, err := parseClientArgs(fs, []string{"http://127.0.0.1:4000", "math", "{}"})
	if err != nil {
		t.Fatal(err)
	}
	if len(command) != 0 {
		t.Errorf("command should be empty without --, got %v", command)
	}
	if strings.Join(front, " ") != "http://127.0.0.1:4000 math {}" {
		t.Errorf("front = %v", front)
	}
}

func TestServeResult_SignalIsCleanExit(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // simulate a delivered signal
	var errOut bytes.Buffer
	if code := serveResult(context.Canceled, ctx, &errOut); code != 0 {
		t.Errorf("signal shutdown should exit 0, got %d", code)
	}
	if errOut.Len() != 0 {
		t.Errorf("no error line on clean shutdown, got %q", errOut.String())
	}
}

func TestServeResult_RealErrorIsReported(t *testing.T) {
	t.Parallel()
	var errOut bytes.Buffer
	if code := serveResult(context.DeadlineExceeded, context.Background(), &errOut); code != 70 {
		t.Errorf("real error should exit 70, got %d", code)
	}
}

// TestMCP_EndToEndOverHTTP drives the actual CLI entry points: it starts
// `mcp serve --http` on an ephemeral port in a goroutine, then runs
// `mcp ls` and `mcp call` against the URL — the full client↔server path
// through the CLI surface, not just the library.
func TestMCP_EndToEndOverHTTP(t *testing.T) {
	t.Parallel()
	addr := "127.0.0.1:7791"
	serveCtx, stopServe := context.WithCancel(context.Background())
	defer stopServe()
	go func() {
		var w, e bytes.Buffer
		_ = mcpServe(serveCtx, []string{"--http", addr}, &w, &e)
	}()

	// Wait for the listener.
	url := "http://" + addr
	waitForListen(t, addr)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var out, errOut bytes.Buffer
	if code := mcpList(ctx, []string{url}, &out, &errOut); code != 0 {
		t.Fatalf("mcp ls exit %d, stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "math") {
		t.Errorf("ls output missing math: %q", out.String())
	}

	out.Reset()
	errOut.Reset()
	if code := mcpCall(ctx, []string{url, "math", `{"op":"add","a":2,"b":3}`}, &out, &errOut); code != 0 {
		t.Fatalf("mcp call exit %d, stderr=%s", code, errOut.String())
	}
	if strings.TrimSpace(out.String()) != `{"result":5}` {
		t.Errorf("call output = %q, want {\"result\":5}", out.String())
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// waitForListen blocks until addr accepts a TCP connection or the
// deadline passes.
func waitForListen(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server never started listening on %s", addr)
}
