package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuf is a goroutine-safe io.Writer wrapper around bytes.Buffer.
// runUI writes from the server goroutine while the test goroutine
// scans for the listen address; bytes.Buffer is not safe for that.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestRunUI_BadDB exercises the early-exit path: a directory that
// can't be created is a fast way to force store.Open to fail.
func TestRunUI_BadDB(t *testing.T) {
	t.Setenv("GALDOR_DB", filepath.Join(t.TempDir(), "no-such-dir", "subdir", "x.db"))
	var out, errOut bytes.Buffer
	code := runUI(context.Background(), []string{"--addr", "127.0.0.1:0"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected non-zero exit for unreachable db path")
	}
	if !strings.Contains(errOut.String(), "ui:") {
		t.Errorf("expected error prefix; got %q", errOut.String())
	}
}

// TestRunUI_StartsAndServes drives the happy path: open a real
// store, start the server on an ephemeral port, then cancel and
// confirm clean exit.
func TestRunUI_StartsAndServes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "traces.db")
	t.Setenv("GALDOR_DB", dbPath)
	// Pre-create the db so the server has something to open; the
	// store creates the schema on Open.
	if f, err := os.Create(dbPath); err != nil {
		t.Fatal(err)
	} else {
		_ = f.Close()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var out, errOut syncBuf
	done := make(chan int, 1)
	go func() {
		done <- runUI(ctx, []string{"--addr", "127.0.0.1:0"}, &out, &errOut)
	}()

	// Wait until the server logs its address.
	deadline := time.After(3 * time.Second)
	var addr string
	for {
		if strings.Contains(out.String(), "http://") {
			line := out.String()
			i := strings.Index(line, "http://")
			j := strings.IndexAny(line[i:], "\n")
			if j < 0 {
				j = len(line) - i
			}
			addr = strings.TrimPrefix(line[i:i+j], "http://")
			break
		}
		select {
		case <-deadline:
			t.Fatalf("server never reported address; stdout=%q stderr=%q", out.String(), errOut.String())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz status = %d", resp.StatusCode)
	}

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit code = %d, want 0; stderr=%q", code, errOut.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not exit after ctx cancel")
	}
}

func TestIsLoopbackAddr(t *testing.T) {
	t.Parallel()
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:7777", true},
		{"127.0.0.1", true},
		{"localhost:7777", true},
		{"[::1]:7777", true},
		{"::1", true},
		{"0.0.0.0:7777", false},
		{":7777", false},
		{"192.168.1.10:7777", false},
		{"example.com:7777", false},
	}
	for _, c := range cases {
		if got := isLoopbackAddr(c.addr); got != c.want {
			t.Errorf("isLoopbackAddr(%q) = %v, want %v", c.addr, got, c.want)
		}
	}
}
