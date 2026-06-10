package jsonschema

import "testing"

// Regression for the embedded-recursion stack overflow (audit C3).
// collectFields promotes embedded struct fields by recursing directly
// rather than through buildSchema, so it must carry the same cycle
// guard. The trigger is narrow but fatal: an EXPORTED embedded field
// that (transitively) embeds itself overflowed the stack and aborted
// the process; an unexported one is skipped before promotion and is the
// control case.

// control: unexported embedded pointer is skipped — no recursion, no schema cycle.
type selfEmbedUnexported struct {
	*selfEmbedUnexported        //nolint:unused // the embedded self-pointer is the point: it must be skipped, not dereferenced
	Name                 string `json:"name"`
}

// SelfEmbedExported has an EXPORTED embedded pointer to itself.
type SelfEmbedExported struct {
	*SelfEmbedExported
	Name string `json:"name"`
}

// Mutual embedding: A embeds B embeds *A, all exported.
type EmbedA struct {
	EmbedB
	X string `json:"x"`
}
type EmbedB struct {
	*EmbedA
	Y string `json:"y"`
}

// Legitimate (non-recursive) embedding must keep working: Derived embeds
// an exported Base, and Base's fields are promoted to the parent object.
type PromotedBase struct {
	BaseField string `json:"base_field"`
}
type promotedDerived struct {
	PromotedBase
	Own string `json:"own"`
}

func TestFor_UnexportedSelfEmbedSkipped(t *testing.T) {
	if _, err := For[selfEmbedUnexported](); err != nil {
		t.Fatalf("unexported embedded self should be skipped, got error: %v", err)
	}
}

func TestFor_ExportedSelfEmbedRejected(t *testing.T) {
	_, err := For[SelfEmbedExported]()
	if err == nil {
		t.Fatal("exported self-embed must be rejected with a recursive-type error (regression of C3)")
	}
}

func TestFor_MutualEmbedRejected(t *testing.T) {
	_, err := For[EmbedA]()
	if err == nil {
		t.Fatal("mutual embedding must be rejected with a recursive-type error (regression of C3)")
	}
}

func TestFor_LegitEmbeddedPromotionStillWorks(t *testing.T) {
	s, err := For[promotedDerived]()
	if err != nil {
		t.Fatalf("non-recursive embedded promotion should succeed, got: %v", err)
	}
	if s.Properties["base_field"] == nil {
		t.Fatal("embedded base field was not promoted to the parent schema")
	}
	if s.Properties["own"] == nil {
		t.Fatal("own field missing from schema")
	}
}
