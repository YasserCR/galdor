package eval

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

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
// first use and memoized.
type Regex struct {
	Pattern string

	compiled *regexp.Regexp
}

// Name implements Scorer.
func (r *Regex) Name() string { return "regex" }

// Score implements Scorer.
func (r *Regex) Score(_ context.Context, _ Case, actual string) (Score, error) {
	if r.compiled == nil {
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			return Score{}, fmt.Errorf("regex: compile %q: %w", r.Pattern, err)
		}
		r.compiled = re
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

// parseJudgeScore extracts the first integer in [0, 100] from raw.
// LLMs sometimes wrap the answer in quotes or add a trailing comma
// despite instructions; this is forgiving of that.
func parseJudgeScore(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	// Fast path: the whole thing is the number.
	if n, err := strconv.Atoi(raw); err == nil {
		return clampScore(n), true
	}
	// Scan for the first contiguous digit run.
	start := -1
	for i, r := range raw {
		if r >= '0' && r <= '9' {
			if start == -1 {
				start = i
			}
			continue
		}
		if start != -1 {
			if n, err := strconv.Atoi(raw[start:i]); err == nil {
				return clampScore(n), true
			}
			start = -1
		}
	}
	if start != -1 {
		if n, err := strconv.Atoi(raw[start:]); err == nil {
			return clampScore(n), true
		}
	}
	return 0, false
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
