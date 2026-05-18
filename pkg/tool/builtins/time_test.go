package builtins

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestTime_NowDefault(t *testing.T) {
	t.Parallel()
	tt := MustNewTimeTool()
	out, err := tt.Execute(context.Background(), TimeIn{Operation: "now"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Unix == 0 {
		t.Error("Unix should be populated for 'now'")
	}
	if out.TZ != "UTC" {
		t.Errorf("default TZ should be UTC, got %q", out.TZ)
	}
}

func TestTime_NowWithLayoutAndTZ(t *testing.T) {
	t.Parallel()
	tt := MustNewTimeTool()
	out, err := tt.Execute(context.Background(), TimeIn{
		Operation: "now",
		Layout:    "2006-01-02",
		TZ:        "America/Guayaquil",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.TZ != "America/Guayaquil" {
		t.Errorf("TZ = %q", out.TZ)
	}
	if len(out.Formatted) != 10 {
		t.Errorf("Formatted should be a YYYY-MM-DD string, got %q", out.Formatted)
	}
}

func TestTime_ParseRFC3339Default(t *testing.T) {
	t.Parallel()
	tt := MustNewTimeTool()
	want := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC).Unix()
	out, err := tt.Execute(context.Background(), TimeIn{
		Operation: "parse",
		Value:     "2026-05-17T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Unix != want {
		t.Errorf("Unix = %d, want %d", out.Unix, want)
	}
}

func TestTime_FormatFromUnix(t *testing.T) {
	t.Parallel()
	tt := MustNewTimeTool()
	out, err := tt.Execute(context.Background(), TimeIn{
		Operation: "format",
		Value:     "1747494000",
		Layout:    "2006-01-02 15:04",
		TZ:        "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Unix != 1747494000 {
		t.Errorf("Unix = %d", out.Unix)
	}
	if !strings.HasPrefix(out.Formatted, "2025-05-17") {
		t.Errorf("Formatted = %q", out.Formatted)
	}
}

func TestTime_ParseRequiresValue(t *testing.T) {
	t.Parallel()
	tt := MustNewTimeTool()
	if _, err := tt.Execute(context.Background(), TimeIn{Operation: "parse"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestTime_FormatRequiresIntegerValue(t *testing.T) {
	t.Parallel()
	tt := MustNewTimeTool()
	if _, err := tt.Execute(context.Background(), TimeIn{Operation: "format", Value: "not-a-number"}); err == nil {
		t.Fatal("expected error")
	}
	if _, err := tt.Execute(context.Background(), TimeIn{Operation: "format"}); err == nil {
		t.Fatal("expected error for empty value")
	}
}

func TestTime_UnknownTZRejected(t *testing.T) {
	t.Parallel()
	tt := MustNewTimeTool()
	if _, err := tt.Execute(context.Background(), TimeIn{Operation: "now", TZ: "Atlantis/Lemuria"}); err == nil {
		t.Fatal("expected error for unknown TZ")
	}
}

func TestTime_UnknownOperationRejected(t *testing.T) {
	t.Parallel()
	tt := MustNewTimeTool()
	if _, err := tt.Execute(context.Background(), TimeIn{Operation: "bogus"}); err == nil {
		t.Fatal("expected error for unknown op")
	}
}

func TestTime_NewTimeToolReturnsNoError(t *testing.T) {
	t.Parallel()
	if _, err := NewTimeTool(); err != nil {
		t.Fatal(err)
	}
}
