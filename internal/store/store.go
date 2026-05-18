package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	_ "modernc.org/sqlite" // sqlite driver registration (pure Go)
)

// Span is the persisted form of an OpenTelemetry span. It is
// deliberately decoupled from sdktrace.ReadOnlySpan so callers (the
// CLI, the UI, future replay engine) don't take an OTel SDK
// dependency just to read trace history.
type Span struct {
	SpanID            string // 16-hex span ID
	TraceID           string // 32-hex trace ID
	ParentSpanID      string // empty for root spans
	Name              string // e.g. "galdor.graph.run"
	StartTimeUnixNano int64
	EndTimeUnixNano   int64
	StatusCode        string // "unset" | "ok" | "error"
	StatusMessage     string
	Attributes        map[string]any // JSON-friendly values
	Events            []Event
	RunID             string // denormalized from attributes for fast filtering
}

// Event is one span event (RecordError adds these, mostly).
type Event struct {
	Name         string         `json:"name"`
	TimeUnixNano int64          `json:"time_unix_nano"`
	Attributes   map[string]any `json:"attributes,omitempty"`
}

// Duration returns the span's duration in nanoseconds.
func (s Span) Duration() int64 {
	if s.EndTimeUnixNano == 0 || s.StartTimeUnixNano == 0 {
		return 0
	}
	return s.EndTimeUnixNano - s.StartTimeUnixNano
}

// RunSummary is the aggregated view of a run that the CLI list
// command renders. One row per distinct galdor.run.id observed in
// the spans table.
type RunSummary struct {
	RunID             string
	TraceID           string
	StartTimeUnixNano int64
	EndTimeUnixNano   int64
	SpanCount         int
	ErrorCount        int
}

// Status reports "ok" or "error" based on whether any span in the
// run carried StatusCode = error.
func (r RunSummary) Status() string {
	if r.ErrorCount > 0 {
		return "error"
	}
	return "ok"
}

// Duration returns end-start in nanoseconds.
func (r RunSummary) Duration() int64 {
	if r.EndTimeUnixNano == 0 || r.StartTimeUnixNano == 0 {
		return 0
	}
	return r.EndTimeUnixNano - r.StartTimeUnixNano
}

// Store is the SQLite-backed span store. Open returns a ready-to-use
// instance; Close releases the underlying DB handle. Concurrent
// Insert / Query calls are safe.
type Store struct {
	db     *sql.DB
	closer sync.Once
}

// Open opens (or creates) the SQLite database at path. The schema
// is applied unconditionally — schema migrations beyond the
// initial layout are tracked in dedicated functions when needed.
//
// path may be ":memory:" for an in-memory DB (useful in tests).
// ctx is used for the initial schema-apply statement; subsequent
// queries take their own ctx via the Store methods.
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("store: empty path")
	}
	// modernc.org/sqlite uses the "sqlite" driver name.
	dsn := path
	// PRAGMAs to tune for write-heavy span ingestion plus concurrent
	// CLI reads. journal_mode=WAL avoids reader blocks; busy_timeout
	// smoothes over the occasional contention.
	if !strings.HasPrefix(path, ":") {
		dsn = path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	// SQLite plays best with a single writer; concurrent reads still
	// work fine with WAL. Use small pool to keep contention low.
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(2)

	s := &Store{db: db}
	if err := s.applySchema(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the underlying DB handle. Safe to call multiple times.
func (s *Store) Close() error {
	var err error
	s.closer.Do(func() {
		err = s.db.Close()
	})
	return err
}

// DB exposes the raw sql.DB for advanced use (custom queries, tests).
// Callers should not mutate schema or close it.
func (s *Store) DB() *sql.DB { return s.db }

const schemaSQL = `
CREATE TABLE IF NOT EXISTS spans (
	span_id              TEXT PRIMARY KEY,
	trace_id             TEXT NOT NULL,
	parent_span_id       TEXT NOT NULL DEFAULT '',
	name                 TEXT NOT NULL,
	start_time_unix_nano INTEGER NOT NULL,
	end_time_unix_nano   INTEGER NOT NULL,
	status_code          TEXT NOT NULL DEFAULT 'unset',
	status_message       TEXT NOT NULL DEFAULT '',
	attrs_json           TEXT NOT NULL DEFAULT '{}',
	events_json          TEXT NOT NULL DEFAULT '[]',
	run_id               TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_spans_trace_id ON spans(trace_id);
CREATE INDEX IF NOT EXISTS idx_spans_run_id   ON spans(run_id);
CREATE INDEX IF NOT EXISTS idx_spans_parent   ON spans(parent_span_id);
CREATE INDEX IF NOT EXISTS idx_spans_start    ON spans(start_time_unix_nano);
`

func (s *Store) applySchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, schemaSQL)
	if err != nil {
		return fmt.Errorf("store: apply schema: %w", err)
	}
	return nil
}

// InsertSpans persists the given spans in a single transaction.
// Duplicate span IDs (same primary key) are rejected by SQLite,
// not silently overwritten — duplicates indicate a producer bug.
func (s *Store) InsertSpans(ctx context.Context, spans []Span) error {
	if len(spans) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin tx: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO spans
			(span_id, trace_id, parent_span_id, name,
			 start_time_unix_nano, end_time_unix_nano,
			 status_code, status_message, attrs_json, events_json, run_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("store: prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, sp := range spans {
		attrs, err := jsonMarshal(sp.Attributes)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: marshal attrs: %w", err)
		}
		events := []byte("[]")
		if len(sp.Events) > 0 {
			events, err = json.Marshal(sp.Events)
			if err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("store: marshal events: %w", err)
			}
		}
		if _, err := stmt.ExecContext(ctx,
			sp.SpanID, sp.TraceID, sp.ParentSpanID, sp.Name,
			sp.StartTimeUnixNano, sp.EndTimeUnixNano,
			normalizeStatus(sp.StatusCode), sp.StatusMessage,
			string(attrs), string(events), sp.RunID,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: insert span %s: %w", sp.SpanID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// ListRuns returns the most recent runs (descending by start time).
// Aggregation is by trace_id so spans emitted by InstrumentProvider
// or InstrumentTool — which do not carry the galdor.run.id
// attribute themselves — still count toward their run.
func (s *Store) ListRuns(ctx context.Context, limit int) ([]RunSummary, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			(SELECT run_id FROM spans r
			 WHERE r.trace_id = s.trace_id AND r.run_id <> ''
			 LIMIT 1) AS run_id,
			s.trace_id,
			MIN(s.start_time_unix_nano) AS start_t,
			MAX(s.end_time_unix_nano)   AS end_t,
			COUNT(*) AS span_count,
			SUM(CASE WHEN s.status_code = 'error' THEN 1 ELSE 0 END) AS error_count
		FROM spans s
		WHERE s.trace_id IN (SELECT trace_id FROM spans WHERE run_id <> '')
		GROUP BY s.trace_id
		ORDER BY start_t DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list runs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []RunSummary
	for rows.Next() {
		var r RunSummary
		if err := rows.Scan(&r.RunID, &r.TraceID, &r.StartTimeUnixNano, &r.EndTimeUnixNano,
			&r.SpanCount, &r.ErrorCount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SpansForRun returns every span belonging to runID, ordered by
// start time ascending. It resolves runID -> trace_id via the
// root span and then fetches every span sharing that trace_id, so
// descendants (provider.generate, tool.execute) are included even
// though they don't carry the run_id attribute themselves.
//
// The returned slice is suitable for direct tree assembly:
// parents always come before their children when the span tree
// was emitted in normal start->end order.
func (s *Store) SpansForRun(ctx context.Context, runID string) ([]Span, error) {
	if runID == "" {
		return nil, errors.New("store: empty runID")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT span_id, trace_id, parent_span_id, name,
		       start_time_unix_nano, end_time_unix_nano,
		       status_code, status_message, attrs_json, events_json, run_id
		FROM spans
		WHERE trace_id = (SELECT trace_id FROM spans WHERE run_id = ? LIMIT 1)
		ORDER BY start_time_unix_nano ASC`, runID)
	if err != nil {
		return nil, fmt.Errorf("store: spans for run: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Span
	for rows.Next() {
		sp, err := scanSpan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// SpanCount returns the total number of spans stored. Useful for
// tests and quick health checks.
func (s *Store) SpanCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM spans`).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func scanSpan(rows *sql.Rows) (Span, error) {
	var sp Span
	var attrs, events string
	if err := rows.Scan(
		&sp.SpanID, &sp.TraceID, &sp.ParentSpanID, &sp.Name,
		&sp.StartTimeUnixNano, &sp.EndTimeUnixNano,
		&sp.StatusCode, &sp.StatusMessage, &attrs, &events, &sp.RunID,
	); err != nil {
		return Span{}, err
	}
	if attrs != "" && attrs != "{}" {
		if err := json.Unmarshal([]byte(attrs), &sp.Attributes); err != nil {
			return Span{}, fmt.Errorf("store: decode attrs for span %s: %w", sp.SpanID, err)
		}
	}
	if events != "" && events != "[]" {
		if err := json.Unmarshal([]byte(events), &sp.Events); err != nil {
			return Span{}, fmt.Errorf("store: decode events for span %s: %w", sp.SpanID, err)
		}
	}
	return sp, nil
}

// jsonMarshal produces a deterministic representation of m. SQLite
// stores it as TEXT; the CLI reads it back unchanged.
func jsonMarshal(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return []byte("{}"), nil
	}
	return json.Marshal(m)
}

func normalizeStatus(s string) string {
	switch strings.ToLower(s) {
	case "ok":
		return "ok"
	case "error":
		return "error"
	default:
		return "unset"
	}
}
