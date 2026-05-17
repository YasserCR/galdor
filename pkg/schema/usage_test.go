package schema

import "testing"

func TestUsage_Total(t *testing.T) {
	t.Parallel()
	u := Usage{InputTokens: 100, OutputTokens: 23, CacheReadTokens: 50}
	if got := u.Total(); got != 123 {
		t.Errorf("Total = %d, want 123 (cache tokens must not be added)", got)
	}
}
