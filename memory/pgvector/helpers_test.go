package pgvector

import "github.com/YasserCR/galdor/pkg/memory"

func memoryQueryWithEmbedding(v []float32, k int, filter map[string]string) memory.Query {
	return memory.Query{Embedding: v, K: k, Filter: filter}
}
