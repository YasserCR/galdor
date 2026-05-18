// Package store is galdor's embedded persistence layer. Phase 4
// session B uses it to durably record OpenTelemetry spans so the
// CLI (galdor scry) and, later, the Web UI (Phase 5) can read
// them back.
//
// SQLite via modernc.org/sqlite (pure Go, no CGO) is the backend
// chosen in ADR-001 §D7. The package keeps the schema deliberately
// small: one table for spans, denormalized run IDs and JSON-blob
// attributes. Anything richer — full-text search, aggregations
// across many runs, time-series rollups — is a query problem
// answered by SQL on top of the same table, not a schema problem.
package store
