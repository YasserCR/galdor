package embedder

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Regression for audit M12: an OpenAI-shape response with fewer (or
// duplicate-index) embeddings than inputs leaves nil slots. Embed must
// error rather than return nil vectors into the store.
func TestEmbed_OpenAIShortResponseErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// One embedding for two inputs.
		_, _ = io.WriteString(w, `{"data":[{"index":0,"embedding":[1,2,3]}]}`)
	}))
	defer srv.Close()
	e, err := NewHTTPEmbedder(HTTPConfig{URL: srv.URL, Shape: ShapeOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Embed(context.Background(), []string{"a", "b"}); err == nil {
		t.Fatal("a short OpenAI response must error, not return nil vectors (regression of M12)")
	}
}

// Regression for audit H10: HTTPEmbedder is documented safe for concurrent
// use, but the dim auto-detect write raced concurrent Embed/Dimensions.
// Run under `go test -race`; with the unsynchronized int field this trips
// the race detector.
func TestEmbed_ConcurrentDimNoRace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"index":0,"embedding":[1,2,3]}]}`)
	}))
	defer srv.Close()
	e, err := NewHTTPEmbedder(HTTPConfig{URL: srv.URL, Shape: ShapeOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = e.Embed(context.Background(), []string{"x"})
			_ = e.Dimensions()
		}()
	}
	wg.Wait()
}
