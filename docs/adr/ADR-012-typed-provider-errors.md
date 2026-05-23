# ADR-012 — Typed provider errors

- **Status:** Accepted
- **Date:** 2026-05-23
- **Deciders:** Gustavo Yasser (BDFL)
- **Supersedes:** —
- **Superseded by:** —

## Context

The first post-v0.1.0 integrator report ("Galdor — Feedback from a Real
Migration", 2026-05-20) flagged that `Provider.Generate` errors are
effectively opaque to callers writing their own retry, fallback, or
alerting logic. The reported shape today:

```go
resp, err := p.Generate(ctx, req)
if err != nil {
    var apiErr *provider.APIError
    if errors.As(err, &apiErr) {
        switch apiErr.Kind {
        case provider.ErrRateLimited:
            time.Sleep(time.Duration(apiErr.RetryAfter) * time.Second)
            // retry...
        case provider.ErrAuth:
            log.Fatal(...)
        }
    }
}
```

The integrator wanted instead:

```go
var rl *provider.RateLimitError
if errors.As(err, &rl) {
    time.Sleep(rl.RetryAfter)
    return retry()
}
```

This is the canonical Go pattern (`errors.As` matches a typed sentinel and
gives you a handle with that type's fields), and it composes with
`provider.RetryPolicy` (Phase 11, separate item) and with caller-side
classification logic without re-implementing what `IsRetryable` already
does.

The existing `provider.Retry` middleware already classifies internally via
`errors.Is(err, ErrRateLimited)` on the unwrap chain, so the retry path is
unaffected. The gap is purely on the **caller-facing surface** when
callers want their own retry, fallback, or telemetry logic.

## Decisions

### D1. Add typed error structs alongside (not replacing) `APIError`

`APIError` stays. It is the canonical normalized shape adapters build at
the failure boundary. The new typed structs are **thin wrappers** that
embed `*APIError`:

```go
type RateLimitError      struct{ *APIError }
type AuthError           struct{ *APIError }
type InvalidRequestError struct{ *APIError }
type TransientError      struct{ *APIError } // 5xx / ErrServer
type ContextLengthError  struct{ *APIError }
type UnsupportedError    struct{ *APIError }
```

Each wrapper exposes:

- `Error() string` — inherited via the embedded `*APIError`.
- `Unwrap() error` — returns the embedded `*APIError` so existing
  `errors.As(err, &apiErr)` keeps finding the `APIError`, and existing
  `errors.Is(err, sentinel)` keeps matching via the chain
  `*RateLimitError` → `*APIError` → `Kind`.

Pointer-embed (not value-embed) so the wrappers share storage with the
underlying APIError and zero-value wrappers are clearly broken (nil
pointer) rather than silently empty.

### D2. `BadOutputError` does not embed `APIError`

Output-parsing failures happen *after* a successful HTTP call, so they
don't fit the `APIError` mold (no StatusCode, no Provider-side message).
`BadOutputError` is its own shape:

```go
type BadOutputError struct {
    Provider string // adapter name (or "schema" for parser-side failures)
    Raw      string // the raw bytes/text that failed to parse
    Reason   string // short human description of what went wrong
    cause    error  // wrapped underlying error (json.SyntaxError, ...)
}

func (e *BadOutputError) Unwrap() error { return e.cause }
```

This is the type returned by `schema.ParseJSON[T]` (Phase 11, separate
item) and by future schema-bound `JSONOf[T]` paths (Phase 12). Including
it here avoids defining two error shapes for what is conceptually the
same failure mode.

### D3. A `Classify` constructor wraps an `APIError` in the right type

Adapters today build `&APIError{Kind: ErrRateLimited, ...}` and return
it. The minimal-touch migration is to wrap that return in a classifier:

```go
return provider.Classify(&provider.APIError{
    Kind: provider.ErrRateLimited,
    ...
})
```

`Classify` switches on `Kind`, wraps in the corresponding typed struct,
and returns the wrapper. When `Kind` is nil or unrecognized, it returns
the `*APIError` unchanged — this is intentionally defensive: an adapter
that forgets to set Kind still returns a usable error, just without the
type-narrowed handle.

### D4. Wrappers are returned as `error`, not as concrete types

Adapters keep their function signatures returning plain `error`. The
typed structs are accessed via `errors.As` at the caller, never via type
assertion. This preserves the option to change the wrapper hierarchy
later without breaking signatures, and avoids leaking `*RateLimitError`
into places where `error` is the right abstraction (logging, hooks,
spans).

### D5. The `Retry` middleware keeps using `errors.Is` + `*APIError`

`provider.Retry` and `IsRetryable` continue to classify via the unwrap
chain. They do not need to know about the new types because:

1. `errors.Is(err, ErrRateLimited)` matches both `*APIError{Kind: ErrRateLimited}`
   and `*RateLimitError{APIError: ...}` via the chain.
2. `errors.As(err, &apiErr)` still finds the embedded `*APIError` for
   reading `RetryAfter`.

This is what makes the change purely additive: zero changes inside the
existing retry path; tests for retry continue to pass with no
modifications.

### D6. `RetryAfter` remains seconds-as-int on `APIError`

We considered surfacing `RetryAfter` as `time.Duration` on
`*RateLimitError`. Rejected because:

- It would shadow / contradict `APIError.RetryAfter` (int seconds) and
  invite confusion about which is authoritative.
- The single existing consumer (`provider.Retry.nextDelay`) reads
  `APIError.RetryAfter` directly; changing the type would force a
  reflective conversion or a dual field.

Callers wanting a `time.Duration` can do
`time.Duration(rl.RetryAfter) * time.Second`. A convenience method
`(*RateLimitError).RetryAfterDuration() time.Duration` may be added if
it stops being trivial; for now the explicit conversion is fine.

## Consequences

- **No breaking changes.** All existing test code using `*APIError`
  directly, all existing callers using `errors.Is(err, ErrRateLimited)`,
  and the entire `Retry` middleware continue to work without
  modification.
- **Adapter migration is one-line per failure site.** Replace
  `return &APIError{...}` with `return Classify(&APIError{...})`. The
  five existing call sites in anthropic / openai / google / bedrock are
  updated in this phase.
- **`BadOutputError` arrives before `ParseJSON[T]`.** That is the
  intended dependency order — `ParseJSON[T]`'s returned error needs a
  type, and defining it once here (not separately in `pkg/schema`)
  prevents duplication when `JSONOf[T]` lands in Phase 12 and uses the
  same shape.
- **Documentation impact.** `docs/patterns/direct-provider.md` (Phase 11,
  separate item) demonstrates the new types in the error-handling
  section. The existing `pkg/provider/doc.go` package comment gains a
  pointer to the typed shapes.

## Alternatives considered

- **Replace `APIError` with a typed hierarchy entirely.** Rejected as a
  breaking change with no offsetting benefit — `*APIError` is already a
  perfectly usable normalized struct; the gap is the ergonomic surface,
  not the data model.
- **Per-provider typed errors (`*anthropic.RateLimitError` etc.).**
  Rejected because it forces callers to type-switch by provider, which
  is exactly what the abstraction exists to prevent.
- **Method-based classification on `APIError`
  (`apiErr.IsRateLimit() bool`).** Rejected because it does not compose
  with `errors.As`, which is the idiomatic Go pattern callers already
  know.
