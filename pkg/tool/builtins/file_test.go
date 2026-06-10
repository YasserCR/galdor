package builtins

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempFile(t *testing.T, dir, name string, body []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFileRead_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTempFile(t, dir, "hello.txt", []byte("hello world"))

	tt := MustNewFileReadTool(FileReadOptions{BaseDir: dir})
	out, err := tt.Execute(context.Background(), FileReadIn{Path: "hello.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "hello world" {
		t.Errorf("Content = %q", out.Content)
	}
	if out.Size != 11 {
		t.Errorf("Size = %d", out.Size)
	}
}

func TestFileRead_Truncates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTempFile(t, dir, "big.txt", []byte(strings.Repeat("x", 5000)))

	tt := MustNewFileReadTool(FileReadOptions{BaseDir: dir, MaxBytes: 100})
	out, err := tt.Execute(context.Background(), FileReadIn{Path: "big.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Truncated || len(out.Content) != 100 {
		t.Errorf("truncated=%v len=%d", out.Truncated, len(out.Content))
	}
	if out.Size != 5000 {
		t.Errorf("Size should report the on-disk size, got %d", out.Size)
	}
}

func TestFileRead_RejectsPathEscape(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTempFile(t, dir, "ok.txt", []byte("inside"))
	// Create a file OUTSIDE the BaseDir.
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}

	tt := MustNewFileReadTool(FileReadOptions{BaseDir: dir})
	for _, bad := range []string{"../secret.txt", filepath.Join("..", "..", "etc", "passwd")} {
		_, err := tt.Execute(context.Background(), FileReadIn{Path: bad})
		if !errors.Is(err, ErrPathEscape) {
			t.Errorf("path %q: err = %v, want ErrPathEscape", bad, err)
		}
	}
}

func TestFileRead_RejectsSymlinkByDefault(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := writeTempFile(t, dir, "real.txt", []byte("hello"))
	linkPath := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, linkPath); err != nil {
		t.Skipf("symlinks unsupported on this filesystem: %v", err)
	}

	tt := MustNewFileReadTool(FileReadOptions{BaseDir: dir})
	_, err := tt.Execute(context.Background(), FileReadIn{Path: "link.txt"})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("err = %v", err)
	}
}

func TestFileRead_FollowSymlinksOptIn(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := writeTempFile(t, dir, "real.txt", []byte("hello"))
	linkPath := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, linkPath); err != nil {
		t.Skipf("symlinks unsupported on this filesystem: %v", err)
	}

	tt := MustNewFileReadTool(FileReadOptions{BaseDir: dir, FollowSymlinks: true})
	out, err := tt.Execute(context.Background(), FileReadIn{Path: "link.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "hello" {
		t.Errorf("Content = %q", out.Content)
	}
}

func TestFileRead_RejectsDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	subdir := filepath.Join(dir, "sub")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	tt := MustNewFileReadTool(FileReadOptions{BaseDir: dir})
	_, err := tt.Execute(context.Background(), FileReadIn{Path: "sub"})
	if err == nil {
		t.Fatal("expected error reading a directory")
	}
}

func TestFileRead_RejectsMissingPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tt := MustNewFileReadTool(FileReadOptions{BaseDir: dir})
	_, err := tt.Execute(context.Background(), FileReadIn{Path: "does-not-exist.txt"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFileRead_RequiresPath(t *testing.T) {
	t.Parallel()
	tt := MustNewFileReadTool(FileReadOptions{})
	if _, err := tt.Execute(context.Background(), FileReadIn{}); err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestFileRead_NoBaseDirAllowsAbsolute(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := writeTempFile(t, dir, "abs.txt", []byte("hi"))

	tt := MustNewFileReadTool(FileReadOptions{}) // no BaseDir confinement
	out, err := tt.Execute(context.Background(), FileReadIn{Path: target})
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "hi" {
		t.Errorf("Content = %q", out.Content)
	}
}

// Regression for audit H4: an intermediate symlink directory inside
// BaseDir that points OUTSIDE it must not let a read escape. The final
// path component is a regular file (so the symlink gate doesn't catch it)
// and the lexical path stays inside BaseDir, so only real-path resolution
// catches the escape.
func TestFileRead_RejectsIntermediateSymlinkEscape(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("TOPSECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unsupported on this filesystem: %v", err)
	}

	tt := MustNewFileReadTool(FileReadOptions{BaseDir: base})
	out, err := tt.Execute(context.Background(), FileReadIn{Path: "link/secret.txt"})
	if err == nil {
		t.Fatalf("intermediate symlink escaped BaseDir (regression of H4): read %q", out.Content)
	}
	if !errors.Is(err, ErrPathEscape) {
		t.Errorf("err = %v, want ErrPathEscape", err)
	}
}
