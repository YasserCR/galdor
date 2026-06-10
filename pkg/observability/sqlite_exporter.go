package observability

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/YasserCR/galdor/internal/store"
)

// SQLiteExporter is an OpenTelemetry SpanExporter that persists
// spans into a galdor-managed SQLite database. The same database is
// what the CLI (galdor scry) and the Web UI (Phase 5) read from.
//
// Construction opens the database, applies the schema, and starts a
// background goroutine that periodically runs PRAGMA wal_checkpoint
// so the dashboard sees fresh data even while the exporter's
// connection is held open by a long-running daemon. Shutdown stops
// the goroutine, runs a final TRUNCATE checkpoint, and closes the
// handle. ExportSpans is safe to call from multiple goroutines (the
// underlying *store.Store is) and is idempotent after Shutdown —
// subsequent calls return ErrExporterShutdown.
type SQLiteExporter struct {
	store              *store.Store
	mu                 sync.Mutex
	shutdown           bool
	checkpointInterval time.Duration
	checkpointLogger   *slog.Logger
	ckptDone           chan struct{}
	ckptWG             sync.WaitGroup
}

// ErrExporterShutdown is returned by ExportSpans after Shutdown.
var ErrExporterShutdown = errors.New("observability: SQLite exporter is shut down")

// DefaultCheckpointInterval is the period between background
// PRAGMA wal_checkpoint(PASSIVE) calls when the caller does not
// override it via WithCheckpointInterval.
const DefaultCheckpointInterval = 3 * time.Second

// ExporterOption configures the SQLiteExporter at construction.
type ExporterOption func(*exporterConfig)

type exporterConfig struct {
	checkpointInterval time.Duration
	checkpointLogger   *slog.Logger
}

// WithCheckpointInterval sets how often the exporter runs PRAGMA
// wal_checkpoint(PASSIVE) in the background. Zero disables the
// goroutine entirely (use only when you have an external
// checkpointer, e.g. another process holding the same DB open).
// Negative values are clamped to the default.
func WithCheckpointInterval(d time.Duration) ExporterOption {
	return func(c *exporterConfig) { c.checkpointInterval = d }
}

// WithCheckpointLogger plumbs a slog.Logger into the background
// checkpoint goroutine; checkpoint errors are logged at warn level.
// nil logger silences output (the default).
func WithCheckpointLogger(l *slog.Logger) ExporterOption {
	return func(c *exporterConfig) { c.checkpointLogger = l }
}

// NewSQLiteExporter opens the database at path (creating it when
// missing) and returns a ready-to-use exporter. Pass the result to
// sdktrace.NewTracerProvider(WithBatcher(exp)) or WithSyncer.
//
// The exporter starts a background goroutine that runs PRAGMA
// wal_checkpoint(PASSIVE) every DefaultCheckpointInterval so the
// dashboard (or a sibling `galdor scry` process) sees freshly
// ingested spans without having to wait for the WAL autocheckpoint
// threshold or for this process to exit. See WithCheckpointInterval
// to tune or disable.
func NewSQLiteExporter(path string, opts ...ExporterOption) (*SQLiteExporter, error) {
	cfg := exporterConfig{checkpointInterval: DefaultCheckpointInterval}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.checkpointInterval < 0 {
		cfg.checkpointInterval = DefaultCheckpointInterval
	}

	// Ensure the parent directory exists. The documented default lives
	// at ~/.galdor/traces.db, which doesn't exist on a fresh machine —
	// without this, recording to the default path fails with a cryptic
	// "unable to open database file (14)".
	if !strings.HasPrefix(path, ":") {
		if dir := filepath.Dir(path); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return nil, fmt.Errorf("observability: create span store dir %q: %w", dir, err)
			}
		}
	}

	s, err := store.Open(context.Background(), path)
	if err != nil {
		return nil, fmt.Errorf("observability: open span store: %w", err)
	}
	e := &SQLiteExporter{
		store:              s,
		checkpointInterval: cfg.checkpointInterval,
		checkpointLogger:   cfg.checkpointLogger,
	}
	if cfg.checkpointInterval > 0 {
		e.ckptDone = make(chan struct{})
		e.ckptWG.Add(1)
		go e.runCheckpointer()
	}
	return e, nil
}

// runCheckpointer ticks at checkpointInterval and runs a PASSIVE
// checkpoint against the underlying store. PASSIVE never blocks
// writers and is the safe periodic choice. Exits cleanly when
// ckptDone is closed.
func (e *SQLiteExporter) runCheckpointer() {
	defer e.ckptWG.Done()
	t := time.NewTicker(e.checkpointInterval)
	defer t.Stop()
	for {
		select {
		case <-e.ckptDone:
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := e.store.Checkpoint(ctx, "PASSIVE")
			cancel()
			if err != nil && e.checkpointLogger != nil {
				e.checkpointLogger.Warn("observability: wal_checkpoint failed",
					slog.String("err", err.Error()))
			}
		}
	}
}

// ExportSpans implements sdktrace.SpanExporter.
func (e *SQLiteExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	if e.shutdown {
		e.mu.Unlock()
		return ErrExporterShutdown
	}
	e.mu.Unlock()

	if len(spans) == 0 {
		return nil
	}

	converted := make([]store.Span, 0, len(spans))
	for _, ros := range spans {
		converted = append(converted, convertSpan(ros))
	}
	return e.store.InsertSpans(ctx, converted)
}

// Shutdown stops the background checkpoint goroutine, runs a final
// PRAGMA wal_checkpoint(TRUNCATE) so the .db-wal file is dropped to
// zero bytes, and closes the underlying database handle. Subsequent
// ExportSpans calls return ErrExporterShutdown.
func (e *SQLiteExporter) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	if e.shutdown {
		e.mu.Unlock()
		return nil
	}
	e.shutdown = true
	e.mu.Unlock()

	if e.ckptDone != nil {
		close(e.ckptDone)
		e.ckptWG.Wait()
	}
	// Best-effort final checkpoint. Ignore errors: even if this fails
	// (caller passed a cancelled ctx, for instance) we still want to
	// close the DB handle below.
	_ = e.store.Checkpoint(ctx, "TRUNCATE")
	return e.store.Close()
}

// Store exposes the underlying *store.Store so callers can re-use
// it for direct queries (the CLI does this). Do not call Close on
// the returned store — call Exporter.Shutdown instead.
func (e *SQLiteExporter) Store() *store.Store { return e.store }

// convertSpan translates an OTel ReadOnlySpan into the store's
// persistence shape. Attributes are flattened into a map[string]any
// per the JSON schema in pkg/store; the galdor.run.id attribute is
// promoted into the dedicated RunID column for fast filtering.
func convertSpan(ros sdktrace.ReadOnlySpan) store.Span {
	sc := ros.SpanContext()
	parentID := ""
	if p := ros.Parent(); p.IsValid() {
		parentID = p.SpanID().String()
	}

	attrs := map[string]any{}
	for _, a := range ros.Attributes() {
		if v, ok := attrValueToGo(a.Value); ok {
			attrs[string(a.Key)] = v
		}
	}

	runID, _ := attrs[AttrGaldorRunID].(string)

	status := ros.Status()
	statusCode := "unset"
	switch status.Code.String() {
	case "Ok":
		statusCode = "ok"
	case "Error":
		statusCode = "error"
	}

	events := make([]store.Event, 0, len(ros.Events()))
	for _, ev := range ros.Events() {
		evAttrs := map[string]any{}
		for _, a := range ev.Attributes {
			if v, ok := attrValueToGo(a.Value); ok {
				evAttrs[string(a.Key)] = v
			}
		}
		events = append(events, store.Event{
			Name:         ev.Name,
			TimeUnixNano: ev.Time.UnixNano(),
			Attributes:   evAttrs,
		})
	}

	return store.Span{
		SpanID:            sc.SpanID().String(),
		TraceID:           sc.TraceID().String(),
		ParentSpanID:      parentID,
		Name:              ros.Name(),
		StartTimeUnixNano: ros.StartTime().UnixNano(),
		EndTimeUnixNano:   ros.EndTime().UnixNano(),
		StatusCode:        statusCode,
		StatusMessage:     status.Description,
		Attributes:        attrs,
		Events:            events,
		RunID:             runID,
	}
}

// attrValueToGo extracts a Go-native value from an OTel attribute.Value.
// Slices of basic types are preserved; INVALID values are dropped.
func attrValueToGo(v attribute.Value) (any, bool) {
	switch v.Type() {
	case attribute.STRING:
		return v.AsString(), true
	case attribute.BOOL:
		return v.AsBool(), true
	case attribute.INT64:
		return v.AsInt64(), true
	case attribute.FLOAT64:
		return v.AsFloat64(), true
	case attribute.STRINGSLICE:
		return v.AsStringSlice(), true
	case attribute.BOOLSLICE:
		return v.AsBoolSlice(), true
	case attribute.INT64SLICE:
		return v.AsInt64Slice(), true
	case attribute.FLOAT64SLICE:
		return v.AsFloat64Slice(), true
	default:
		return nil, false
	}
}
