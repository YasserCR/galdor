# examples/structured-output

Constrain a model's reply to the shape of a Go struct and get it back
decoded — one call, no manual JSON wrangling.

## Run

```bash
go run ./examples/structured-output
```

Expected output:

```
Buttermilk Pancakes — 20 min
  - flour
  - buttermilk
  - egg
  - butter
  - baking powder

schema sent to the model:
{"type":"object","properties":{...},"required":["title","minutes","ingredients"],"additionalProperties":false}
```

## What it shows

- **`provider.GenerateStructured[Recipe]`** derives a JSON Schema from the
  `Recipe` struct (using the same `json` / `jsonschema` tags tools use),
  asks the provider to return JSON matching it, and decodes the reply back
  into a `Recipe` — tolerating code fences or surrounding prose.
- **`provider.JSONSchemaFor[Recipe]()`** returns the schema bytes on their
  own, if you'd rather set `Request.ResponseFormat` yourself or inspect
  what the model saw.

## Run against a real provider

The scripted provider drops out unchanged — swap it for a real adapter
that reports `StructuredOutput: true` (OpenAI, Google, or Anthropic):

```go
import anthropic "github.com/YasserCR/galdor/providers/anthropic"

p, _ := anthropic.New(anthropic.Config{APIKey: os.Getenv("ANTHROPIC_API_KEY")})
recipe, err := provider.GenerateStructured[Recipe](ctx, p, provider.Request{
    Model:    "claude-haiku-4-5",
    Messages: []schema.Message{schema.UserMessage("a quick pancake recipe")},
})
```

Make optional fields `omitempty` or pointers — object schemas are emitted
closed (`additionalProperties: false`) with every non-`omitempty` field
required.
