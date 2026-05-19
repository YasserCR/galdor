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
	rows, err := s.db.QueryContext(ctx, lexicalRetrieveSQL, match, k*4) // overfetch for post-filtering
	if err != nil {
		return nil, fmt.Errorf("memory/sqlite: fts query: %w", err)
	}
	defer rows.Close()
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
		if !matchesFilter(c.Metadata, q.Filter) {
			continue
		}
		results = append(results, memory.Result{Chunk: c, Score: float32(-bm25)})
		if len(results) >= k {
			break
		}
	}
	return results, rows.Err()
}

func (s *Store) vectorRetrieve(ctx context.Context, q memory.Query, k int) ([]memory.Result, error) {
	rows, err := s.db.QueryContext(ctx, scanAllSQL)
	if err != nil {
		return nil, fmt.Errorf("memory/sqlite: scan query: %w", err)
	}
	defer rows.Close()
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
		if !matchesFilter(c.Metadata, q.Filter) {
			continue
		}
		if len(c.Embedding) == 0 {
			continue
		}
		score := cosine(q.Embedding, c.Embedding)
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

// buildFTSMatch turns a free-form query into an FTS5 MATCH clause.
// Stop characters that FTS5 treats as operators are stripped to
// avoid syntax errors on user-typed queries. Terms are joined with
// OR so any of them contributes to the score (BM25 handles the
// weighting).
func buildFTSMatch(query string) string {
	var terms []string
	for _, raw := range strings.Fields(query) {
		t := sanitizeFTSTerm(raw)
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

func sanitizeFTSTerm(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '"', '*', '(', ')', ':', '-', '+', '^', '~':
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

func matchesFilter(meta, filter map[string]string) bool {
	for k, v := range filter {
		if meta[k] != v {
			return false
		}
	}
	return true
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
func cosine(a, b []float32) float32 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := 0; i < n; i++ {
		ai := float64(a[i])
		bi := float64(b[i])
		dot += ai * bi
		na += ai * ai
		nb += bi * bi
	}
	denom := math.Sqrt(na) * math.Sqrt(nb)
	if denom == 0 {
		return 0
	}
	return float32(dot / denom)
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS chunks (
    id          TEXT PRIMARY KEY,
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

const lexicalRetrieveSQL = `
SELECT c.id, c.document_id, c.idx, c.text, c.embedding, c.metadata, bm25(chunks_fts) AS score
FROM chunks_fts
JOIN chunks c ON c.rowid = chunks_fts.rowid
WHERE chunks_fts MATCH ?
ORDER BY score ASC
LIMIT ?
`

const scanAllSQL = `
SELECT id, document_id, idx, text, embedding, metadata
FROM chunks
`
