package provider

import (
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		value    string
		wantSecs int
		wantOK   bool
	}{
		{"empty", "", 0, false},
		{"seconds", "120", 120, true},
		{"zero seconds", "0", 0, true},
		{"negative seconds rejected", "-5", 0, false},
		{"garbage rejected", "soon", 0, false},
		{"http-date future", "Wed, 10 Jun 2026 12:02:00 GMT", 120, true},
		{"http-date past means now", "Wed, 10 Jun 2026 11:59:00 GMT", 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			secs, ok := ParseRetryAfter(c.value, now)
			if ok != c.wantOK || secs != c.wantSecs {
				t.Errorf("ParseRetryAfter(%q) = (%d, %v), want (%d, %v)", c.value, secs, ok, c.wantSecs, c.wantOK)
			}
		})
	}
}
