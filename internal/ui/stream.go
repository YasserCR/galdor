package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/YasserCR/galdor/internal/store"
)

// handleStreamRuns emits a Server-Sent Events feed of RunSummary
// rows as new spans land in the store. The connection stays open
// until the client disconnects (ctx cancellation) or the server
// shuts down. Cadence is controlled by the `interval` query
// parameter (milliseconds), defaulting to 1000ms — matches the
// scry tail CLI default.
//
// Events:
//
//	event: run         data: <RunSummary JSON>
//	event: heartbeat   data: <unix-ns int>
//
// The heartbeat exists so intermediaries (reverse proxies, dev
// browser inactivity timers) don't close an idle connection.
func (s *Server) handleStreamRuns(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // nginx hint

	interval := parsePositiveDuration(r.URL.Query().Get("interval"), time.Second)
	maxIters := parseMaxIters(r.URL.Query().Get("_max-iterations"))

	// Seed cursor at the current max so we don't replay historical
	// runs to a freshly connected client. Existing runs are already
	// in the static page render; we only stream what's new.
	cursor, err := s.store.MaxSpanStart(r.Context())
	if err != nil {
		http.Error(w, "stream: seed cursor: "+err.Error(), http.StatusInternalServerError)
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	emitted := map[string]int64{} // runID -> last emitted end-time, dedups updates

	iter := 0
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
		iter++

		spans, err := s.store.SpansSince(r.Context(), cursor, 500)
		if err != nil {
			writeSSEError(w, flusher, err)
			return
		}

		touched := map[string]struct{}{}
		for _, sp := range spans {
			cursor = max(cursor, sp.StartTimeUnixNano)
			touched[sp.TraceID] = struct{}{}
		}

		if len(touched) > 0 {
			runs, err := s.store.ListRuns(r.Context(), 500)
			if err != nil {
				writeSSEError(w, flusher, err)
				return
			}
			for _, run := range runs {
				if _, ok := touched[run.TraceID]; !ok {
					continue
				}
				if prev, ok := emitted[run.RunID]; ok && prev == run.EndTimeUnixNano {
					continue
				}
				emitted[run.RunID] = run.EndTimeUnixNano
				writeSSE(w, flusher, "run", run)
			}
		}

		writeSSE(w, flusher, "heartbeat", time.Now().UnixNano())

		if maxIters > 0 && iter >= maxIters {
			return
		}
	}
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, event string, payload any) {
	var b []byte
	switch v := payload.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	case store.RunSummary:
		b, _ = json.Marshal(v)
	default:
		b, _ = json.Marshal(v)
	}
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	flusher.Flush()
}

func writeSSEError(w http.ResponseWriter, flusher http.Flusher, err error) {
	writeSSE(w, flusher, "error", map[string]string{"error": err.Error()})
}

// minStreamInterval is the floor applied to the SSE poll cadence.
// Without it, interval=1us drives SpansSince/ListRuns against SQLite
// every tick — a cheap hot-loop DoS lever on an exposed instance.
// Mirrors the 10ms minimum the `scry tail` CLI enforces, but a touch
// higher since this path also re-queries ListRuns(500).
const minStreamInterval = 100 * time.Millisecond

func parsePositiveDuration(raw string, fallback time.Duration) time.Duration {
	d := fallback
	if raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			d = parsed
		} else if ms, err := strconv.Atoi(raw); err == nil && ms > 0 {
			d = time.Duration(ms) * time.Millisecond
		}
	}
	if d < minStreamInterval {
		return minStreamInterval
	}
	return d
}

// parseMaxIters lets the test harness bound a stream to a finite
// number of ticks. Production callers omit the flag; the loop runs
// until the client disconnects.
func parseMaxIters(raw string) int {
	if raw == "" {
		return 0
	}
	n, _ := strconv.Atoi(raw)
	return n
}
