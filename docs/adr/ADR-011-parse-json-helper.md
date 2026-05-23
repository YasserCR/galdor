# ADR-011 — `schema.ParseJSON[T]` tolerant JSON helper

- **Status:** Accepted
- **Date:** 2026-05-23
- **Deciders:** Gustavo Yasser (BDFL)
- **Supersedes:** —
- **Superseded by:** —

## Context

The first post-v0.1.0 integrator report ("Galdor — Feedback from a Real
Migration", 2026-05-20) identified the absence of a stdlib-level helper
for parsing LLM JSON output as the single largest source of boilerplate
when porting from Python/LangChain.

Today every integrator who returns structured data from a non-schema-
bound `Provider.Generate` call writes their own variant of:

1. Strip the `\`\`\`json ... \`\`\`` (or bare `\`\`\``) Markdown fences the model
   tends to wrap around JSON.
2. Trim leading and trailing prose ("Sure! Here's the JSON: ...").
3. `json.Unmarshal` into a tolerant struct that accepts strings where
   numbers were requested, because the model occasionally drifts.

`schema.JSONOf[T]` (Phase 12) will eventually eliminate this for
providers that support real schema binding. Until then, **and** for
providers that don't honor the schema even when claimed, a tolerant
parser remains useful. This ADR fixes the shape of that helper so it
doesn't drift toward LangChain's `JsonOutputParser` (re-prompt on
failure, repair via LLM, etc.) which would betray galdor's stated
non-goals.

## Decisions

### D1. Generic `func ParseJSON[T any](raw string) (T, error)`

A single generic function in `pkg/schema`. Type parameter binds the
target shape; no separate `MapParser` / `ListParser` / `PydanticParser`
zoo:

```go
parsed, err := schema.ParseJSON[InterpretResponse](resp.Message.Text())
```

This is the Go-native form of `JsonOutputParser` — one function, one
target type, no runtime polymorphism.

### D2. What ParseJSON tolerates

Three normalization steps run before `json.Unmarshal`:

1. **Outer whitespace** trimmed.
2. **Markdown code fences** stripped — `\`\`\`json`, `\`\`\`JSON`, bare
   `\`\`\``. Optional language tag is honored.
3. **Surrounding prose** removed by extracting the first `{`...`}` (or
   `[`...`]`) substring.

If the trimmed text already begins with a valid JSON-start token
(`{`, `[`, `"`, digit, `-`, `t`, `f`, `n`), the extraction step is
skipped to avoid corrupting clean input.

### D3. What ParseJSON explicitly does NOT do

The following are out of scope and will stay that way:

- **Repair malformed JSON.** Trailing commas, single-quoted strings,
  unquoted keys, JavaScript-flavored numbers, etc. all fail. They
  indicate a non-JSON-tuned model or a wrong prompt; silent recovery
  hides real bugs that callers need to see. A regression test
  documents the trailing-comma rejection so a future contributor does
  not "fix" it.
- **Re-prompt the model.** LangChain's `OutputFixingParser` re-issues
  the request when parsing fails. ParseJSON is a pure stdlib helper
  with no provider dependency. Caller code can build that pattern in
  five lines using `provider.Provider` directly when warranted; it
  does not belong in `pkg/schema`.
- **Schema validation.** ParseJSON does not check whether required
  fields are present, enum values are valid, or types match a deeper
  schema. Strong validation is `schema.JSONOf[T]`'s job (Phase 12,
  provider-side enforcement) and standard `go-playground/validator`-
  style libraries' job for caller-side enforcement.
- **Streaming or partial parsing.** ParseJSON consumes the full string
  once. Streaming JSON parsers exist as separate libraries; reaching
  for one is a different design discussion.

### D4. Failure shape: `*schema.BadOutputError`

ParseJSON returns `(T, error)` where error, when non-nil, is always a
`*BadOutputError` populated with:

- `Provider: "schema"` — identifies the parser as the source.
- `Raw` — the original input, capped at 2048 bytes with a truncation
  marker, so error logs never explode on a wall of LLM prose.
- `Reason` — short human description ("no JSON payload found",
  "invalid JSON: ...").
- `Cause` — the underlying `*json.SyntaxError` /
  `*json.UnmarshalTypeError` when the failure originated in
  `encoding/json`, so callers can do `errors.As(err, &jsonErr)` for
  finer classification.

`BadOutputError` lives in `pkg/schema` rather than `pkg/provider`
because `pkg/provider` already imports `pkg/schema` and the reverse
direction is a cycle. ADR-012's D2 originally placed the type in
`pkg/provider`; that placement is revised in the same commit that
adds ParseJSON.

### D5. Size cap on captured raw

`Raw` is capped at 2048 bytes. Larger payloads are truncated with a
trailing `...(truncated)` marker. Rationale:

- LLMs occasionally emit multi-kilobyte garbage on a malformed prompt;
  preserving all of it in an error message floods logs and breaks
  structured logging backends.
- 2048 bytes is enough to see the start of the payload, the offending
  region in most cases, and any code fence the model wrapped around
  it.
- Callers that need the full text can wrap ParseJSON and capture the
  raw input themselves before passing it in. The helper does not pretend
  to be a forensic tool.

## Consequences

- **Removes ~30 lines of fence-stripping + permissive-struct code from
  every integrator's repo.** The integrator report cites this as the
  single most-requested feature when porting from LangChain.
- **Compatible with the eventual `JSONOf[T]` flow.** When a provider
  honors `ResponseFormat: schema.JSONOf[T]{}`, the returned
  `Response.Parsed[T]` is preferred. When the provider claims schema
  support but emits non-conforming output (real-world Gemini behavior
  on edge cases), `ParseJSON[T]` is the fallback. The two share
  `BadOutputError` so caller error-handling is uniform.
- **`BadOutputError` declaration moves from `pkg/provider` to
  `pkg/schema`** in the same commit that introduces ParseJSON. The
  type was added in a prior commit (ADR-012) and never released, so
  this is not a breaking change.

## Alternatives considered

- **Use `encoding/json` directly and document the patterns.** Rejected
  — fence stripping and prose tolerance are a real recurring task; a
  10-line helper saves every downstream user from solving it. Stdlib
  is the floor, not the ceiling.
- **Provide a constructor: `parser := schema.NewJSONParser[T]()` with
  configurable knobs.** Rejected — three normalization steps with no
  optional behavior is too small a surface to justify a builder.
  Adding knobs later is additive; removing them is not.
- **Add LLM-driven repair.** Rejected on identity grounds. LangChain's
  `OutputFixingParser` is the prototype of what galdor explicitly
  refuses to be (Acceptance Principle, ROADMAP.md). Failure here is
  signal, not a bug to paper over.
