package sqlite

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/YasserCR/galdor/pkg/memory"
	_ "modernc.org/sqlite"
)

// Store is a SQLite-backed memory.Store. Open returns a usable
// instance; Close releases the underlying *sql.DB.
//
// The store is goroutine-safe (delegated to database/sql's connection
// pool). Concurrent Add and Retrieve calls are supported; FTS5
// updates are wrapped in a transaction per Add.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) a SQLite database at the given path. Use
// ":memory:" for an ephemeral in-process database, useful for tests.
// The schema is created on first open and is idempotent across
// re-opens.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("memory/sqlite: open: %w", err)
	}
	// SQLite is happiest with a single writer; cap connections so
	// concurrent Add calls don't surface SQLITE_BUSY noise.
	db.SetMaxOpenConns(1)
	// For a ":memory:" database each connection is a SEPARATE, empty DB, so
	// if the pool ever drops the single connection the data vanishes and the
	// next query hits a fresh, schema-less one. Pin the connection open:
	// keep it idle and never expire it. (Harmless for file-backed DBs too.)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(0)
	db.SetConnMaxLifetime(0)

	if _, err := db.Exec(schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("memory/sqlite: schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the underlying database handle.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Add ingests chunks. Chunks with an empty ID are rejected (callers
// are expected to assign IDs; see uuid.NewString or any stable hash
// of the chunk content). Re-adding a chunk with an existing ID
// overwrites the previous entry, making re-ingestion idempotent.
func (s *Store) Add(ctx context.Context, chunks []memory.Chunk) error {
	if len(chunks) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory/sqlite: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, c := range chunks {
		if c.ID == "" {
			return errors.New("memory/sqlite: Chunk.ID is empty (caller must assign IDs)")
		}
		metaJSON := []byte("{}")
		if len(c.Metadata) > 0 {
			metaJSON, err = json.Marshal(c.Metadata)
			if err != nil {
				return fmt.Errorf("memory/sqlite: marshal metadata: %w", err)
			}
		}
		embedBlob := encodeEmbedding(c.Embedding)
		if _, err := tx.ExecContext(ctx, deleteChunkSQL, c.ID); err != nil {
			return fmt.Errorf("memory/sqlite: delete-before-upsert: %w", err)
		}
		if _, err := tx.ExecContext(ctx, insertChunkSQL,
			c.ID, c.DocumentID, c.Index, c.Text, embedBlob, string(metaJSON),
		); err != nil {
			return fmt.Errorf("memory/sqlite: insert chunk: %w", err)
		}
	}
	return tx.Commit()
}

// Retrieve runs q against the store. When q.Embedding is set, a
// brute-force cosine pass over the chunks table is used; otherwise
// FTS5 BM25 is queried against q.Text. Results are returned in
// descending Score order (higher = more relevant).
func (s *Store) Retrieve(ctx context.Context, q memory.Query) ([]memory.Result, error) {
	if q.Text == "" && len(q.Embedding) == 0 {
		return nil, errors.New("memory/sqlite: Query.Text and Query.Embedding both empty")
	}
	k := q.K
	if k <= 0 {
		k = 5
	}
	if len(q.Embedding) > 0 {
		return s.vectorRetrieve(ctx, q, k)
	}
	return s.lexicalRetrieve(ctx, q, k)
}

// Delete removes every chunk whose DocumentID matches the argument.
// Returns nil even when no chunks were removed (idempotent).
func (s *Store) Delete(ctx context.Context, documentID string) error {
	if documentID == "" {
		return errors.New("memory/sqlite: Delete called with empty documentID")
	}
	_, err := s.db.ExecContext(ctx, deleteByDocSQL, documentID)
	if err != nil {
		return fmt.Errorf("memory/sqlite: delete: %w", err)
	}
	return nil
}

// Len reports the total number of chunks currently stored. Not part
// of the Store interface; useful for tests.
func (s *Store) Len(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chunks`).Scan(&n)
	return n, err
}

func (s *Store) lexicalRetrieve(ctx context.Context, q memory.Query, k int) ([]memory.Result, error) {
	match := buildFTSMatch(q.Text)
	if match == "" {
		return nil, nil
	}
	// FTS5 bm25() returns negative scores (lower = more relevant);
	// negate so the caller-facing Score follows the higher-is-better
	// convention used across galdor.
	// Push the metadata filter into SQL alongside the FTS MATCH. Filtering
	// in Go after a fixed `k*4` overfetch could return 0 of K available
	// rows when the filter-matching chunks ranked below the overfetch
	// window.
	where, filterArgs := filterSQL("c.metadata", q.Filter)
	args := make([]any, 0, 2+len(filterArgs))
	args = append(args, match)
	args = append(args, filterArgs...)
	args = append(args, k)
	rows, err := s.db.QueryContext(ctx, lexicalRetrieveSQL(where), args...)
	if err != nil {
		return nil, fmt.Errorf("memory/sqlite: fts query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	results := make([]memory.Result, 0, k)
	for rows.Next() {
		var c memory.Chunk
		var bm25 float64
		var metaStr string
		var embedBlob []byte
		if err := rows.Scan(&c.ID, &c.DocumentID, &c.Index, &c.Text, &embedBlob, &metaStr, &bm25); err != nil {
			return nil, fmt.Errorf("memory/sqlite: scan: %w", err)
		}
		if err := decodeMeta(metaStr, &c.Metadata); err != nil {
			return nil, err
		}
		c.Embedding = decodeEmbedding(embedBlob)
		results = append(results, memory.Result{Chunk: c, Score: float32(-bm25)})
	}
	return results, rows.Err()
}

// vectorRetrieve ranks chunks by cosine similarity. The metadata filter is
// pushed into SQL (see filterSQL), so only the matching subset — e.g. a single
// topic — is read and scored, rather than the whole table. Within that subset
// the scoring is still a brute-force cosine pass in Go: SQLite has no native
// vector index, so this targets small/medium single-process corpora. For large,
// unfiltered corpora use the pgvector or qdrant backend, which push the
// nearest-neighbor search into an indexed store.
func (s *Store) vectorRetrieve(ctx context.Context, q memory.Query, k int) ([]memory.Result, error) {
	where, args := filterSQL("metadata", q.Filter)
	rows, err := s.db.QueryContext(ctx, vectorScanSQL(where), args...)
	if err != nil {
		return nil, fmt.Errorf("memory/sqlite: scan query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var hits []memory.Result
	for rows.Next() {
		var c memory.Chunk
		var metaStr string
		var embedBlob []byte
		if err := rows.Scan(&c.ID, &c.DocumentID, &c.Index, &c.Text, &embedBlob, &metaStr); err != nil {
			return nil, fmt.Errorf("memory/sqlite: scan: %w", err)
		}
		if err := decodeMeta(metaStr, &c.Metadata); err != nil {
			return nil, err
		}
		c.Embedding = decodeEmbedding(embedBlob)
		if len(c.Embedding) == 0 {
			continue
		}
		score, err := cosine(q.Embedding, c.Embedding)
		if err != nil {
			return nil, err
		}
		if score < 0 {
			continue
		}
		hits = append(hits, memory.Result{Chunk: c, Score: score})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits, nil
}

// buildFTSMatch turns a free-form query into an FTS5 MATCH clause. Each
// token is wrapped as a quoted FTS5 string literal so that operator
// keywords (AND/OR/NOT) and special characters (* ( ) : - + ^ ~) in the
// user's text are matched literally instead of being parsed as query
// syntax — a bare `foo AND bar` would otherwise produce a MATCH of
// `foo OR AND OR bar`, which is an FTS5 syntax error. Terms are joined
// with OR so any of them contributes to the score (BM25 handles the
// weighting).
func buildFTSMatch(query string) string {
	var terms []string
	for _, raw := range strings.Fields(query) {
		t := quoteFTSTerm(raw)
		if t == "" {
			continue
		}
		terms = append(terms, t)
	}
	if len(terms) == 0 {
		return ""
	}
	return strings.Join(terms, " OR ")
}

// quoteFTSTerm wraps a single user token as an FTS5 string literal.
// Inside a double-quoted FTS5 string the only character needing escaping
// is the double-quote itself, which is escaped by doubling it. Returns ""
// for whitespace-only input.
func quoteFTSTerm(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// filterSQL builds an AND-chain of `json_extract(<col>, ?) = ?` predicates that
// push a metadata equality filter down into SQLite, together with the bound
// arguments. Keys are sorted so the generated SQL is deterministic. The JSON
// path and the value are BOUND parameters (never interpolated), so the filter
// is injection-safe; only the column name — a caller-supplied package constant
// ("metadata" or "c.metadata") — is concatenated. Returns ("", nil) for an
// empty filter.
func filterSQL(col string, filter map[string]string) (string, []any) {
	if len(filter) == 0 {
		return "", nil
	}
	keys := make([]string, 0, len(filter))
	for k := range filter {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	args := make([]any, 0, 2*len(keys))
	for _, k := range keys {
		b.WriteString(" AND json_extract(")
		b.WriteString(col)
		b.WriteString(", ?) = ?")
		args = append(args, jsonPath(k), filter[k])
	}
	return b.String(), args
}

// jsonPath builds a quoted SQLite json_extract path for an arbitrary key.
// An unquoted "$."+key breaks for keys containing "." (treated as a nested
// lookup), spaces, or other path specials — those never matched. Quoting
// (with "" escaping) makes any flat key a literal member access.
func jsonPath(key string) string {
	return `$."` + strings.ReplaceAll(key, `"`, `""`) + `"`
}

// vectorScanSQL returns the chunk scan used by vectorRetrieve, with the metadata
// filter (built by filterSQL) pushed into the WHERE clause so only the matching
// subset is read. The fragment is assembled from package constants plus filterSQL's
// constant predicates — no caller data reaches the SQL string.
func vectorScanSQL(where string) string {
	const base = `SELECT id, document_id, idx, text, embedding, metadata FROM chunks`
	if where == "" {
		return base
	}
	return base + " WHERE 1=1" + where
}

func decodeMeta(raw string, out *map[string]string) error {
	if raw == "" || raw == "{}" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("memory/sqlite: decode metadata: %w", err)
	}
	return nil
}

// encodeEmbedding packs a []float32 as little-endian IEEE-754 bytes.
// nil and empty slices encode to a zero-length blob.
func encodeEmbedding(v []float32) []byte {
	if len(v) == 0 {
		return nil
	}
	buf := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// decodeEmbedding is the inverse of encodeEmbedding.
func decodeEmbedding(b []byte) []float32 {
	if len(b) == 0 || len(b)%4 != 0 {
		return nil
	}
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return out
}

// cosine returns the cosine similarity between a and b, in [-1, 1].
// Returns 0 when either vector is zero-length.
func cosine(a, b []float32) (float32, error) {
	if len(a) != len(b) {
		// A mismatch means the corpus and query were embedded by different
		// models; truncating to the shorter length produced a wrong score
		// over a prefix instead of failing.
		return 0, fmt.Errorf("memory/sqlite: embedding dimension mismatch: query=%d vs chunk=%d", len(a), len(b))
	}
	if len(a) == 0 {
		return 0, nil
	}
	var dot, na, nb float64
	for i := range a {
		ai := float64(a[i])
		bi := float64(b[i])
		dot += ai * bi
		na += ai * ai
		nb += bi * bi
	}
	denom := math.Sqrt(na) * math.Sqrt(nb)
	if denom == 0 {
		return 0, nil
	}
	return float32(dot / denom), nil
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS chunks (
    -- seq aliases the rowid via INTEGER PRIMARY KEY so it is STABLE across a
    -- VACUUM. The FTS5 external-content index below keys on this rowid; an
    -- implicit (TEXT PRIMARY KEY) rowid can be renumbered by VACUUM, which
    -- would silently desync the FTS index from the chunks table.
    seq         INTEGER PRIMARY KEY,
    id          TEXT NOT NULL UNIQUE,
    document_id TEXT NOT NULL,
    idx         INTEGER NOT NULL,
    text        TEXT NOT NULL,
    embedding   BLOB,
    metadata    TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS chunks_doc_idx ON chunks(document_id);
CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
    text,
    content='chunks', content_rowid='rowid'
);
CREATE TRIGGER IF NOT EXISTS chunks_ai AFTER INSERT ON chunks BEGIN
    INSERT INTO chunks_fts(rowid, text) VALUES (new.rowid, new.text);
END;
CREATE TRIGGER IF NOT EXISTS chunks_ad AFTER DELETE ON chunks BEGIN
    INSERT INTO chunks_fts(chunks_fts, rowid, text) VALUES('delete', old.rowid, old.text);
END;
CREATE TRIGGER IF NOT EXISTS chunks_au AFTER UPDATE ON chunks BEGIN
    INSERT INTO chunks_fts(chunks_fts, rowid, text) VALUES('delete', old.rowid, old.text);
    INSERT INTO chunks_fts(rowid, text) VALUES (new.rowid, new.text);
END;
`

const insertChunkSQL = `
INSERT INTO chunks(id, document_id, idx, text, embedding, metadata)
VALUES (?, ?, ?, ?, ?, ?)
`

const deleteChunkSQL = `DELETE FROM chunks WHERE id = ?`

const deleteByDocSQL = `DELETE FROM chunks WHERE document_id = ?`

func lexicalRetrieveSQL(where string) string {
	return `
SELECT c.id, c.document_id, c.idx, c.text, c.embedding, c.metadata, bm25(chunks_fts) AS score
FROM chunks_fts
JOIN chunks c ON c.rowid = chunks_fts.rowid
WHERE chunks_fts MATCH ?` + where + `
ORDER BY score ASC
LIMIT ?
`
}
