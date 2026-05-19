// Package chunk splits Documents into Chunks suitable for embedding
// and retrieval. Three strategies ship in-tree:
//
//   - FixedSize: split at a fixed character count, with optional overlap.
//   - Recursive: split on a list of separators in order (paragraphs,
//     then sentences, then words, ...), falling back to FixedSize at
//     the lowest level. This is the default for prose.
//   - Sentence: split at sentence boundaries, packing whole sentences
//     into chunks up to a size budget.
//
// All chunkers produce memory.Chunk values with DocumentID, Index
// and Metadata copied through from the source memory.Document. The
// resulting Chunks have empty Embedding fields; embed them with a
// memory.Embedder before adding to a vector store.
package chunk
