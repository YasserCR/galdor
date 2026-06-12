# spellbook

`pkg/spellbook` is galdor's prompt registry — versioned prompt templates with a diff-friendly storage format, retrievable by name and version from agents and the CLI. "Spellbook" is one of the few themed package names: a prompt registry stores incantations — *galdors* in the project's namesake sense — that are named, versioned, and chanted at the model. The constructors are unthemed and Go-idiomatic (`New`, `Open`, `Book.Get`) so application code doesn't have to think about the theming.

The package is **stdlib-only** by design — it lives in the core module, which stays dependency-light.

## Core types

```go
type Spell struct {
    Name     string
    Version  string
    Template string
    Metadata map[string]string
}

// Render executes the template against data (a map or struct) using Go
// text/template syntax, e.g. {{.Input}}. A missing key is an error, not
// "<no value>".
func (s Spell) Render(data any) (string, error)

type Book interface {
    Get(name, version string) (Spell, error)
    Latest(name string) (Spell, error)   // lexically-greatest version
    List() ([]Spell, error)
}
```

## Two backends

```go
// In-memory — handy for tests and programmatic prompts.
book := spellbook.New(
    spellbook.Spell{Name: "summarize", Version: "v1", Template: "Summarize:\n{{.Input}}"},
)

// File-backed — the directory IS the registry.
book, err := spellbook.Open("./spells")
```

The file layout is one raw-text file per version:

```
spells/
  summarize/
    v1.md          # the file content is the raw template
    2024-06-01.md
  translate/
    v1.md
```

Because each version is a plain `.md` file, prompts diff and review in code review exactly like Go source. Versions are **opaque string labels** — `"v3"`, `"2024-05-01"`, a git SHA — and `Latest` returns the lexically-greatest one (use zero-padded or date-prefixed labels if you need a particular order). The file backend leaves `Metadata` empty; the in-memory backend carries whatever you set.

## From the CLI (`galdor spellbook`)

```bash
galdor spellbook list                         # name + version of every spell
galdor spellbook show  summarize              # the latest version's template
galdor spellbook show  summarize v1           # a specific version
galdor spellbook diff  summarize v1 v2        # unified +/- line diff
galdor spellbook render summarize --data '{"Input":"..."}'
```

`--dir` selects the store (default `$GALDOR_SPELLBOOK`, then `./spells`). Flags work wherever you put them.

## Using a spell as an agent's system prompt

An agent block (`cast`, `council`, `trial`) can pull its system prompt from the spellbook instead of inlining it — so a fleet of agents shares one reviewed, versioned prompt:

```yaml
version: 1
agent:
  provider: anthropic
  model: claude-haiku-4-5
  system_spell: {name: support_persona, version: v3}   # ./spells/support_persona/v3.md
```

`system_spell` and an inline `system:` are mutually exclusive; omit `version` to use the latest. The spell's template is used verbatim as the system prompt (no per-run rendering — inline a `system:` string if you need interpolation).

## Gotchas

- **Versions sort lexically, not by semver.** `"v10" < "v2"` as strings; zero-pad (`v02`, `v10`) or date-prefix if ordering matters.
- **`Render` is strict.** A `{{.Field}}` with no matching key errors (`missingkey=error`) rather than silently emitting `<no value>`.
- **The file backend rejects path traversal.** Names and versions must be single path segments — no `/`, `\`, or `..`.
- **Metadata is in-memory only** for now; the file backend stores just the template text.

## See also

- [agent](agent.md), [eval](eval.md), [council](council.md) — the verbs whose agent blocks can reference a spell via `system_spell`.
- [provider](provider.md), [schema](schema.md) — what a rendered spell ultimately becomes.
