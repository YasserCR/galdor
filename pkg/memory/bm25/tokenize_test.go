package bm25

import (
	"reflect"
	"slices"
	"testing"
)

func contains(tokens []string, want string) bool {
	return slices.Contains(tokens, want)
}

func TestCodeTokenizer_KeepsCompoundAndParts(t *testing.T) {
	cases := []struct {
		in    string
		parts []string // must all be present
	}{
		{"customer_id", []string{"customer_id", "customer", "id"}},
		{"customerId", []string{"customer", "id"}},
		{"mrr_amount", []string{"mrr_amount", "mrr", "amount"}},
		{"HTTPServer", []string{"http", "server"}},
		{"past_due", []string{"past_due", "past", "due"}},
	}
	for _, c := range cases {
		got := Tokenize(c.in)
		for _, want := range c.parts {
			if !contains(got, want) {
				t.Errorf("Tokenize(%q) = %v, missing %q", c.in, got, want)
			}
		}
	}
}

func TestCodeTokenizer_PlainProseIsClean(t *testing.T) {
	// Ordinary words must not spawn extra tokens.
	got := Tokenize("Monthly Recurring Revenue")
	want := []string{"monthly", "recurring", "revenue"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Tokenize = %v, want %v", got, want)
	}
}

func TestCodeTokenizer_SplitsOnPunctuation(t *testing.T) {
	// A resource URI folds into searchable parts.
	got := Tokenize("warehouse://acme-prod/analytics/subscriptions")
	for _, want := range []string{"warehouse", "acme", "prod", "analytics", "subscriptions"} {
		if !contains(got, want) {
			t.Errorf("missing %q in %v", want, got)
		}
	}
}
