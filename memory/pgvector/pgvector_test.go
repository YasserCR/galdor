package pgvector

import (
	"strings"
	"testing"
)

func TestFormatVector(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   []float32
		want string
	}{
		{nil, "[]"},
		{[]float32{}, "[]"},
		{[]float32{1, 2.5, -3}, "[1,2.5,-3]"},
	}
	for _, tc := range cases {
		if got := formatVector(tc.in); got != tc.want {
			t.Errorf("formatVector(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseVector_Roundtrip(t *testing.T) {
	t.Parallel()
	in := []float32{0.1, -0.25, 3.14159, 0}
	s := formatVector(in)
	out, err := parseVector(s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("len = %d, want %d", len(out), len(in))
	}
	for i := range in {
		if out[i] != in[i] {
			t.Errorf("[%d] = %v, want %v", i, out[i], in[i])
		}
	}
}

func TestParseVector_Empty(t *testing.T) {
	t.Parallel()
	// Empty forms are legitimately "no vector": nil, NO error.
	for _, s := range []string{"[]", ""} {
		got, err := parseVector(s)
		if got != nil || err != nil {
			t.Errorf("parseVector(%q) = (%v, %v), want (nil, nil)", s, got, err)
		}
	}
}

func TestParseVector_RejectsMalformed(t *testing.T) {
	t.Parallel()
	// Malformed input must ERROR, not silently yield nil.
	for _, s := range []string{"[1,abc,3]", "not a vector"} {
		got, err := parseVector(s)
		if err == nil {
			t.Errorf("parseVector(%q) = (%v, nil), want an error", s, got)
		}
	}
}

func TestIsSafeIdent(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"":              false,
		"chunks":        true,
		"galdor_chunks": true,
		"abc123":        true,
		"_underscore":   true,
		"Chunks":        false, // upper-case rejected to keep DDL quoting-free
		"chunks-table":  false,
		"chunks table":  false,
		"chunks; DROP":  false,
		"chunks\"quote": false,
	}
	for in, want := range cases {
		if got := isSafeIdent(in); got != want {
			t.Errorf("isSafeIdent(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestBuildRetrieveSQL_NoFilter(t *testing.T) {
	t.Parallel()
	s := &Store{table: "chunks", dim: 3}
	sql, args := s.buildRetrieveSQL(memoryQueryWithEmbedding([]float32{1, 0, 0}, 5, nil), 5)

	if !strings.Contains(sql, "FROM chunks") {
		t.Errorf("missing FROM clause: %s", sql)
	}
	if strings.Contains(sql, "WHERE") {
		t.Errorf("did not expect WHERE clause when filter is empty: %s", sql)
	}
	if !strings.Contains(sql, "ORDER BY distance ASC") {
		t.Errorf("missing ORDER BY: %s", sql)
	}
	// $1 = embedding, $2 = limit.
	if !strings.Contains(sql, "$1::vector") {
		t.Errorf("vector placeholder missing: %s", sql)
	}
	if !strings.Contains(sql, "$2") {
		t.Errorf("limit placeholder missing: %s", sql)
	}
	if len(args) != 2 || args[1] != 5 {
		t.Errorf("args = %+v, want [vec, 5]", args)
	}
}

func TestBuildRetrieveSQL_WithFilter(t *testing.T) {
	t.Parallel()
	s := &Store{table: "chunks", dim: 3}
	sql, args := s.buildRetrieveSQL(
		memoryQueryWithEmbedding([]float32{1, 0, 0}, 3, map[string]string{"lang": "es"}),
		3,
	)
	if !strings.Contains(sql, "WHERE metadata @> $2::jsonb") {
		t.Errorf("missing JSONB containment filter: %s", sql)
	}
	if !strings.Contains(sql, "LIMIT $3") {
		t.Errorf("limit placeholder should shift to $3: %s", sql)
	}
	if len(args) != 3 {
		t.Fatalf("args = %+v, want 3 elements", args)
	}
	if fs, ok := args[1].(string); !ok || !strings.Contains(fs, `"lang":"es"`) {
		t.Errorf("filter arg = %+v", args[1])
	}
	if args[2] != 3 {
		t.Errorf("limit arg = %+v, want 3", args[2])
	}
}
