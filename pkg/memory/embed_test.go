package memory_test

import (
	"context"
	"math"
	"testing"

	"github.com/YasserCR/galdor/pkg/memory"
)

func TestHashingEmbedder_Dimensions(t *testing.T) {
	t.Parallel()
	e := memory.NewHashingEmbedder(128)
	if e.Dimensions() != 128 {
		t.Errorf("Dimensions = %d", e.Dimensions())
	}
	vecs, err := e.Embed(context.Background(), []string{"hello world"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 1 || len(vecs[0]) != 128 {
		t.Errorf("vec shape = %d x %d", len(vecs), len(vecs[0]))
	}
}

func TestHashingEmbedder_Deterministic(t *testing.T) {
	t.Parallel()
	e := memory.NewHashingEmbedder(64)
	a, _ := e.Embed(context.Background(), []string{"the quick brown fox"})
	b, _ := e.Embed(context.Background(), []string{"the quick brown fox"})
	if len(a) != 1 || len(b) != 1 {
		t.Fatal("embed returned wrong shape")
	}
	for i := range a[0] {
		if a[0][i] != b[0][i] {
			t.Fatalf("non-deterministic at index %d: %v vs %v", i, a[0][i], b[0][i])
		}
	}
}

func TestHashingEmbedder_L2Normalized(t *testing.T) {
	t.Parallel()
	e := memory.NewHashingEmbedder(64)
	vecs, _ := e.Embed(context.Background(), []string{"alpha beta gamma delta epsilon"})
	v := vecs[0]
	var sumSq float64
	for _, x := range v {
		sumSq += float64(x) * float64(x)
	}
	norm := math.Sqrt(sumSq)
	if math.Abs(norm-1.0) > 1e-5 {
		t.Errorf("L2 norm = %v, want ~1", norm)
	}
}

func TestHashingEmbedder_OverlapDrivesSimilarity(t *testing.T) {
	t.Parallel()
	e := memory.NewHashingEmbedder(512)
	ctx := context.Background()
	vecs, _ := e.Embed(ctx, []string{
		"quito ecuador capital",
		"quito ecuador city",
		"unrelated gardening tips",
	})
	cos := func(a, b []float32) float64 {
		var dot float64
		for i := range a {
			dot += float64(a[i]) * float64(b[i])
		}
		return dot
	}
	near := cos(vecs[0], vecs[1])
	far := cos(vecs[0], vecs[2])
	if near <= far {
		t.Errorf("similar texts should have higher cosine: near=%v, far=%v", near, far)
	}
}

func TestEmbedderFunc_AdaptsFunction(t *testing.T) {
	t.Parallel()
	var ef memory.Embedder = memory.EmbedderFunc{
		Dim: 4,
		Fn: func(_ context.Context, texts []string) ([][]float32, error) {
			out := make([][]float32, len(texts))
			for i := range texts {
				out[i] = []float32{1, 2, 3, 4}
			}
			return out, nil
		},
	}
	if ef.Dimensions() != 4 {
		t.Errorf("Dimensions = %d", ef.Dimensions())
	}
	vecs, err := ef.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 || len(vecs[0]) != 4 {
		t.Errorf("shape wrong: %d x %d", len(vecs), len(vecs[0]))
	}
}
