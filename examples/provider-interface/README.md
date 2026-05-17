# examples/provider-interface

A self-contained demo of galdor's `Provider` abstraction (`pkg/provider`)
and shared types (`pkg/schema`).

The example defines `stubProvider`, a tiny in-process implementation that
uppercases the last user message. No network calls. It exercises both
`Generate` (assembled via `CollectStream`) and `Stream` (consumed one
event at a time).

## Run

```bash
go run ./examples/provider-interface
```

Expected output:

```
provider:     stub
capabilities: {Streaming:true ToolCalling:false StructuredOutput:false PromptCaching:false VisionInput:false MaxContextTokens:8192}

Generate -> "HELLO GALDOR" (stop=end_turn, tokens=12/12)
Stream   -> HELLO GALDOR
```

## What to look at

- How a `Provider` satisfies the interface with `Name`, `Capabilities`,
  `Generate` and `Stream`.
- How a stream-only adapter can implement `Generate` by calling its own
  `Stream` and passing the result to `provider.CollectStream`.
- How `schema.SystemMessage` / `schema.UserMessage` keep request building
  free of provider specifics.

Real adapters (Anthropic, OpenAI, ...) follow the same shape but live in
independent Go modules under `providers/<name>/`.
