// Package ui serves galdor's embedded observability dashboard.
//
// NewServer returns an http.Handler over a *store.Store: a run list
// page at "/", a per-run span tree at "/runs/{id}", a small JSON API
// at "/api/*", and embedded static assets. Templates and CSS are
// compiled into the binary via embed.FS, so the framework stays a
// single artefact even with the UI enabled.
//
// Wiring lives in cmd/galdor/ui.go (`galdor ui`); tests treat the
// handler as an http.Handler and exercise it through httptest.
package ui
