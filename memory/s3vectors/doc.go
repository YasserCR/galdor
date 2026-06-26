// Package s3vectors provides a memory.Store backed by Amazon S3 Vectors.
//
// It is a drop-in alternative to the pgvector and qdrant backends:
// same memory.Store interface, same Retriever composition, switchable
// by configuration. The store is vector-only — embeddings come from a
// memory.Embedder upstream (e.g. Bedrock Titan) and document content
// lives outside the vector store.
//
// The adapter talks to S3 Vectors through aws-sdk-go-v2; credentials
// are resolved via the default AWS credential chain (env → shared
// config → container creds → IMDS), the same model as galdor's Bedrock
// provider. No static keys are accepted.
//
// On Open the index is created with the configured dimension, the
// cosine distance metric and float32 data type if it does not already
// exist; the vector bucket must already exist (mirroring how pgvector
// creates its table but not its database). If the index exists, its
// dimension is validated against Config.Dim.
//
// Metadata round-trips through each vector's metadata document. The
// Chunk fields DocumentID, Index and Text are stored under reserved
// keys (`__document_id`, `__index`, `__text`) so they don't collide
// with user-supplied metadata, which is rejected at Add time if it
// uses the reserved `__` prefix. `__text` is declared non-filterable
// (it can be large and filterable metadata is size-limited); user
// metadata keys stay filterable so Query.Filter can be pushed down to
// S3 Vectors' native metadata filtering.
//
// Score follows galdor's higher-is-better convention. S3 Vectors
// returns a cosine distance; it is converted to a similarity via
// score = 1 - distance, and anti-correlated hits (negative score) are
// dropped, for parity with the qdrant/sqlite/in-memory backends.
//
// Delete(documentID) removes every chunk of a document. S3 Vectors has
// no delete-by-filter and ListVectors has no filter parameter, so
// Delete scans the index (paginated) and batch-deletes the matching
// keys. This is O(index size); acceptable at the standards-hub scale
// it targets, but worth noting for very large indexes.
package s3vectors
