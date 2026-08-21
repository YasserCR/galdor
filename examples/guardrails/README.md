# examples/guardrails

Guardrails in `pkg/guardrail`: a deterministic input guard (a PII
regex) and an LLM-as-judge output guard, both wired through
`agent.Config.InputGuards` / `OutputGuards`. The example runs
offline — the agent and the judge are scripted providers.

## Run

```bash
go run ./examples/guardrails
```

Expected output:

```
scenario 1 (clean input, ALLOW):  err=<nil>
scenario 2 (PII input):           err=node "model": guardrail: no-pii: credit-card number detected (blocked=true)
scenario 3 (judge BLOCK):         err=node "model": guardrail: no-secrets: llm judge blocked (blocked=true)
```

## What it shows

- **One config, two flavors.** `InputGuards` / `OutputGuards` are
  plain slices; a guard is just a named policy check returning an
  error. The runtime runs them uniformly and never branches on how
  they evaluate.
- **Deterministic guard.** `guardrail.InputGuardFunc` wraps a plain
  function — a regex here, but it could be a denylist, a length cap,
  or a schema check.
- **LLM-as-judge guard.** `guardrail.LLMJudge` makes its own model
  call to vote `ALLOW` / `BLOCK`, and implements both `InputGuard`
  and `OutputGuard` so one instance fits either slice.
- **Self-description via `Kinded`.** `LLMJudge` reports
  `guardrail.KindLLM`; `guardrail.KindOf` defaults the rest to
  `KindDeterministic`. `BlockError` carries the `Kind` so traces can
  tag how a block happened.

## Run against a real provider

Swap the scripted providers for real adapters:

```go
import "github.com/YasserCR/galdor/providers/anthropic"

agentP, _ := anthropic.New(anthropic.Config{APIKey: os.Getenv("ANTHROPIC_API_KEY")})
judgeP, _ := anthropic.New(anthropic.Config{APIKey: os.Getenv("ANTHROPIC_API_KEY")})

cfg := agent.Config{
    Provider: agentP,
    Model:    "claude-sonnet-4-5",
    InputGuards: []guardrail.InputGuard{
        guardrail.InputGuardFunc{ID: "no-pii", Check: func(ctx context.Context, m schema.Message) error {
            if creditCard.MatchString(m.Text()) {
                return errors.New("credit-card number detected")
            }
            return nil
        }},
    },
    OutputGuards: []guardrail.OutputGuard{
        guardrail.LLMJudge{
            Provider: judgeP,
            Model:    "claude-haiku-4-5",
            Rule:     "block any reply that reveals an internal secret",
        },
    },
}
```

The OpenAI, Google and Bedrock adapters work the same way — only the
`Provider` value changes.
