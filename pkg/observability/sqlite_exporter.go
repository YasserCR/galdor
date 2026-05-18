package observability

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/YasserCR/galdor/internal/store"
)

// SQLiteExporter is an OpenTelemetry SpanExporter that persists
// spans into a galdor-managed SQLite database. The same database is
// what the CLI (galdor scry) and the Web UI (Phase 5) read from.
//
// Construction opens the database and applies the schema. Shutdown
// closes the handle. ExportSpans is safe to call from multiple
// goroutines (the underlying *store.Store is) and is idempotent
// after Shutdown — subsequent calls return ErrExporterShutdown.
type SQLiteExporter struct {
	store    *store.Store
	mu       sync.Mutex
	shutdown bool
}

// ErrExporterShutdown is returned by ExportSpans after Shutdown.
var ErrExporterShutdown = errors.New("observability: SQLite exporter is shut down")

// NewSQLiteExporter opens the database at path (creating it when
// missing) and returns a ready-to-use exporter. Pass the result to
// sdktrace.NewTracerProvider(WithBatcher(exp)) or WithSyncer.
func NewSQLiteExporter(path string) (*SQLiteExporter, error) {
	s, err := store.Open(context.Background(), path)
	if err != nil {
		return nil, fmt.Errorf("observability: open span store: %w", err)
	}
	return &SQLiteExporter{store: s}, nil
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

// Shutdown closes the underlying database. Subsequent ExportSpans
// calls return ErrExporterShutdown.
func (e *SQLiteExporter) Shutdown(_ context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.shutdown {
		return nil
	}
	e.shutdown = true
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
