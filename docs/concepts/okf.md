# OKF

`memory/okf` is a `memory.Store` over **Open Knowledge Format** bundles: knowledge kept as Markdown files with YAML frontmatter, organized in a git-versioned directory tree. One `.md` file is one concept, links between concepts form a directed graph, and the only frontmatter field the format requires is `type`. It is a lexical (BM25) retrieval backend with a knowledge layer on top — a link graph, progressive-disclosure indexes, change logs, citations, and read/write/validate tooling — so an agent can both search a bundle and navigate it.

It lives in its own module (`github.com/YasserCR/galdor/memory/okf`), so it only enters your dependency tree if you use it.

## The one-liner

`Open` reads a bundle from a directory and hands back a ready store:

```go
store, err := okf.Open(ctx, "./knowledge")
if err != nil {
    return err
}
defer store.Close()

hits, _ := store.Retrieve(ctx, memory.Query{Text: "how is MRR defined?", K: 5})
for _, h := range hits {
    fmt.Println(h.Chunk.Metadata[okf.MetaConceptID], h.Score)
}
```

`Open` is `Load` + `ChunkConcepts` + `NewStore`. Reach for the pieces when you want the documents too — for instance to build a second, vector-backed source and fuse the two:

```go
docs, warnings, err := okf.Load("./knowledge")   // []memory.Document, []string, error
// ... embed docs into a vector store ...
store, _ := okf.NewStore(ctx, okf.ChunkConcepts(docs))
```

`Load` never fails on content: a missing `type`, a broken link or an unparseable reserved file comes back in `warnings`, not `err`. The format is permissive by design, so the loader is too — see [Validate](#validating) for the strict counterpart.

## Retrieval

`Store` is an ordinary `memory.Store`, so it drops into anything that takes one.

```go
type Store struct{ /* ... */ }

func NewStore(ctx context.Context, chunks []memory.Chunk) (*Store, error)
func Open(ctx context.Context, root string) (*Store, error)

func (s *Store) Retrieve(ctx context.Context, q memory.Query) ([]memory.Result, error)
func (s *Store) Add(ctx context.Context, chunks []memory.Chunk) error
func (s *Store) Delete(ctx context.Context, documentID string) error
func (s *Store) Close() error
func (s *Store) Len(ctx context.Context) (int, error)
```

Chunking is concept-first: `ChunkConcepts` emits one chunk per concept, splitting on top-level headings only when the body is large, and prefixes each chunk's indexed text with the concept's title, description and tags — so a query matches on what a concept *is*, not only on where a phrase happens to fall.

The BM25 index uses galdor's code-aware tokenizer, which keeps a compound identifier **and** emits its parts: `customer_id` is indexed as `customer_id`, `customer` and `id`. A query for the whole identifier, or for either part, matches — and the concept carrying the literal identifier outranks one that merely mentions the parts.

### Query filters

Beyond `Query.Text` and `Query.K`, the OKF store reads three reserved `Query.Filter` keys that the plain key/value contract can't express, and applies them as post-filters:

| Key | Constant | Effect |
|---|---|---|
| `tag` | `okf.FilterTag` | Keep concepts carrying this tag (OKF tags are a list, not a scalar) |
| `since` | `okf.FilterSince` | Keep concepts whose `timestamp` is `>=` this ISO-8601 value |
| `until` | `okf.FilterUntil` | Keep concepts whose `timestamp` is `<=` this ISO-8601 value |

```go
hits, _ := store.Retrieve(ctx, memory.Query{
    Text:   "revenue",
    Filter: map[string]string{okf.FilterTag: "finance", okf.FilterSince: "2026-01-01"},
})
```

Every other `Filter` entry — including `okf.MetaSection` to keep only chunks from a `schema`, `examples` or `citations` body section — is a real metadata key pushed down to the index as an exact match. A concept with no `timestamp` is dropped whenever `since` or `until` is set.

### Hybrid retrieval

OKF is lexical only. When exact terms and meaning both matter, fuse it with a dense source under the core's `memory.HybridRetriever` — Reciprocal Rank Fusion needs no score calibration between the two:

```go
hybrid := &memory.HybridRetriever{
    Sources: []memory.Searcher{
        okfStore,                                        // lexical (BM25)
        &memory.Retriever{Store: vecStore, Embedder: emb}, // dense
    },
    K: 8,
}
```

The runnable end-to-end version is [`examples/okf-rag`](../../examples/okf-rag/).

## Tools for agents

Two ReAct-callable tools sit on the bundle — search versus browse.

```go
search, _ := okf.NewSearchTool(store)   // "okf_search"
browse, _ := okf.NewBrowseTool(bundle)  // "okf_browse"
registry := tool.NewRegistry(search, browse)
```

`okf_search` finds concepts by text, narrowing by `type`, `tag`, `since`/`until` or `section`:

```go
type SearchInput struct {
    Query   string `json:"query"`             // required
    Type    string `json:"type,omitempty"`
    Tag     string `json:"tag,omitempty"`
    Since   string `json:"since,omitempty"`
    Until   string `json:"until,omitempty"`
    Section string `json:"section,omitempty"` // "schema" | "examples" | "citations"
    K       int    `json:"k,omitempty"`
}
```

Pass an `*okf.Store` so the tag filter is honored; any `memory.Store` also works, minus the OKF-specific filters. Each `SearchHit` carries the concept id, title, type, score and a display snippet.

`okf_browse` is the complement: given a directory (empty for the root) it returns the directory's index, its subdirectories, and the concepts directly inside it — so an agent can explore what a bundle holds before searching it.

## The link graph

OKF cross-links are directed edges. A `Bundle` turns them into a navigable graph, derived lazily on first use:

```go
bundle, _ := okf.LoadBundle("./knowledge")

bundle.Outlinks("metrics/mrr")        // concepts mrr links to
bundle.Inlinks("metrics/mrr")         // concepts that link to mrr
bundle.Neighborhood("metrics/mrr", 2) // everything reachable within 2 forward hops
```

`GraphExpander` turns that graph into retrieval-time expansion: it wraps a `Store` and, after each `Retrieve`, appends the graph neighbors of the top hits — so "give me this concept and the ones it links to" needs no manual walking.

```go
expander := &okf.GraphExpander{
    Inner:  store,
    Bundle: bundle,
    Depth:  1,   // hops to expand; default 1
    Decay:  0.5, // score multiplier per hop; default 0.5
}
hits, _ := expander.Retrieve(ctx, memory.Query{Text: "MRR"})
```

`GraphExpander` is itself a `memory.Store` (`Add`/`Delete`/`Close` delegate to `Inner`), so it composes. It leaves the wrapped ranking untouched: base hits come first in their original order, then the neighbors, each scored `seed_score × Decay^hops` and never allowed to outrank or duplicate a real hit. Set `IncludeInlinks: true` to expand along reverse edges too.

## Navigating the bundle

A concept id like `references/metrics/mrr` encodes a place in a tree. `Bundle` exposes it:

```go
bundle.Children("references/metrics") // concept ids directly in that dir, sorted
bundle.Dirs("references")             // immediate subdirectories, sorted
bundle.Parent("references/metrics/mrr")
bundle.Concept("metrics/mrr")         // (memory.Document, bool)
```

### Progressive disclosure

The OKF spec lets each directory carry an `index.md` — a short "what's here" an agent reads before opening any concept. `IndexFor` returns a directory's real index, or synthesizes one on the fly when none exists:

```go
idx := bundle.IndexFor("references/metrics")
if idx.Synthesized {
    // no index.md on disk; this was generated from the directory's children
}
fmt.Println(idx.Title, idx.Body)
```

`Index` carries `Dir`, `Title`, `Description`, `Version` (only the root index declares `okf_version`), `Body`, and `Synthesized`. `Bundle.Indexes` holds every parsed on-disk index; `Bundle.Logs` holds each directory's parsed `log.md` change history as date-grouped `LogEntry` rows.

## Citations

A concept sourcing external claims lists them under a `# Citations` heading, numbered `[1] [text](url)`. The parser gives you the structured view:

```go
for _, c := range bundle.Citations("metrics/mrr") {
    fmt.Printf("[%d] %s -> %s\n", c.Number, c.Title, c.Target)
}
```

`ParseCitations(body string) []Citation` does the same against a raw concept body.

## Writing

The package writes OKF as well as reads it, so a producer can generate a bundle programmatically. `Marshal` renders one concept back to Markdown + frontmatter; `WriteBundle` writes a whole `Bundle` — concepts plus each directory's `index.md` and `log.md` — to disk:

```go
data := okf.Marshal(doc)          // []byte: frontmatter + body
err := okf.WriteBundle("./out", bundle)
```

Round-tripping is faithful for the standard fields (`type`, `title`, `description`, `tags`, `resource`, `timestamp`) and for producer-defined extension keys, which are carried under the `fm.` metadata prefix and written back — so loading and re-marshaling a bundle preserves unknown fields rather than dropping them.

## Validating

The loader never rejects a bundle; `Validate` is the strict check a producer or CI runs before publishing one:

```go
problems := bundle.Validate()
for _, p := range problems {
    fmt.Println(p) // "error metrics/mrr: missing required field: type"
}
if okf.HasErrors(problems) {
    os.Exit(1) // gate the publish
}
```

Each `Problem` has a `Where` (concept id or reserved path), a `Severity` and a `Message`. `SeverityError` marks a hard requirement — a concept with no frontmatter block or no `type`, frontmatter in a non-root `index.md`, a non-ISO date heading in a `log.md`. `SeverityWarning` marks a recommendation — a missing `description` or `timestamp`, a malformed timestamp, a broken cross-link, or a missing/unsupported `okf_version`. `HasErrors` is the gate: warnings inform, errors block.

## Metadata keys

Frontmatter and derived fields are surfaced on each `memory.Document.Metadata` under exported constants, so you match on a symbol rather than a string literal:

```go
okf.MetaType       // "type"        (the only required field)
okf.MetaTitle      // "title"
okf.MetaDesc       // "description"
okf.MetaTags       // "tags"        (comma-joined)
okf.MetaResource   // "resource"
okf.MetaTimestamp  // "timestamp"   (ISO-8601)
okf.MetaConceptID  // "concept_id"  (the file's path within the bundle)
okf.MetaOutlinks   // "outlinks"    (comma-joined concept ids)
okf.MetaSection    // "section"     (query filter: which body section a chunk came from)
okf.MetaExtraPrefix // "fm."        (prefix for producer-defined frontmatter keys)
```

## Gotchas

- **`Load` reads concepts; `LoadBundle` reads everything else.** The graph, indexes, logs, citations and validation all hang off `*Bundle`, so a graph-aware or navigable setup needs `LoadBundle`, not `Open`/`Load`.
- **`Open` returns a `Store`, not a `Bundle`.** For `GraphExpander` or the browse tool, load the bundle separately and build the store from its concepts — they must describe the same bundle.
- **`GraphExpander.Depth`/`Decay` of 0 mean the default** (1 hop, 0.5 decay), not "no expansion". A `GraphExpander` with a nil `Bundle` simply passes retrieval through.
- **Warnings are not errors.** A bundle with broken links or a missing `okf_version` loads and retrieves fine. Run `Validate` when you need conformance, and check `bundle.Warnings` after loading if you want to surface load-time issues.
- **The tag filter needs an `*okf.Store`.** Handed a bare `memory.Store`, `NewSearchTool` still works but `tag`/`since`/`until` are silently inert, since only the OKF store applies them.
- **Reserved files never retrieve.** `index.md` and `log.md` are navigation, not concepts; they are excluded from the search index.

## See also

- [memory](memory.md) — the `Store`, `Retriever` and `HybridRetriever` interfaces OKF satisfies.
- [tool](tool.md) — the `Registry` the `okf_search` and `okf_browse` tools plug into.
- [RAG](../patterns/rag.md) — where OKF fits among the retrieval options.
- [`examples/okf-rag`](../../examples/okf-rag/) — BM25 + hybrid retrieval over an OKF bundle, end to end.
