# spellbook

`pkg/spellbook` is the planned home for galdor's prompt registry — versioned prompt templates with a diff-friendly storage format, retrievable by name and version from agents and the CLI. "Spellbook" is one of the few themed package names in galdor; it teaches the concept better than the technical alternative ("prompt-registry") would, and the package boundary is small enough that the theming doesn't obscure anything.

## Status

The package is a **stub** at the current revision. Only the `doc.go` placeholder ships — no exported types, no constructors, no tests beyond the package compiling. The shape below is the planned surface, recorded here so callers can track when it lands. Until the implementation lands, build your own prompt loader against `pkg/schema.Message` and swap to `spellbook` later — the upgrade path is intended to be a search-and-replace.

```go
// Planned surface — not yet implemented.
package spellbook

type Spell struct {
    Name     string
    Version  string
    Template string
    Metadata map[string]string
}

type Book interface {
    Get(name, version string) (Spell, error)
    Latest(name string) (Spell, error)
    List() ([]Spell, error)
}
```

## Why the theming

The package-name rule in galdor (see [`ADR-001`](../adr/ADR-001-foundational-decisions.md)) is: prefer plain technical names (`pkg/tool`, `pkg/graph`, `pkg/memory`), use a themed name only when the theme **adds** semantics over the plain alternative. `council` qualifies because "Supervisor + Swarm + Hierarchy" is a compound concept; `scry` qualifies because the verb shape ("look into the trace store") is the API surface. `spellbook` qualifies because a prompt registry stores incantations — *galdors* in the project's namesake sense — that are named, versioned, and chanted at the model. The technical name "prompt-template-registry" is longer and no clearer.

The constructors when they land will be unthemed and Go-idiomatic — `spellbook.New`, `spellbook.Open`, `Book.Get` — so callers writing application code don't have to think about the theming when navigating the API.

## What to do until it lands

For now, keep prompts as Go constants or load them from a file you own:

```go
const summarizePrompt = `Summarize the following text in 3 bullet points:

{{.Input}}`

func summarize(ctx context.Context, p provider.Provider, model, input string) (string, error) {
    tmpl, _ := template.New("").Parse(summarizePrompt)
    var buf bytes.Buffer
    _ = tmpl.Execute(&buf, map[string]string{"Input": input})
    resp, err := p.Generate(ctx, provider.Request{
        Model: model,
        Messages: []schema.Message{
            schema.SystemMessage("You are a concise summarizer."),
            schema.UserMessage(buf.String()),
        },
    })
    if err != nil { return "", err }
    return resp.Message.Text(), nil
}
```

Version the prompt the same way you version code (commit hash, package path). When `pkg/spellbook` ships, swap the constant for a `Book.Get("summarize", "v1")` call — the rest of the chain (template rendering, message construction, provider call) stays the same.

## Gotchas

- Don't import `pkg/spellbook` expecting symbols today — the package compiles but exports nothing.
- The planned storage format is "diff-friendly" — likely YAML or a flat-file layout rather than a binary blob — so prompts can be reviewed in code-review the same way Go source is.
- Versioning is by **string**, not semver. A spell version is whatever label the operator picks (`"2024-05-01"`, `"v3"`, a git SHA). The registry treats them opaquely.

## See also

- [provider](provider.md), [schema](schema.md) — what a rendered spell ultimately becomes.
- [`ROADMAP.md`](../../ROADMAP.md) — where the implementation phase is tracked.
- [`ADR-001`](../adr/ADR-001-foundational-decisions.md) — the package-naming rule that justifies the themed name.
