package memory

import (
	"context"
	"fmt"
)

// ExampleHybridRetriever fuses a lexical ranking and a dense ranking with
// Reciprocal Rank Fusion. Each source ranks the same chunks differently;
// RRF rewards chunks that rank highly in *both*, which is why "mrr" and
// "arpu" (top-2 in each source, in opposite order) come out ahead.
func ExampleHybridRetriever() {
	// In real use these would be *memory.Retriever values over a BM25 store
	// and a vector store; here they are fixed rankings for a deterministic
	// example.
	lexical := stubSearcher{res: ranking("mrr", "arpu", "churn")}
	dense := stubSearcher{res: ranking("arpu", "mrr", "ltv")}

	h := &HybridRetriever{
		Sources: []Searcher{lexical, dense},
		K:       3,
	}
	hits, err := h.Retrieve(context.Background(), Query{Text: "monthly revenue per user"})
	if err != nil {
		panic(err)
	}
	for _, hit := range hits {
		fmt.Printf("%-6s %.4f\n", hit.Chunk.ID, hit.Score)
	}
	// Output:
	// mrr    0.0325
	// arpu   0.0325
	// churn  0.0159
}
