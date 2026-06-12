# ADR-015 — Schema-bound structured output

- **Status:** Accepted
- **Date:** 2026-06-12
- **Deciders:** Gustavo Yasser (BDFL)
- **Supersedes:** —
- **Superseded by:** —

## Context

`provider.Request.ResponseFormat` and `Capabilities.StructuredOutput`
already existed, and OpenAI and Google honored a `json_schema` format on
the wire. But there was no public, ergonomic way to go from a Go type to a
parsed structured result: `internal/jsonschema` (the reflection-derived
schema machinery the tool system uses) was internal, and callers had to
hand-assemble a `ResponseFormat`, set it, call `Generate`, and parse the
text themselves. Anthropic — the namesake/primary provider — reported
`StructuredOutput: false` and so couldn't participate at all.

## Decisions

### D1. Public API in `pkg/provider`

Two generic helpers, next to the `Request`/`Response`/`Generate` surface
they build on:

- `JSONSchemaFor[T any]() ([]byte, error)` — exposes the existing
  reflection schema derivation (a thin wrapper over `internal/jsonschema`)
  so callers can fill `ResponseFormat.Schema` themselves, or feed a tool.
- `GenerateStructured[T any](ctx, p, req) (T, error)` — derives `T`'s
  schema into `req.ResponseFormat` (when unset, a `json_schema` named after
  the type), calls `Generate`, and decodes the reply into `T` via
  `schema.ParseJSON` (tolerant of code fences / surrounding prose).

The derivation stays in `internal/jsonschema`; only a typed entry point is
exported. Object schemas are closed (`additionalProperties: false`) with a
`required` list, which is what strict modes expect.

### D2. Provider mapping

- **OpenAI / OpenAI-compatible / Google** — native `json_schema` (strict).
  Already wired; unchanged.
- **Anthropic** — has no `json_schema` response_format, so a schema request
  is expressed as **a single forced tool** whose `input_schema` is the
  requested schema (`tool_choice: {type: "tool", name}`). `Generate`
  unwraps the resulting `tool_use` input back into the message text, so the
  caller reads JSON exactly like the native providers. `StructuredOutput`
  flips to `true`.
- **Bedrock** — left at `StructuredOutput: false`. Bedrock fronts many
  model families; a single translation isn't correct across them, and the
  capability flag tells callers the truth. Revisit per-model later.

### D3. Capability-gated, parse on the way out

`Generate` rejects `ResponseFormat` on a provider that reports
`StructuredOutput: false` (via `ValidateRequest`), so a caller gets
`ErrUnsupported` rather than silent free-form text. `GenerateStructured`
always parses, returning a wrapped decode error when the model didn't
produce valid JSON.

## Consequences

- Structured output works against OpenAI, Google and Anthropic with the
  same two-line call; Bedrock is honestly out until per-model work lands.
- The Anthropic path reuses its tool machinery — no new wire surface, and
  it composes with the existing forced-tool plumbing.
- The reflection schema derivation now has one public door; the rest of
  `internal/jsonschema` stays internal.

## Out of scope

- Streaming structured output (the tool input would stream as a tool-call
  delta). `GenerateStructured` is single-shot via `Generate`.
- `$ref`/`$defs` for recursive types — the derivation still rejects cycles.
- Bedrock per-model structured output.

## References

- ADR-011 — `schema.ParseJSON[T]`, the tolerant decoder this builds on.
- ADR-004 — the tool system whose schema derivation is reused.
