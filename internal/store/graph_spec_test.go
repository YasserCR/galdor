package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestGraphSpec_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := Open(context.Background(), filepath.Join(dir, "specs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	want := `{"entry":"a","nodes":[{"name":"a"}],"static_edges":[],"conditional_edges":[]}`
	if setErr := s.SetGraphSpec(context.Background(), "run-1", []byte(want)); setErr != nil {
		t.Fatal(setErr)
	}
	got, err := s.GetGraphSpec(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGraphSpec_Replace(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := Open(context.Background(), filepath.Join(dir, "specs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	_ = s.SetGraphSpec(context.Background(), "run-1", []byte(`{"first":1}`))
	if err := s.SetGraphSpec(context.Background(), "run-1", []byte(`{"second":2}`)); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetGraphSpec(context.Background(), "run-1")
	if got != `{"second":2}` {
		t.Errorf("expected replacement, got %q", got)
	}
}

func TestGraphSpec_MissingReturnsEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := Open(context.Background(), filepath.Join(dir, "specs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	got, err := s.GetGraphSpec(context.Background(), "never-recorded")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestGraphSpec_EmptyRunID(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := Open(context.Background(), filepath.Join(dir, "specs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if err := s.SetGraphSpec(context.Background(), "", []byte(`{}`)); err == nil {
		t.Error("expected error for empty runID")
	}
	if _, err := s.GetGraphSpec(context.Background(), ""); err == nil {
		t.Error("expected error for empty runID")
	}
}
