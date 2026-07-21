package okf

import "testing"

func TestSplitFrontmatter(t *testing.T) {
	fm, body, present := splitFrontmatter("---\ntype: Metric\n---\nhello\nworld")
	if fm != "type: Metric" || !present {
		t.Fatalf("fm = %q present = %v", fm, present)
	}
	if body != "hello\nworld" {
		t.Fatalf("body = %q", body)
	}

	// No frontmatter: whole text is body, present = false.
	fm, body, present = splitFrontmatter("no frontmatter here")
	if fm != "" || body != "no frontmatter here" || present {
		t.Fatalf("expected empty fm, unchanged body, absent; got %q / %q / %v", fm, body, present)
	}

	// Unterminated frontmatter is tolerated (permissive): treated as body,
	// and reported as not-present (a §9 rule-1 fact Validate surfaces).
	fm, _, present = splitFrontmatter("---\ntype: Metric\nno closing delim")
	if fm != "" || present {
		t.Fatalf("unterminated fm should yield empty fm and present=false, got %q / %v", fm, present)
	}
}

func TestParseFrontmatter(t *testing.T) {
	fm := parseFrontmatter(strjoin(
		`type: Metric`,
		`title: MRR (Monthly Recurring Revenue)`,
		`tags: [metric, revenue, mrr]`,
		`timestamp: '2026-06-04T10:00:00Z'`,
		`resource: "warehouse://x/y"`,
	))
	if got := asString(fm, "type"); got != "Metric" {
		t.Fatalf("type = %q", got)
	}
	if got := asString(fm, "title"); got != "MRR (Monthly Recurring Revenue)" {
		t.Fatalf("title = %q", got)
	}
	if got := asString(fm, "timestamp"); got != "2026-06-04T10:00:00Z" {
		t.Fatalf("timestamp = %q (quotes should be stripped)", got)
	}
	if got := asString(fm, "resource"); got != "warehouse://x/y" {
		t.Fatalf("resource = %q (double quotes should be stripped, colon kept)", got)
	}
	tags := asList(fm, "tags")
	if len(tags) != 3 || tags[0] != "metric" || tags[2] != "mrr" {
		t.Fatalf("tags = %v", tags)
	}
}

func TestParseFrontmatter_BlockList(t *testing.T) {
	fm := parseFrontmatter(strjoin(
		`tags:`,
		`  - alpha`,
		`  - beta`,
	))
	tags := asList(fm, "tags")
	if len(tags) != 2 || tags[0] != "alpha" || tags[1] != "beta" {
		t.Fatalf("block list tags = %v", tags)
	}
}

func strjoin(lines ...string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}
