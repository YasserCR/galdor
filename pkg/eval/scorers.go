package eval

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// ExactMatch scores 1.0 when strings.TrimSpace(actual) ==
// strings.TrimSpace(case.Expected) and 0.0 otherwise. Useful for
// deterministic tasks (formatting, classification) where there is
// exactly one correct answer.
type ExactMatch struct {
	// CaseSensitive, when false (the default), folds both sides to
	// lowercase before comparing.
	CaseSensitive bool
}

// Name implements Scorer.
func (ExactMatch) Name() string { return "exact_match" }

// Score implements Scorer.
func (e ExactMatch) Score(_ context.Context, c Case, actual string) (Score, error) {
	a := strings.TrimSpace(actual)
	b := strings.TrimSpace(c.Expected)
	if !e.CaseSensitive {
		a = strings.ToLower(a)
		b = strings.ToLower(b)
	}
	if b == "" {
		return Score{Value: 0, Pass: false, Explanation: "Case.Expected is empty"}, nil
	}
	if a == b {
		return Score{Value: 1, Pass: true}, nil
	}
	return Score{Value: 0, Pass: false, Explanation: "actual != expected"}, nil
}

// Contains scores 1.0 when Case.Expected appears as a substring of
// actual (case-insensitive by default). The most forgiving content
// check; good for "did the model mention X?" tasks.
type Contains struct {
	// CaseSensitive defaults to false.
	CaseSensitive bool
}

// Name implements Scorer.
func (Contains) Name() string { return "contains" }

// Score implements Scorer.
func (c Contains) Score(_ context.Context, cs Case, actual string) (Score, error) {
	a := actual
	b := cs.Expected
	if b == "" {
		return Score{Value: 0, Pass: false, Explanation: "Case.Expected is empty"}, nil
	}
	if !c.CaseSensitive {
		a = strings.ToLower(a)
		b = strings.ToLower(b)
	}
	if strings.Contains(a, b) {
		return Score{Value: 1, Pass: true}, nil
	}
	return Score{Value: 0, Pass: false, Explanation: fmt.Sprintf("missing substring %q", cs.Expected)}, nil
}

// Regex scores 1.0 when actual matches Pattern. Compiled lazily on
// first use and memoized. The lazy compile is guarded by a sync.Once
// so a single *Regex instance is safe to share across the runner's
// Parallel worker goroutines.
type Regex struct {
	Pattern string

	once     sync.Once
	compiled *regexp.Regexp
	compErr  error
}

// Name implements Scorer.
func (r *Regex) Name() string { return "regex" }

// Score implements Scorer.
func (r *Regex) Score(_ context.Context, _ Case, actual string) (Score, error) {
	r.once.Do(func() {
		r.compiled, r.compErr = regexp.Compile(r.Pattern)
	})
	if r.compErr != nil {
		return Score{}, fmt.Errorf("regex: compile %q: %w", r.Pattern, r.compErr)
	}
	if r.compiled.MatchString(actual) {
		return Score{Value: 1, Pass: true}, nil
	}
	return Score{Value: 0, Pass: false, Explanation: fmt.Sprintf("no match for /%s/", r.Pattern)}, nil
}

// ScorerFunc adapts a plain function to the Scorer interface. The
// returned Scorer reports the supplied name.
func ScorerFunc(name string, fn func(ctx context.Context, c Case, actual string) (Score, error)) Scorer {
	return scorerFuncAdapter{name: name, fn: fn}
}

type scorerFuncAdapter struct {
	name string
	fn   func(ctx context.Context, c Case, actual string) (Score, error)
}

func (s scorerFuncAdapter) Name() string { return s.name }
func (s scorerFuncAdapter) Score(ctx context.Context, c Case, actual string) (Score, error) {
	return s.fn(ctx, c, actual)
}

// LLMJudge uses a second LLM to score actual against the case's
// Expected (or just against a Rubric for open-ended tasks). The
// judge is asked to reply with a single integer 0..100, which is
// rescaled to [0, 1]; values >= PassThreshold (default 0.7) count
// as a pass.
//
// LLMJudge is intentionally a thin wrapper: callers swap the
// Provider / Model independently from the system under test so the
// judge can be a stronger (or smaller) model than the Subject.
type LLMJudge struct {
	// Provider serves the judge LLM. Required.
	Provider provider.Provider

	// Model is the judge model ID. Required.
	Model string

	// Rubric describes the evaluation criteria. Embedded verbatim
	// in the judge's system prompt. Keep it tight and specific:
	// "Score 100 only when the answer is factually correct AND
	// concise" beats "score the quality".
	Rubric string

	// NameOverride lets callers distinguish multiple LLMJudge
	// scorers in the same report (e.g. "judge_correctness" vs
	// "judge_style"). When empty, Name() returns "llm_judge".
	NameOverride string

	// PassThreshold is the minimum normalized score (in [0, 1])
	// that counts as a pass. Default 0.7.
	PassThreshold float64

	// MaxTokens caps the judge's reply length. Default 32 — enough
	// for "85" plus a short rationale.
	MaxTokens int
}

// Name implements Scorer.
func (j LLMJudge) Name() string {
	if j.NameOverride != "" {
		return j.NameOverride
	}
	return "llm_judge"
}

// Score implements Scorer.
func (j LLMJudge) Score(ctx context.Context, c Case, actual string) (Score, error) {
	if j.Provider == nil {
		return Score{}, errors.New("eval: LLMJudge.Provider is nil")
	}
	if j.Model == "" {
		return Score{}, errors.New("eval: LLMJudge.Model is empty")
	}
	threshold := j.PassThreshold
	if threshold <= 0 {
		threshold = 0.7
	}
	maxTok := j.MaxTokens
	if maxTok <= 0 {
		maxTok = 32
	}

	sys := strings.TrimSpace(`You are an evaluation judge. Read the candidate answer and rate it on a 0..100 scale where:
  100 = perfect, exactly matches what's expected
   50 = partially correct, on-topic but flawed
    0 = irrelevant or wrong
Respond with ONLY a single integer between 0 and 100. No prose. No punctuation. No code fences.`)
	if j.Rubric != "" {
		sys += "\n\nEvaluation criteria:\n" + j.Rubric
	}
	user := buildJudgeUserPrompt(c, actual)

	resp, err := j.Provider.Generate(ctx, provider.Request{
		Model:     j.Model,
		MaxTokens: &maxTok,
		Messages: []schema.Message{
			schema.SystemMessage(sys),
			schema.UserMessage(user),
		},
	})
	if err != nil {
		return Score{}, fmt.Errorf("llm_judge: %w", err)
	}
	raw := resp.Message.Text()
	n, ok := parseJudgeScore(raw)
	if !ok {
		return Score{Value: 0, Pass: false, Explanation: "could not parse score from judge reply: " + raw}, nil
	}
	value := float64(n) / 100.0
	if value < 0 {
		value = 0
	} else if value > 1 {
		value = 1
	}
	return Score{
		Value:       value,
		Pass:        value >= threshold,
		Explanation: fmt.Sprintf("judge raw=%q score=%d/100 threshold=%.2f", raw, n, threshold),
	}, nil
}

// buildJudgeUserPrompt formats the case + actual for the judge.
// Expected is only shown when it's set.
func buildJudgeUserPrompt(c Case, actual string) string {
	var b strings.Builder
	b.WriteString("INPUT:\n")
	b.WriteString(c.Input)
	if c.Expected != "" {
		b.WriteString("\n\nEXPECTED REFERENCE:\n")
		b.WriteString(c.Expected)
	}
	b.WriteString("\n\nCANDIDATE ANSWER:\n")
	b.WriteString(actual)
	return b.String()
}

// judgeScoreOutOf matches a number explicitly presented as a score:
// "88/100" or "88%". These are strong, unambiguous signals so they
// take priority over loose integer tokens scattered through prose.
var judgeScoreOutOf = regexp.MustCompile(`(\d{1,3})\s*(?:/\s*100|%)`)

// judgeIntToken matches standalone integer tokens (not glued to other
// digits or to a slash, e.g. not the "2" in "v2" — but "scored 88" or
// "answer: 88." both qualify).
// judgeIntToken matches a STANDALONE integer run. The \b anchors exclude
// digits embedded in a word ("v2", "gpt4", "Option2") — without them \d+
// matched those too, contradicting the doc above and turning "Option 2" / a
// model name into a bogus score.
var judgeIntToken = regexp.MustCompile(`\b\d+\b`)

// parseJudgeScore extracts the judge's intended score in [0, 100] from
// raw. The judge is instructed to reply with a bare integer, so:
//
//   - Fast path: the whole trimmed string is an integer -> use it.
//   - Otherwise prefer an explicit "N/100" or "N%" form.
//   - Otherwise fall back to standalone integer tokens, but only when
//     they are unambiguous (every token resolves to the same value).
//     Prose like "matches reference 95 ... score 100" or "version 2
//     answer scored 88" yields conflicting tokens; rather than guess
//     wrong (a false pass/fail) we refuse and report a parse failure.
func parseJudgeScore(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	// Fast path: the whole thing is the number.
	if n, err := strconv.Atoi(raw); err == nil {
		return clampScore(n), true
	}
	// Explicit "N/100" or "N%" form wins. If several appear and they
	// disagree, that's ambiguous -> refuse.
	if m := judgeScoreOutOf.FindAllStringSubmatch(raw, -1); len(m) > 0 {
		first, _ := strconv.Atoi(m[0][1])
		for _, g := range m[1:] {
			if v, _ := strconv.Atoi(g[1]); v != first {
				return 0, false
			}
		}
		return clampScore(first), true
	}
	// Fall back to standalone integer tokens, but only if they all
	// agree; conflicting numbers in prose are not safe to guess from.
	toks := judgeIntToken.FindAllString(raw, -1)
	if len(toks) == 0 {
		return 0, false
	}
	first, err := strconv.Atoi(toks[0])
	if err != nil {
		return 0, false
	}
	for _, t := range toks[1:] {
		v, err := strconv.Atoi(t)
		if err != nil || v != first {
			return 0, false
		}
	}
	return clampScore(first), true
}

func clampScore(n int) int {
	if n < 0 {
		return 0
	}
	if n > 100 {
		return 100
	}
	return n
}
