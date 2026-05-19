package eval_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YasserCR/galdor/pkg/eval"
	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// scriptedProvider is a deterministic Provider for tests. Returns
// the configured Reply on every Generate call.
type scriptedProvider struct {
	Reply schema.Message
	calls atomic.Int32
}

func (*scriptedProvider) Name() string { return "scripted" }
func (*scriptedProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{}
}
func (*scriptedProvider) Stream(_ context.Context, _ provider.Request) (provider.StreamReader, error) {
	return nil, provider.ErrUnsupported
}

func (p *scriptedProvider) Generate(_ context.Context, _ provider.Request) (*provider.Response, error) {
	p.calls.Add(1)
	return &provider.Response{Message: p.Reply, StopReason: schema.StopReasonEndTurn}, nil
}

// -------------------- Scorers --------------------

func TestExactMatch(t *testing.T) {
	t.Parallel()
	s := eval.ExactMatch{}
	got, _ := s.Score(context.Background(), eval.Case{Expected: "Hello"}, "  hello  ")
	if !got.Pass || got.Value != 1 {
		t.Errorf("expected pass for trim+case-fold match, got %+v", got)
	}
	got, _ = s.Score(context.Background(), eval.Case{Expected: "yes"}, "no")
	if got.Pass {
		t.Errorf("expected fail, got %+v", got)
	}
}

func TestExactMatch_CaseSensitive(t *testing.T) {
	t.Parallel()
	s := eval.ExactMatch{CaseSensitive: true}
	got, _ := s.Score(context.Background(), eval.Case{Expected: "Yes"}, "yes")
	if got.Pass {
		t.Errorf("case-sensitive mismatch should fail, got %+v", got)
	}
}

func TestContains(t *testing.T) {
	t.Parallel()
	s := eval.Contains{}
	got, _ := s.Score(context.Background(), eval.Case{Expected: "Quito"}, "the capital of Ecuador is QUITO.")
	if !got.Pass {
		t.Errorf("expected pass, got %+v", got)
	}
	got, _ = s.Score(context.Background(), eval.Case{Expected: "Lima"}, "the capital of Ecuador is Quito.")
	if got.Pass {
		t.Errorf("expected fail, got %+v", got)
	}
	got, _ = s.Score(context.Background(), eval.Case{}, "anything")
	if got.Pass {
		t.Errorf("empty Expected should fail")
	}
}

func TestRegex(t *testing.T) {
	t.Parallel()
	s := &eval.Regex{Pattern: `^\d+ items?$`}
	got, _ := s.Score(context.Background(), eval.Case{}, "42 items")
	if !got.Pass {
		t.Errorf("expected match, got %+v", got)
	}
	got, _ = s.Score(context.Background(), eval.Case{}, "lots of items")
	if got.Pass {
		t.Errorf("expected no match")
	}
}

func TestRegex_CompileErrorSurfacesOnFirstScore(t *testing.T) {
	t.Parallel()
	s := &eval.Regex{Pattern: "[invalid("}
	_, err := s.Score(context.Background(), eval.Case{}, "anything")
	if err == nil {
		t.Fatal("expected compile error")
	}
}

func TestScorerFunc(t *testing.T) {
	t.Parallel()
	s := eval.ScorerFunc("len_at_least_5", func(_ context.Context, _ eval.Case, actual string) (eval.Score, error) {
		if len(actual) >= 5 {
			return eval.Score{Value: 1, Pass: true}, nil
		}
		return eval.Score{Value: 0, Pass: false}, nil
	})
	if s.Name() != "len_at_least_5" {
		t.Errorf("Name = %q", s.Name())
	}
	got, _ := s.Score(context.Background(), eval.Case{}, "hello")
	if !got.Pass {
		t.Errorf("expected pass: %+v", got)
	}
}

func TestLLMJudge_ParsesScoreAndAppliesThreshold(t *testing.T) {
	t.Parallel()
	judge := eval.LLMJudge{
		Provider: &scriptedProvider{Reply: schema.AssistantMessage("85")},
		Model:    "judge",
		Rubric:   "test rubric",
	}
	got, err := judge.Score(context.Background(), eval.Case{Input: "x", Expected: "y"}, "z")
	if err != nil {
		t.Fatal(err)
	}
	if got.Value < 0.84 || got.Value > 0.86 {
		t.Errorf("Value = %v, want ~0.85", got.Value)
	}
	if !got.Pass {
		t.Errorf("0.85 should pass default threshold 0.7")
	}
}

func TestLLMJudge_FailsBelowThreshold(t *testing.T) {
	t.Parallel()
	judge := eval.LLMJudge{
		Provider: &scriptedProvider{Reply: schema.AssistantMessage("Score: 30/100")},
		Model:    "judge",
	}
	got, err := judge.Score(context.Background(), eval.Case{Input: "x"}, "z")
	if err != nil {
		t.Fatal(err)
	}
	if got.Pass {
		t.Errorf("0.30 should fail default threshold 0.7: %+v", got)
	}
}

func TestLLMJudge_GracefulOnUnparseableReply(t *testing.T) {
	t.Parallel()
	judge := eval.LLMJudge{
		Provider: &scriptedProvider{Reply: schema.AssistantMessage("I refuse to answer.")},
		Model:    "judge",
	}
	got, err := judge.Score(context.Background(), eval.Case{Input: "x"}, "z")
	if err != nil {
		t.Fatal(err)
	}
	if got.Pass || got.Value != 0 {
		t.Errorf("unparseable reply should be 0/fail: %+v", got)
	}
}

func TestLLMJudge_RejectsMissingProvider(t *testing.T) {
	t.Parallel()
	judge := eval.LLMJudge{Model: "x"}
	_, err := judge.Score(context.Background(), eval.Case{}, "z")
	if err == nil {
		t.Fatal("expected error for missing Provider")
	}
}

// -------------------- Runner / Report --------------------

func TestRun_AllPass(t *testing.T) {
	t.Parallel()
	report, err := eval.Run(context.Background(), eval.Config{
		Dataset: eval.Dataset{Name: "smoke", Version: "1", Cases: []eval.Case{
			{ID: "c1", Input: "hello", Expected: "hello"},
			{ID: "c2", Input: "world", Expected: "world"},
		}},
		Subject: func(_ context.Context, in string) (string, error) { return in, nil },
		Scorers: []eval.Scorer{eval.ExactMatch{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed != 2 || report.Failed != 0 || report.Errored != 0 {
		t.Errorf("counts: %+v", report)
	}
	if !report.Meets(1.0) {
		t.Errorf("PassRate = %v", report.PassRate())
	}
	if got := report.Aggregates["exact_match"]; got.Mean != 1.0 {
		t.Errorf("aggregate mean = %v", got.Mean)
	}
}

func TestRun_PartialPass(t *testing.T) {
	t.Parallel()
	report, err := eval.Run(context.Background(), eval.Config{
		Dataset: eval.Dataset{Name: "mixed", Version: "1", Cases: []eval.Case{
			{ID: "c1", Input: "hello", Expected: "hello"},
			{ID: "c2", Input: "hello", Expected: "world"},
		}},
		Subject: func(_ context.Context, _ string) (string, error) { return "hello", nil },
		Scorers: []eval.Scorer{eval.ExactMatch{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed != 1 || report.Failed != 1 {
		t.Errorf("counts: %+v", report)
	}
	if got := report.PassRate(); got != 0.5 {
		t.Errorf("PassRate = %v, want 0.5", got)
	}
	if report.Meets(0.9) {
		t.Errorf("0.5 should not meet 0.9 threshold")
	}
	if !report.Meets(0.5) {
		t.Errorf("0.5 should meet 0.5 threshold")
	}
}

func TestRun_SubjectErrorCountsAsErrored(t *testing.T) {
	t.Parallel()
	report, err := eval.Run(context.Background(), eval.Config{
		Dataset: eval.Dataset{Name: "x", Version: "1", Cases: []eval.Case{
			{ID: "c1", Input: "x", Expected: "x"},
		}},
		Subject: func(_ context.Context, _ string) (string, error) {
			return "", errors.New("provider down")
		},
		Scorers: []eval.Scorer{eval.ExactMatch{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Errored != 1 || report.Passed != 0 || report.Failed != 0 {
		t.Errorf("counts: %+v", report)
	}
	if report.Cases[0].Err == "" {
		t.Error("CaseResult.Err must capture the subject error")
	}
}

func TestRun_AllScorersMustPass(t *testing.T) {
	t.Parallel()
	// Two scorers, one passes, one fails -> case fails.
	report, err := eval.Run(context.Background(), eval.Config{
		Dataset: eval.Dataset{Name: "x", Version: "1", Cases: []eval.Case{
			{ID: "c1", Input: "x", Expected: "the cat sat on the mat"},
		}},
		Subject: func(_ context.Context, _ string) (string, error) {
			return "the cat sat on the mat", nil
		},
		Scorers: []eval.Scorer{
			eval.ExactMatch{},             // passes
			&eval.Regex{Pattern: `^bird`}, // fails
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed != 0 || report.Failed != 1 {
		t.Errorf("counts: %+v", report)
	}
}

func TestRun_RejectsBadConfig(t *testing.T) {
	t.Parallel()
	cases := map[string]eval.Config{
		"nil subject": {
			Dataset: eval.Dataset{Name: "x", Version: "1", Cases: []eval.Case{{ID: "a"}}},
			Scorers: []eval.Scorer{eval.ExactMatch{}},
		},
		"empty scorers": {
			Dataset: eval.Dataset{Name: "x", Version: "1", Cases: []eval.Case{{ID: "a"}}},
			Subject: func(_ context.Context, _ string) (string, error) { return "", nil },
		},
		"empty dataset cases": {
			Dataset: eval.Dataset{Name: "x", Version: "1"},
			Subject: func(_ context.Context, _ string) (string, error) { return "", nil },
			Scorers: []eval.Scorer{eval.ExactMatch{}},
		},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := eval.Run(context.Background(), cfg); err == nil {
				t.Fatalf("expected error for %s", name)
			}
		})
	}
}

func TestRun_ParallelExecution(t *testing.T) {
	t.Parallel()
	// 8 cases, each subject call sleeps 50ms. With Parallel=4 the
	// total wall time should be ~100ms (2 batches), well under
	// the sequential 400ms.
	var ds []eval.Case
	for i := 0; i < 8; i++ {
		ds = append(ds, eval.Case{ID: "c" + string(rune('0'+i)), Input: "x", Expected: "x"})
	}
	start := time.Now()
	_, err := eval.Run(context.Background(), eval.Config{
		Dataset: eval.Dataset{Name: "slow", Version: "1", Cases: ds},
		Subject: func(_ context.Context, _ string) (string, error) {
			time.Sleep(50 * time.Millisecond)
			return "x", nil
		},
		Scorers:  []eval.Scorer{eval.ExactMatch{}},
		Parallel: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if elapsed >= 350*time.Millisecond {
		t.Errorf("elapsed = %v, expected ~100ms with Parallel=4", elapsed)
	}
}

func TestPrintSummary_IncludesFailingCases(t *testing.T) {
	t.Parallel()
	report, _ := eval.Run(context.Background(), eval.Config{
		Dataset: eval.Dataset{Name: "x", Version: "v2", Cases: []eval.Case{
			{ID: "ok", Input: "a", Expected: "a"},
			{ID: "fails", Input: "a", Expected: "b"},
		}},
		Subject: func(_ context.Context, _ string) (string, error) { return "a", nil },
		Scorers: []eval.Scorer{eval.ExactMatch{}},
	})
	var buf bytes.Buffer
	report.PrintSummary(&buf)
	out := buf.String()
	for _, want := range []string{"Dataset:  x @ v2", "Pass rate: 50.0%", "fails"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q in:\n%s", want, out)
		}
	}
}

func TestRun_PreservesCaseOrderByID(t *testing.T) {
	t.Parallel()
	// Random insertion order; report should come back sorted.
	report, _ := eval.Run(context.Background(), eval.Config{
		Dataset: eval.Dataset{Name: "x", Version: "1", Cases: []eval.Case{
			{ID: "c", Input: "x", Expected: "x"},
			{ID: "a", Input: "x", Expected: "x"},
			{ID: "b", Input: "x", Expected: "x"},
		}},
		Subject: func(_ context.Context, _ string) (string, error) { return "x", nil },
		Scorers: []eval.Scorer{eval.ExactMatch{}},
	})
	ids := []string{report.Cases[0].Case.ID, report.Cases[1].Case.ID, report.Cases[2].Case.ID}
	if ids[0] != "a" || ids[1] != "b" || ids[2] != "c" {
		t.Errorf("expected sorted IDs, got %v", ids)
	}
}

// -------------------- Loader --------------------

func TestLoadDataset_Roundtrip(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "ds.json")
	original := eval.Dataset{
		Name: "loader-test", Version: "1",
		Cases: []eval.Case{
			{ID: "a", Input: "hello", Expected: "world"},
			{ID: "b", Input: "ping", Expected: "pong", Metadata: map[string]string{"tag": "smoke"}},
		},
	}
	if err := eval.SaveDataset(original, path); err != nil {
		t.Fatal(err)
	}
	loaded, err := eval.LoadDataset(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Name != original.Name || loaded.Version != original.Version {
		t.Errorf("metadata mismatch: %+v vs %+v", loaded, original)
	}
	if len(loaded.Cases) != 2 || loaded.Cases[1].Metadata["tag"] != "smoke" {
		t.Errorf("cases roundtrip wrong: %+v", loaded.Cases)
	}
}

func TestLoadDataset_RejectsDuplicateIDs(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "dup.json")
	bad := `{"name":"x","version":"1","cases":[{"id":"a","input":"i"},{"id":"a","input":"j"}]}`
	if err := os.WriteFile(path, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := eval.LoadDataset(path); err == nil {
		t.Fatal("expected error for duplicate IDs")
	}
}

func TestLoadDataset_RejectsEmptyMetadata(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if _, err := eval.LoadDataset(filepath.Join(tmp, "missing.json")); err == nil {
		t.Fatal("expected error for missing file")
	}
}
