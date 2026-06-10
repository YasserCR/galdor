package memory_test

import (
	"context"
	"testing"
	"time"

	"github.com/YasserCR/galdor/pkg/memory"
	"github.com/YasserCR/galdor/pkg/schema"
)

// Regression for audit M18: Window.Snapshot must not hold the data lock
// across the Summarizer (an LLM call), or concurrent Append/Len wedge for
// the whole round-trip. Here the summarizer blocks; Append must stay
// responsive.
func TestWindow_AppendNotBlockedBySummarizer(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{})
	w := &memory.Window{
		MaxMessages: 1, // force eviction so the summarizer runs
		Summarizer: memory.SummarizerFunc(func(_ context.Context, _ []schema.Message) (string, error) {
			close(entered)
			<-release
			return "summary", nil
		}),
	}
	w.Append(schema.UserMessage("a"))
	w.Append(schema.UserMessage("b"))
	w.Append(schema.UserMessage("c"))

	go func() { _, _ = w.Snapshot(context.Background()) }()
	<-entered // summarizer is now blocked

	done := make(chan struct{})
	go func() { w.Append(schema.UserMessage("d")); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("Append blocked while the Summarizer ran (regression of M18)")
	}
	close(release)
}
