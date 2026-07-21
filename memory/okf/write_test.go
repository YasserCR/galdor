package okf

import (
	"strings"
	"testing"
)

func TestMarshal_RoundTripsStandardFields(t *testing.T) {
	docs, _, err := Load(bundlePath())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// subscriptions exercises every standard field, including resource.
	subs := findDoc(t, docs, "tables/subscriptions")

	// Marshal, then load the rendered markdown back through the reader.
	reloaded, err := LoadBundleFS(newMapFS(map[string]string{
		"index.md":                "---\nokf_version: \"0.1\"\n---\n",
		"tables/subscriptions.md": string(Marshal(subs)),
	}), ".")
	if err != nil {
		t.Fatalf("LoadBundleFS: %v", err)
	}
	got, ok := reloaded.Concept("tables/subscriptions")
	if !ok {
		t.Fatal("reloaded concept missing")
	}
	for _, key := range []string{MetaType, MetaTitle, MetaDesc, MetaTags, MetaResource, MetaTimestamp} {
		if got.Metadata[key] != subs.Metadata[key] {
			t.Fatalf("field %q: got %q, want %q", key, got.Metadata[key], subs.Metadata[key])
		}
	}
	if strings.TrimSpace(got.Text) != strings.TrimSpace(subs.Text) {
		t.Fatalf("body not preserved:\n got: %q\nwant: %q", got.Text, subs.Text)
	}
}

func TestWriteBundle_RoundTrip(t *testing.T) {
	src := loadBundle(t)

	dir := t.TempDir()
	if err := WriteBundle(dir, src); err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}
	out, err := LoadBundle(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	// Concepts, version and the reserved files all survive the round trip.
	if len(out.Concepts) != len(src.Concepts) {
		t.Fatalf("concepts = %d, want %d", len(out.Concepts), len(src.Concepts))
	}
	if out.Version != src.Version {
		t.Fatalf("version = %q, want %q", out.Version, src.Version)
	}
	if idx, ok := out.Indexes["references/metrics"]; !ok || !strings.Contains(idx.Body, "MRR") {
		t.Fatalf("nested index not preserved: %+v", out.Indexes["references/metrics"])
	}
	// Log entries survive with their date groups and kind markers.
	srcLog, outLog := src.Logs[""], out.Logs[""]
	if len(outLog.Entries) != len(srcLog.Entries) {
		t.Fatalf("root log entries = %d, want %d: %+v", len(outLog.Entries), len(srcLog.Entries), outLog)
	}
	for i := range srcLog.Entries {
		if outLog.Entries[i] != srcLog.Entries[i] {
			t.Fatalf("log entry %d changed in round trip:\n got %+v\nwant %+v", i, outLog.Entries[i], srcLog.Entries[i])
		}
	}
	// A concept's standard metadata matches the source.
	srcSubs, _ := src.Concept("tables/subscriptions")
	outSubs, ok := out.Concept("tables/subscriptions")
	if !ok {
		t.Fatal("subscriptions missing after round trip")
	}
	for _, key := range []string{MetaType, MetaResource, MetaTimestamp, MetaTags} {
		if outSubs.Metadata[key] != srcSubs.Metadata[key] {
			t.Fatalf("field %q: got %q, want %q", key, outSubs.Metadata[key], srcSubs.Metadata[key])
		}
	}
	// The written bundle is itself conformant.
	if ps := out.Validate(); HasErrors(ps) {
		t.Fatalf("round-tripped bundle has validation errors: %v", ps)
	}
}
