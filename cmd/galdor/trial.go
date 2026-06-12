package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/YasserCR/galdor/pkg/agent"
	"github.com/YasserCR/galdor/pkg/eval"
)

// trial runs a declarative evaluation suite from a YAML file. It maps the
// file to eval.Config — the dataset is data, the subject is an agent
// block, the scorers are the builtin scorers — runs it, prints the report,
// and exits with a CI-friendly code (0 pass, 1 below threshold, 2 setup
// error).
func trial(ctx context.Context, args []string, w io.Writer, errW io.Writer) int {
	fs := flag.NewFlagSet("trial", flag.ContinueOnError)
	fs.SetOutput(errW)
	jsonOut := fs.Bool("json", false, "emit the report as JSON instead of the text summary")
	if err := fs.Parse(args); err != nil {
		return 64
	}
	rest := fs.Args()
	if len(rest) != 1 {
		_, _ = fmt.Fprintf(errW, "trial: expected exactly one suite file\n\n%s\n", trialUsage)
		return 64
	}
	suitePath := rest[0]

	var tc TrialConfig
	if err := loadConfigFile(suitePath, &tc); err != nil {
		_, _ = fmt.Fprintf(errW, "trial: %v\n", err)
		return 2
	}

	cfg, cleanup, err := buildEvalConfig(ctx, tc, errW)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "trial: %v\n", err)
		return 2
	}
	defer cleanup()

	// Resolve the pass threshold up front so an out-of-range value is a
	// setup error, not a silent accept (mirrors eval.RunAndExit).
	threshold := 1.0
	if cfg.MinPass != nil {
		threshold = *cfg.MinPass
		if threshold < 0 || threshold > 1 {
			_, _ = fmt.Fprintf(errW, "trial: min_pass %.2f out of range [0,1]\n", threshold)
			return 2
		}
	}

	report, err := eval.Run(ctx, cfg)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "trial: %v\n", err)
		return 2
	}

	if *jsonOut {
		if werr := report.WriteJSON(w); werr != nil {
			_, _ = fmt.Fprintf(errW, "trial: %v\n", werr)
			return 2
		}
	} else {
		report.PrintSummary(w)
	}

	if !report.Meets(threshold) {
		_, _ = fmt.Fprintf(errW, "trial: pass rate %.1f%% < threshold %.1f%%\n",
			report.PassRate()*100, threshold*100)
		return 1
	}
	return 0
}

const trialUsage = `galdor trial — run a declarative evaluation suite.

Usage:
  galdor trial <suite.yaml> [--json]

  --json   Emit the report as JSON instead of the text summary.

The suite maps to pkg/eval: a dataset of cases, a subject (an agent block),
and one or more scorers (contains, exact, regex, llm_judge). Exit code is
0 (pass), 1 (pass rate below min_pass), or 2 (setup error) — drop it into
CI as a gate.`

// TrialConfig is the top-level schema for a trial suite file.
type TrialConfig struct {
	Version        int           `yaml:"version"`
	Dataset        DatasetBlock  `yaml:"dataset"`
	Subject        AgentBlock    `yaml:"subject"`
	Scorers        []ScorerBlock `yaml:"scorers"`
	MinPass        *float64      `yaml:"min_pass,omitempty"`
	Parallel       int           `yaml:"parallel,omitempty"`
	TimeoutPerCase string        `yaml:"timeout_per_case,omitempty"`
}

func (c *TrialConfig) schemaVersion() int { return c.Version }

// DatasetBlock and CaseBlock mirror eval.Dataset / eval.Case with yaml
// tags (eval's own types carry json tags for the report path).
type DatasetBlock struct {
	Name    string      `yaml:"name"`
	Version string      `yaml:"version,omitempty"`
	Cases   []CaseBlock `yaml:"cases"`
}

type CaseBlock struct {
	ID       string            `yaml:"id"`
	Input    string            `yaml:"input"`
	Expected string            `yaml:"expected,omitempty"`
	Metadata map[string]string `yaml:"metadata,omitempty"`
}

// ScorerBlock declares one scorer. Type selects the implementation; the
// other fields apply per type.
type ScorerBlock struct {
	Type          string `yaml:"type"` // contains | exact | regex | llm_judge
	Name          string `yaml:"name,omitempty"`
	CaseSensitive bool   `yaml:"case_sensitive,omitempty"`
	Pattern       string `yaml:"pattern,omitempty"` // regex

	// llm_judge fields.
	Judge         *AgentBlock `yaml:"judge,omitempty"`
	Rubric        string      `yaml:"rubric,omitempty"`
	PassThreshold float64     `yaml:"pass_threshold,omitempty"`
}

// buildEvalConfig resolves a TrialConfig into a runnable eval.Config plus
// a cleanup func releasing any MCP connections held by the subject's
// tools.
func buildEvalConfig(ctx context.Context, tc TrialConfig, errW io.Writer) (eval.Config, func(), error) {
	if len(tc.Dataset.Cases) == 0 {
		return eval.Config{}, func() {}, fmt.Errorf("dataset has no cases")
	}
	if len(tc.Scorers) == 0 {
		return eval.Config{}, func() {}, fmt.Errorf("at least one scorer is required")
	}

	// Subject: an agent block run per case.
	subjectCfg, cleanup, err := resolveAgentConfig(ctx, tc.Subject, errW)
	if err != nil {
		return eval.Config{}, func() {}, fmt.Errorf("subject: %w", err)
	}
	system, err := effectiveSystem(tc.Subject)
	if err != nil {
		cleanup()
		return eval.Config{}, func() {}, fmt.Errorf("subject: %w", err)
	}
	subject := eval.Subject(func(ctx context.Context, input string) (string, error) {
		if system != "" {
			return agent.Run(ctx, subjectCfg, input, system)
		}
		return agent.Run(ctx, subjectCfg, input)
	})

	scorers, err := resolveScorers(tc.Scorers) //nolint:contextcheck // an llm_judge builds its provider via providerset.New, which takes no ctx
	if err != nil {
		cleanup()
		return eval.Config{}, func() {}, err
	}

	dataset := eval.Dataset{Name: tc.Dataset.Name, Version: tc.Dataset.Version}
	for _, c := range tc.Dataset.Cases {
		dataset.Cases = append(dataset.Cases, eval.Case{
			ID: c.ID, Input: c.Input, Expected: c.Expected, Metadata: c.Metadata,
		})
	}

	cfg := eval.Config{
		Dataset:  dataset,
		Subject:  subject,
		Scorers:  scorers,
		Parallel: tc.Parallel,
		MinPass:  tc.MinPass,
	}
	if tc.TimeoutPerCase != "" {
		d, perr := time.ParseDuration(tc.TimeoutPerCase)
		if perr != nil {
			cleanup()
			return eval.Config{}, func() {}, fmt.Errorf("timeout_per_case: %w", perr)
		}
		cfg.TimeoutPerCase = d
	}
	return cfg, cleanup, nil
}

// resolveScorers maps scorer blocks to eval.Scorer implementations.
func resolveScorers(blocks []ScorerBlock) ([]eval.Scorer, error) {
	out := make([]eval.Scorer, 0, len(blocks))
	for i, b := range blocks {
		s, err := resolveScorer(b)
		if err != nil {
			return nil, fmt.Errorf("scorers[%d]: %w", i, err)
		}
		if b.Name != "" {
			s = eval.Named(b.Name, s)
		}
		out = append(out, s)
	}
	return out, nil
}

func resolveScorer(b ScorerBlock) (eval.Scorer, error) {
	switch b.Type {
	case "contains":
		return eval.Contains{CaseSensitive: b.CaseSensitive}, nil
	case "exact", "exact_match":
		return eval.ExactMatch{CaseSensitive: b.CaseSensitive}, nil
	case "regex":
		if b.Pattern == "" {
			return nil, fmt.Errorf("regex scorer requires a pattern")
		}
		return &eval.Regex{Pattern: b.Pattern}, nil
	case "llm_judge":
		if b.Judge == nil {
			return nil, fmt.Errorf("llm_judge requires a judge block (provider + model)")
		}
		p, err := resolveProvider(*b.Judge)
		if err != nil {
			return nil, fmt.Errorf("llm_judge judge: %w", err)
		}
		return eval.LLMJudge{
			Provider:      p,
			Model:         b.Judge.Model,
			Rubric:        b.Rubric,
			PassThreshold: b.PassThreshold,
		}, nil
	case "":
		return nil, fmt.Errorf("scorer type is required")
	default:
		return nil, fmt.Errorf("unknown scorer type %q (have: contains, exact, regex, llm_judge)", b.Type)
	}
}
