# Providerset

`providerset` is the runtime router over galdor's LLM adapters. Each adapter (Anthropic, OpenAI, Google, Bedrock) lives in its own Go module under `providers/<name>/`. Picking between them at startup normally means a switch over an env var; `providerset` owns that switch so your app doesn't.

It is the closest thing galdor has to LiteLLM's "set a name, get a client" surface. Unlike LiteLLM, the routing is in-process, the dependency tree stays small (you still only pay for the providers you build), and the returned value is a plain `provider.Provider` — the rest of galdor doesn't know it went through a router.

## The interface

```go
type Config struct {
    Provider   string        // "anthropic" | "openai" | "google" | "bedrock"
                             //   or an OpenAI-compatible alias:
                             //   "groq" | "together" | "mistral" | "minimax"
                             //   | "deepseek" | "vllm" | "ollama"
    APIKey     string
    BaseURL    string        // override; honoured by every provider except bedrock
    HTTPClient *http.Client  // optional; falls back to the adapter default
}

func New(cfg Config) (provider.Provider, error)
func FromEnv() (provider.Provider, error)
```

`FromEnv` reads four variables: `LLM_PROVIDER` (required), `LLM_API_KEY`, `LLM_BASE_URL`, and `LLM_HTTP_TIMEOUT`. The timeout accepts any `time.ParseDuration` value, or a bare integer interpreted as seconds.

## Things you do with it

### 1. Construct from a Config

```go
p, err := providerset.New(providerset.Config{
    Provider: "anthropic",
    APIKey:   os.Getenv("ANTHROPIC_API_KEY"),
})
if err != nil {
    log.Fatal(err)
}
```

`p` is a `provider.Provider`; pass it to `agent.Run`, `agent.NewReAct`, `council.NewSupervisor`, or whatever consumes one.

### 2. Construct from the environment

```go
p, err := providerset.FromEnv()
if err != nil {
    log.Fatal(err)
}
```

With `LLM_PROVIDER=openai` and `LLM_API_KEY=sk-...`, you get the OpenAI adapter. Swap to `LLM_PROVIDER=anthropic` and `LLM_API_KEY=sk-ant-...` and the rest of the app is unchanged.

### 3. Talk to an OpenAI-compatible endpoint

Aliases resolve to `providers/openai` with a preset `BaseURL`:

| Alias      | BaseURL                                |
| ---------- | -------------------------------------- |
| `groq`     | `https://api.groq.com/openai/v1`       |
| `together` | `https://api.together.xyz/v1`          |
| `mistral`  | `https://api.mistral.ai/v1`            |
| `minimax`  | `https://api.minimaxi.chat/v1`         |
| `deepseek` | `https://api.deepseek.com/v1`          |
| `vllm`     | `http://localhost:8000/v1`             |
| `ollama`   | `http://localhost:11434/v1`            |

```go
p, err := providerset.New(providerset.Config{
    Provider: "groq",
    APIKey:   os.Getenv("GROQ_API_KEY"),
})
```

Set `BaseURL` explicitly to point at a self-hosted gateway:

```go
p, err := providerset.New(providerset.Config{
    Provider: "openai",
    APIKey:   os.Getenv("PROXY_KEY"),
    BaseURL:  "https://llm-proxy.internal/v1",
})
```

`vllm` and `ollama` do not require an API key. If you leave `APIKey` blank for those aliases, providerset substitutes a placeholder so the OpenAI adapter constructs cleanly.

## Quirks per provider

- **anthropic**, **openai**, **google** — `APIKey` is required; the adapter rejects an empty value. `BaseURL` and `HTTPClient` pass through.
- **bedrock** — `APIKey` and `BaseURL` are ignored. Credentials come from the AWS default chain (env vars, `~/.aws/credentials`, IAM role, SSO, EC2/ECS metadata) via `aws-sdk-go-v2/config.LoadDefaultConfig`. Region must be set on that chain (`AWS_REGION` or the active profile); the adapter errors otherwise.
- **OpenAI-compatible aliases** — `BaseURL` on the Config wins over the table. `vllm` and `ollama` accept a blank `APIKey`.

## Demo

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/YasserCR/galdor/pkg/provider"
    "github.com/YasserCR/galdor/providerset"
    "github.com/YasserCR/galdor/pkg/schema"
)

func main() {
    p, err := providerset.FromEnv()
    if err != nil {
        log.Fatal(err)
    }

    resp, err := p.Generate(context.Background(), provider.Request{
        Model: "gpt-4o-mini",
        Messages: []schema.Message{
            schema.UserMessage("Say hi in one word."),
        },
    })
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(resp.Message.Text())
}
```

Run it against any backend by exporting the right env vars:

```sh
LLM_PROVIDER=openai LLM_API_KEY=sk-... go run .
LLM_PROVIDER=anthropic LLM_API_KEY=sk-ant-... go run .
LLM_PROVIDER=groq LLM_API_KEY=gsk-... go run .
LLM_PROVIDER=ollama go run .
```

## See also

- [Provider](provider.md) — the `Provider` interface every adapter implements.
- Each adapter README: [anthropic](../../providers/anthropic/README.md), [openai](../../providers/openai/README.md), [google](../../providers/google/README.md), [bedrock](../../providers/bedrock/README.md).
