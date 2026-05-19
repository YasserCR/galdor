// Package replay reproduces a past agent run from its recorded trace.
//
// A Provider implementation here wraps a list of RecordedCall values
// — prompts paired with the responses the real LLM produced — and
// serves them back to the agent during a re-run. Plug it into
// agent.Config or any caller that takes a provider.Provider and the
// system behaves exactly as before, without spending tokens, without
// network calls, and (when the model is deterministic at the wire
// level) without flakiness.
//
// What this unlocks:
//
//   - Regression tests for prompts and agents that don't touch a
//     real LLM but exercise the full graph + tool + memory chain
//     against historical, real-world inputs.
//   - Cheap debugging: load a production run, re-run it locally
//     under a debugger.
//   - Deterministic CI: paid-API runs once, becomes a fixture,
//     subsequent runs use the fixture.
//
// Two matching modes:
//
//   - ModeStrict (default): call N must match recording N exactly.
//     Use this when the graph hasn't changed between recording and
//     replay — the safest setting, fails loudly on drift.
//   - ModeLenient: match by a hash of the prompt messages. Order
//     doesn't matter; works even when the graph has been
//     restructured, as long as the same prompts surface at some
//     point.
//
// Loading sources:
//
//   - LoadFromStore reads spans from a SQLite trace DB (the same
//     store the embedded dashboard reads).
//   - LoadFromFile / SaveToFile use a portable JSON fixture format.
//   - You can also build a []RecordedCall by hand for unit tests.
//
// Required: the original run must have been recorded with
// observability.WithCaptureContent(true). Without it, the spans
// don't carry the prompt or completion bodies and replay is
// impossible. The loader returns a clear error in that case.
package replay
