# examples/tools-loop

End-to-end demo of galdor's tool-calling loop using a deterministic
in-process provider. No network, no API key.

## Run

```bash
go run ./examples/tools-loop
```

Expected output:

```
Turn 1 → 2 tool calls, stop=tool_use, tokens 30/12
  call call_add -> add{"a":2,"b":3}
  call call_weather -> weather{"city":"Quito"}
  result call_add ✓ {"sum":5}
  result call_weather ✓ {"city":"Quito","temp_c":21,"brief":"sunny"}

Turn 2 → final reply: Tool results: {"sum":5} | {"city":"Quito","temp_c":21,"brief":"sunny"}
```

## What it shows

- Defining typed tools with `tool.MustNewTool` — the JSON Schema for
  each input struct is generated automatically by
  `internal/jsonschema` from the struct's `json:"..."` and
  `jsonschema:"..."` tags.
- Registering tools in a `tool.Registry`, then handing the registry's
  `ToolDefs()` to a `provider.Request`. The same code works against
  Anthropic, OpenAI, Google or Bedrock — only the `Provider`
  implementation changes.
- Dispatching `schema.ToolCall`s concurrently with `tool.ExecuteCalls`.
  Results preserve input order regardless of which goroutine finished
  first.
- Feeding `tool.AsToolResultMessages(results)` back into the next
  `provider.Request.Messages` to complete the assistant ↔ tool round
  trip.

## Swap in a real provider

The example uses a `scriptedProvider` so the loop is deterministic and
free. To run it against a real LLM, replace the provider construction:

```go
import "github.com/YasserCR/galdor/providers/anthropic"

p, _ := anthropic.New(anthropic.Config{APIKey: os.Getenv("ANTHROPIC_API_KEY")})
// Then pass `p` wherever `&scriptedProvider{}` is used.
```

Everything else — the tools, the registry, the executor, the
result-folding — is provider-agnostic by design.
