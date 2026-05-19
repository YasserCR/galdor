package pgvector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/YasserCR/galdor/pkg/memory"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config configures the pgvector Store.
type Config struct {
	// ConnString is a libpq-style connection string accepted by
	// pgxpool.New. Required. Example:
	//   postgres://user:pass@host:5432/db?sslmode=disable
	ConnString string

	// Table is the name of the chunks table. Defaults to "galdor_chunks".
	// Must be a valid Postgres identifier (a-z, 0-9, _) so it can be
	// safely embedded in DDL without quoting.
	Table string

	// Dim is the embedding dimensionality. Required; the table is
	// created with `embedding vector(Dim)` and chunks added later
	// must match.
	Dim int
}

// Store is a memory.Store backed by Postgres + pgvector. The zero
// value is not usable; call Open.
type Store struct {
	pool  *pgxpool.Pool
	table string
	dim   int
}

// Open returns a usable Store. It validates the config, opens a
// connection pool, ensures the pgvector extension is present and
// creates the chunks table + indexes if they don't exist.
func Open(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.ConnString == "" {
		return nil, errors.New("memory/pgvector: ConnString is required")
	}
	if cfg.Dim <= 0 {
		return nil, errors.New("memory/pgvector: Dim must be > 0")
	}
	if cfg.Table == "" {
		cfg.Table = "galdor_chunks"
	}
	if !isSafeIdent(cfg.Table) {
		return nil, fmt.Errorf("memory/pgvector: Table %q must match [a-z0-9_]+", cfg.Table)
	}

	pool, err := pgxpool.New(ctx, cfg.ConnString)
	if err != nil {
		return nil, fmt.Errorf("memory/pgvector: connect: %w", err)
	}
	s := &Store{pool: pool, table: cfg.Table, dim: cfg.Dim}
	if err := s.ensureSchema(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the connection pool.
func (s *Store) Close() error {
	if s.pool != nil {
		s.pool.Close()
	}
	return nil
}

// Add ingests chunks. Every chunk must have a non-empty ID; callers
// are expected to assign stable IDs so re-ingestion is idempotent.
// Chunks whose Embedding length differs from the table's declared
// dimension are rejected.
func (s *Store) Add(ctx context.Context, chunks []memory.Chunk) error {
	if len(chunks) == 0 {
		return nil
	}
	for _, c := range chunks {
		if c.ID == "" {
			return errors.New("memory/pgvector: Chunk.ID is empty (caller must assign IDs)")
		}
		if len(c.Embedding) != s.dim {
			return fmt.Errorf("memory/pgvector: chunk %q has %d-dim embedding; table is %d-dim", c.ID, len(c.Embedding), s.dim)
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("memory/pgvector: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	sql := fmt.Sprintf(`
INSERT INTO %s (id, document_id, idx, text, embedding, metadata)
VALUES ($1, $2, $3, $4, $5::vector, $6::jsonb)
ON CONFLICT (id) DO UPDATE SET
    document_id = EXCLUDED.document_id,
    idx         = EXCLUDED.idx,
    text        = EXCLUDED.text,
    embedding   = EXCLUDED.embedding,
    metadata    = EXCLUDED.metadata
`, s.table)

	for _, c := range chunks {
		metaJSON := []byte("{}")
		if len(c.Metadata) > 0 {
			metaJSON, err = json.Marshal(c.Metadata)
			if err != nil {
				return fmt.Errorf("memory/pgvector: marshal metadata: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, sql,
			c.ID, c.DocumentID, c.Index, c.Text, formatVector(c.Embedding), string(metaJSON),
		); err != nil {
			return fmt.Errorf("memory/pgvector: insert %q: %w", c.ID, err)
		}
	}
	return tx.Commit(ctx)
}

// Retrieve runs q against the store. Requires q.Embedding to be set;
// pure-text queries are rejected because pgvector is a vector store
// (text-only retrieval belongs in the SQLite/BM25 adapter).
//
// Filtering by metadata maps to JSONB `@>` containment so multi-key
// filters work in a single round trip.
func (s *Store) Retrieve(ctx context.Context, q memory.Query) ([]memory.Result, error) {
	if len(q.Embedding) == 0 {
		return nil, errors.New("memory/pgvector: Query.Embedding is required (this backend is vector-only)")
	}
	if len(q.Embedding) != s.dim {
		return nil, fmt.Errorf("memory/pgvector: query has %d-dim embedding; table is %d-dim", len(q.Embedding), s.dim)
	}
	k := q.K
	if k <= 0 {
		k = 5
	}

	sql, args := s.buildRetrieveSQL(q, k)
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("memory/pgvector: query: %w", err)
	}
	defer rows.Close()

	results := make([]memory.Result, 0, k)
	for rows.Next() {
		var c memory.Chunk
		var distance float64
		var metaJSON []byte
		var embedStr string
		if err := rows.Scan(&c.ID, &c.DocumentID, &c.Index, &c.Text, &embedStr, &metaJSON, &distance); err != nil {
			return nil, fmt.Errorf("memory/pgvector: scan: %w", err)
		}
		if len(metaJSON) > 0 && string(metaJSON) != "{}" {
			if err := json.Unmarshal(metaJSON, &c.Metadata); err != nil {
				return nil, fmt.Errorf("memory/pgvector: decode metadata: %w", err)
			}
		}
		c.Embedding = parseVector(embedStr)
		// Convert pgvector cosine distance (0 = identical, 2 = opposite)
		// to galdor's higher-is-better Score in [-1, 1].
		results = append(results, memory.Result{Chunk: c, Score: float32(1.0 - distance)})
	}
	return results, rows.Err()
}

// Delete removes every chunk whose DocumentID matches the argument.
// Returns nil even when no chunks were removed (idempotent).
func (s *Store) Delete(ctx context.Context, documentID string) error {
	if documentID == "" {
		return errors.New("memory/pgvector: Delete called with empty documentID")
	}
	sql := fmt.Sprintf(`DELETE FROM %s WHERE document_id = $1`, s.table)
	if _, err := s.pool.Exec(ctx, sql, documentID); err != nil {
		return fmt.Errorf("memory/pgvector: delete: %w", err)
	}
	return nil
}

// Len reports the total number of chunks currently stored. Not part
// of the Store interface; useful for tests.
func (s *Store) Len(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s`, s.table)).Scan(&n)
	return n, err
}

func (s *Store) ensureSchema(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		return fmt.Errorf("memory/pgvector: ensure extension: %w", err)
	}
	create := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
    id          TEXT PRIMARY KEY,
    document_id TEXT NOT NULL,
    idx         INTEGER NOT NULL,
    text        TEXT NOT NULL,
    embedding   vector(%d) NOT NULL,
    metadata    JSONB NOT NULL DEFAULT '{}'::jsonb
)`, s.table, s.dim)
	if _, err := s.pool.Exec(ctx, create); err != nil {
		return fmt.Errorf("memory/pgvector: create table: %w", err)
	}
	idx := fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_doc_idx ON %s(document_id)`, s.table, s.table)
	if _, err := s.pool.Exec(ctx, idx); err != nil {
		return fmt.Errorf("memory/pgvector: create index: %w", err)
	}
	return nil
}

// buildRetrieveSQL is exported as a method so it can be unit-tested
// without a live database. It returns the SQL string and the
// positional arguments slice.
func (s *Store) buildRetrieveSQL(q memory.Query, k int) (string, []any) {
	var b strings.Builder
	args := make([]any, 0, 3)

	args = append(args, formatVector(q.Embedding))
	fmt.Fprintf(&b, `
SELECT id, document_id, idx, text, embedding::text, metadata, embedding <=> $1::vector AS distance
FROM %s`, s.table)

	if len(q.Filter) > 0 {
		filterJSON, _ := json.Marshal(q.Filter)
		args = append(args, string(filterJSON))
		fmt.Fprintf(&b, ` WHERE metadata @> $%d::jsonb`, len(args))
	}

	args = append(args, k)
	fmt.Fprintf(&b, ` ORDER BY distance ASC LIMIT $%d`, len(args))
	return b.String(), args
}

// formatVector serializes a float32 slice to the pgvector input
// literal `[v0,v1,...]`. The literal is cast to `vector` server-side.
func formatVector(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(x), 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

// parseVector is the inverse of formatVector. It accepts the textual
// representation pgvector returns when the column is cast to text.
func parseVector(s string) []float32 {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '[' || s[len(s)-1] != ']' {
		return nil
	}
	body := s[1 : len(s)-1]
	if body == "" {
		return nil
	}
	parts := strings.Split(body, ",")
	out := make([]float32, 0, len(parts))
	for _, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 32)
		if err != nil {
			return nil
		}
		out = append(out, float32(f))
	}
	return out
}

// isSafeIdent reports whether s is composed only of lowercase ASCII
// letters, digits and underscores — the subset of Postgres
// identifiers that is safe to embed in DDL without quoting.
func isSafeIdent(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_':
		default:
			return false
		}
	}
	return true
}
