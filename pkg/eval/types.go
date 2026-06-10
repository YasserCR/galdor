package eval

import (
	"context"
	"time"
)

// Case is one test point in a Dataset: an input fed to the Subject
// and (optionally) an expected reference output used by Scorers.
type Case struct {
	// ID uniquely identifies the case within a Dataset. Stable IDs
	// make report diffs across runs meaningful.
	ID string `json:"id"`

	// Input is the string fed to Subject.
	Input string `json:"input"`

	// Expected is the reference answer used by content-comparison
	// scorers (ExactMatch, Contains). Optional; LLM-judge scorers
	// often work without it.
	Expected string `json:"expected,omitempty"`

	// Metadata carries free-form key/value data through the run.
	// Useful for tagging cases by category, difficulty, or origin.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Dataset is a versioned collection of Cases. The Version field
// shows up in every Report so a regression can be attributed to
// "model changed" vs. "dataset changed".
type Dataset struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Cases   []Case `json:"cases"`
}

// Subject is the system under evaluation. It takes a single input
// string and returns the agent's text answer (or an error). Wrap a
// ReAct runnable, a Supervisor, a Plan-and-Execute pipeline, or any
// other agent shape behind this signature.
type Subject func(ctx context.Context, input string) (string, error)

// Score is the result of one Scorer applied to one Case + actual
// output. Value is normalized to [0, 1]; Pass is the discrete
// verdict (typically Value >= scorer-specific threshold).
type Score struct {
	Value       float64 `json:"value"`
	Pass        bool    `json:"pass"`
	Explanation string  `json:"explanation,omitempty"`
}

// Scorer rates an agent's output against the case's expected value
// (and possibly other criteria the Scorer carries internally).
//
// Name uniquely identifies the scorer in a Report's aggregates so
// multiple instances of the same type (e.g. two LLMJudge scorers
// with different rubrics) can coexist.
type Scorer interface {
	Name() string
	Score(ctx context.Context, c Case, actual string) (Score, error)
}

// Named wraps any Scorer to report a different Name(), so two scorers that
// would otherwise collide (same underlying type/name) can coexist in one
// Config.Scorers. Works for every scorer type, unlike LLMJudge.NameOverride
// which only the judge exposes.
func Named(name string, s Scorer) Scorer {
	return namedScorer{name: name, inner: s}
}

type namedScorer struct {
	name  string
	inner Scorer
}

func (n namedScorer) Name() string { return n.name }
func (n namedScorer) Score(ctx context.Context, c Case, actual string) (Score, error) {
	return n.inner.Score(ctx, c, actual)
}

// CaseResult is the per-case slice of a Report.
type CaseResult struct {
	Case     Case             `json:"case"`
	Actual   string           `json:"actual,omitempty"`
	Err      string           `json:"err,omitempty"`
	Scores   map[string]Score `json:"scores,omitempty"`
	Pass     bool             `json:"pass"`
	Duration time.Duration    `json:"duration_ns"`
}

// Aggregate summarizes one scorer's results across all cases.
type Aggregate struct {
	Scorer string  `json:"scorer"`
	Mean   float64 `json:"mean"`
	Pass   int     `json:"pass"`
	Fail   int     `json:"fail"`
}

// Report is the output of an Evaluator run.
type Report struct {
	Dataset    string               `json:"dataset"`
	Version    string               `json:"version"`
	StartedAt  time.Time            `json:"started_at"`
	Duration   time.Duration        `json:"duration_ns"`
	Cases      []CaseResult         `json:"cases"`
	Aggregates map[string]Aggregate `json:"aggregates"`

	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Errored int `json:"errored"`
}

// PassRate returns the fraction of cases that passed every scorer,
// in [0, 1]. Errored cases count as failures.
func (r *Report) PassRate() float64 {
	total := len(r.Cases)
	if total == 0 {
		return 0
	}
	return float64(r.Passed) / float64(total)
}

// Meets reports whether the report's PassRate is >= minPass.
// Convenience for CI gates: `if !report.Meets(0.9) { os.Exit(1) }`.
func (r *Report) Meets(minPass float64) bool {
	return r.PassRate() >= minPass
}
