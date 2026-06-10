package eval

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Config bundles everything Evaluator.Run needs.
type Config struct {
	// Dataset is the test corpus. Required.
	Dataset Dataset

	// Subject is the system under test. Required.
	Subject Subject

	// Scorers rate the Subject's output on each case. Must be
	// non-empty; a case passes when EVERY scorer's Pass is true.
	Scorers []Scorer

	// Parallel caps the number of cases evaluated concurrently.
	// Default 4. Set to 1 for fully sequential execution.
	Parallel int

	// MinPass is the pass-rate threshold (in [0,1]) used by RunAndExit.
	// nil means "use the default" (1.0 — every case must pass). A non-nil
	// pointer is honored literally, so MinPass = eval.Threshold(0) expresses
	// "accept any pass rate" (report-only), which a bare 0 could not. Values
	// outside [0,1] are rejected by RunAndExit as a setup error.
	MinPass *float64

	// TimeoutPerCase, when > 0, derives a per-case context with
	// that deadline. A timeout counts as an error (not a fail) so
	// it can be diagnosed separately.
	TimeoutPerCase time.Duration
}

// Run executes the dataset against the Subject and returns a Report.
// Cases run concurrently up to Parallel; Scorers are applied
// sequentially per case (cheap operations + LLM judges that hit the
// network amortize well at the case level).
func Run(ctx context.Context, cfg Config) (*Report, error) {
	if cfg.Subject == nil {
		return nil, errors.New("eval: Config.Subject is nil")
	}
	if len(cfg.Scorers) == 0 {
		return nil, errors.New("eval: Config.Scorers must be non-empty")
	}
	if len(cfg.Dataset.Cases) == 0 {
		return nil, errors.New("eval: Dataset.Cases is empty")
	}
	// Scorer names key the per-case and aggregate maps; duplicates
	// would silently overwrite each other and corrupt the report.
	seenNames := make(map[string]struct{}, len(cfg.Scorers))
	for _, s := range cfg.Scorers {
		name := s.Name()
		if _, dup := seenNames[name]; dup {
			return nil, fmt.Errorf("eval: duplicate scorer name %q (wrap one with eval.Named to disambiguate)", name)
		}
		seenNames[name] = struct{}{}
	}
	parallel := cfg.Parallel
	if parallel <= 0 {
		parallel = 4
	}

	report := &Report{
		Dataset:    cfg.Dataset.Name,
		Version:    cfg.Dataset.Version,
		StartedAt:  time.Now().UTC(),
		Cases:      make([]CaseResult, len(cfg.Dataset.Cases)),
		Aggregates: map[string]Aggregate{},
	}

	// Worker pool: each worker pulls a case index and writes the
	// result back into the report's pre-sized slice at the same
	// index. Ordering is preserved without extra synchronization.
	queue := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < parallel; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range queue {
				report.Cases[idx] = evalOne(ctx, cfg, cfg.Dataset.Cases[idx])
			}
		}()
	}
	for i := range cfg.Dataset.Cases {
		// Workers always drain the queue, so the send can't deadlock;
		// the ctx.Done() arm just lets the producer stop promptly on
		// cancellation. Every index is still enqueued (or short-cut by
		// the select) and evalOne records cancelled cases as Errored.
		select {
		case queue <- i:
		case <-ctx.Done():
			queue <- i
		}
	}
	close(queue)
	wg.Wait()

	report.Duration = time.Since(report.StartedAt)
	tallyReport(report, cfg)
	return report, nil
}

func evalOne(parentCtx context.Context, cfg Config, c Case) CaseResult {
	start := time.Now()
	ctx := parentCtx
	var cancel context.CancelFunc
	if cfg.TimeoutPerCase > 0 {
		ctx, cancel = context.WithTimeout(parentCtx, cfg.TimeoutPerCase)
		defer cancel()
	}

	result := CaseResult{Case: c, Scores: map[string]Score{}}

	// Honor cancellation before doing any work so a cancelled run
	// stops promptly and never falsely reports a pass.
	if err := ctx.Err(); err != nil {
		result.Err = "context: " + err.Error()
		result.Duration = time.Since(start)
		return result
	}

	actual, err := callSubject(ctx, cfg.Subject, c.Input)
	if err != nil {
		result.Err = err.Error()
		result.Duration = time.Since(start)
		// Errored cases short-circuit: scorers don't run.
		return result
	}
	result.Actual = actual
	pass := true
	for _, s := range cfg.Scorers {
		sc, err := callScorer(ctx, s, c, actual)
		if err != nil {
			// Scorer errors degrade to "fail" with the error in the
			// Explanation so the report stays well-formed.
			sc = Score{Value: 0, Pass: false, Explanation: "scorer error: " + err.Error()}
		}
		result.Scores[s.Name()] = sc
		if !sc.Pass {
			pass = false
		}
	}
	result.Pass = pass
	result.Duration = time.Since(start)
	return result
}

// callSubject invokes the Subject, converting a panic into an error so
// one misbehaving case is recorded as Errored instead of aborting the
// whole process (RunAndExit is a plain main(), not go test, so there
// is no recovering harness above us).
func callSubject(ctx context.Context, subject Subject, input string) (out string, err error) {
	defer func() {
		if r := recover(); r != nil {
			out = ""
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return subject(ctx, input)
}

// callScorer invokes a Scorer, converting a panic into an error.
func callScorer(ctx context.Context, s Scorer, c Case, actual string) (sc Score, err error) {
	defer func() {
		if r := recover(); r != nil {
			sc = Score{}
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return s.Score(ctx, c, actual)
}

// tallyReport walks the case results and fills Passed / Failed /
// Errored counters plus per-scorer aggregates.
func tallyReport(r *Report, cfg Config) {
	type acc struct {
		sum  float64
		pass int
		fail int
		n    int
	}
	totals := map[string]*acc{}
	for _, sc := range cfg.Scorers {
		totals[sc.Name()] = &acc{}
	}

	for _, cr := range r.Cases {
		switch {
		case cr.Err != "":
			r.Errored++
		case cr.Pass:
			r.Passed++
		default:
			r.Failed++
		}
		for name, sc := range cr.Scores {
			a := totals[name]
			if a == nil {
				continue
			}
			a.sum += sc.Value
			a.n++
			if sc.Pass {
				a.pass++
			} else {
				a.fail++
			}
		}
	}

	// Deterministic order for the aggregate map keys is the
	// scorer registration order (we just walk cfg.Scorers).
	for _, sc := range cfg.Scorers {
		a := totals[sc.Name()]
		var mean float64
		if a.n > 0 {
			mean = a.sum / float64(a.n)
		}
		r.Aggregates[sc.Name()] = Aggregate{
			Scorer: sc.Name(),
			Mean:   mean,
			Pass:   a.pass,
			Fail:   a.fail,
		}
	}
	// Sort the Cases slice by case ID so reports diff cleanly
	// across runs even when workers complete out of order.
	sort.SliceStable(r.Cases, func(i, j int) bool {
		return r.Cases[i].Case.ID < r.Cases[j].Case.ID
	})
}
