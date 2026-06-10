package eval

import "testing"

// Regression (audit low): the LLM-judge score parser must not read digits
// EMBEDDED in words (a model name like "gpt4", "v2"). With \b anchors those
// are ignored, so a reply that mentions them plus one real score parses to
// that score instead of refusing on a phantom conflict.
func TestParseJudgeScore_IgnoresEmbeddedDigits(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw    string
		wantN  int
		wantOK bool
	}{
		{"85", 85, true}, // bare integer
		{"Based on gpt4 analysis, score 85", 85, true}, // "gpt4" digit ignored
		{"v2 model answered; final 90", 90, true},      // "v2" digit ignored
		{"Option 2", 2, true},                          // standalone 2 is a real token
		{"matches 95 ... but only 70", 0, false},       // genuine conflict -> refuse
		{"no number here", 0, false},                   // nothing parseable
	}
	for _, c := range cases {
		n, ok := parseJudgeScore(c.raw)
		if ok != c.wantOK || (ok && n != c.wantN) {
			t.Errorf("parseJudgeScore(%q) = (%d, %v), want (%d, %v)", c.raw, n, ok, c.wantN, c.wantOK)
		}
	}
}
