// Package pgvector provides a memory.Store backed by Postgres with
// the pgvector extension.
//
// The store uses pgx/v5 as the driver and assumes the target database
// already has the pgvector extension installed (`CREATE EXTENSION
// vector` on a recent Postgres). The schema is created on Open and
// is idempotent across re-opens; the table name is configurable so
// multiple stores can coexist in the same database without colliding.
//
// Retrieval uses pgvector's cosine distance operator `<=>` (lower is
// closer); galdor's Score is computed as `1 - distance` so it follows
// the higher-is-better convention used everywhere else.
//
// The dimensionality of the embeddings is fixed per table at Open
// time. Adding chunks whose Embedding length differs from the table's
// declared dimension returns an error rather than silently truncating.
package pgvector
