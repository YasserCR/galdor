package eval

import "testing"

// Regression (audit low): Config.MinPass is now a *float64 so an explicit 0
// (report-only) is expressible and distinct from "unset → default 1.0".
func TestThresholdHelperAndReportOnly(t *testing.T) {
	t.Parallel()
	p := Threshold(0)
	if p == nil || *p != 0 {
		t.Fatalf("Threshold(0) = %v, want pointer to 0", p)
	}
	// A report with a failing case (25% pass rate) must still "meet" a 0
	// threshold, but not a 1.0 one.
	r := &Report{Cases: make([]CaseResult, 4), Passed: 1}
	if !r.Meets(0) {
		t.Errorf("Meets(0) = false; a 0 threshold must accept any pass rate")
	}
	if r.Meets(1.0) {
		t.Errorf("Meets(1.0) = true for a 25%% pass rate; want false")
	}
}
