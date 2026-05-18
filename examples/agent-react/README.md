# examples/agent-react

End-to-end ReAct loop in `pkg/agent`, using a deterministic
scripted provider so the example runs offline. Combines a custom
`weather` tool with the built-in `math` tool from
`pkg/tool/builtins` and shows how `agent.NewReAct` glues
`pkg/provider` + `pkg/tool` + `pkg/graph` into a single Runnable.

## Run

```bash
go run ./examples/agent-react
```

Expected output:

```
iterations: 3
answer:     Quito is sunny at 21°C (about 37.8 °F).

conversation:
  0  system: You may use tools when helpful.
  1  user: What's the weather in Quito and what is that in Fahrenheit?
  2  assistant: tool calls
        weather({"city":"Quito"})
  3  tool:      {"temp_c":21,"brief":"sunny in Quito"}
  4  assistant: tool calls
        math({"op":"mul","a":21,"b":1.8})
  5  tool:      {"result":37.800000000000004}
  6  assistant: Quito is sunny at 21°C (about 37.8 °F).
```

## What it shows

- **`agent.NewReAct(cfg)` returns a `graph.Runnable[State]`.** All
  graph primitives apply — Invoke, Stream, InvokeWith for
  checkpointing, Resume after an interrupt.
- **Tools come from a `tool.Registry`.** Mix your own tools
  (`weatherTool`) with the built-ins (`builtins.MustNewMathTool()`).
- **The loop terminates when the model returns a message with no
  tool calls.** `MaxIterations` (default 10) caps the cycle so a
  buggy provider can't spin forever.
- **`State.FinalText`** is populated on terminal turns;
  `State.Messages` carries the full transcript.

## Run against a real provider

Replace the scripted provider with any of the real adapters:

```go
import "github.com/YasserCR/galdor/providers/anthropic"

p, _ := anthropic.New(anthropic.Config{APIKey: os.Getenv("ANTHROPIC_API_KEY")})
cfg := agent.Config{
    Provider: p,
    Tools:    reg,
    Model:    "claude-haiku-4-5",
}
final, err := agent.Run(ctx, cfg, "What's the weather in Quito?",
    "Use the tools to answer the user.")
```

The OpenAI, Google and Bedrock adapters work the same way — only
the `Provider` value changes.
