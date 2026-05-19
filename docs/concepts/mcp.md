# mcp

`pkg/mcp` is galdor's implementation of Anthropic's Model Context Protocol — the spec for connecting LLM applications to external tools and data sources over JSON-RPC 2.0. Two sides are supported in one package: a **Client** that consumes a remote MCP server's tools (and exposes them as a galdor `tool.Registry`), and a **Server** that wraps any galdor `tool.Registry` and serves it to MCP clients like Claude Desktop. Stdio transport ships today; HTTP+SSE is planned.

What's in scope: JSON-RPC framing, the `initialize` handshake plus `notifications/initialized`, `tools/list`, and `tools/call`. Out of scope for now: resources, prompts, sampling.

## Core types

```go
type Transport interface {
    Send(ctx context.Context, msg any) error
    Recv(ctx context.Context) ([]byte, error)
    Close() error
}

func NewStdioTransport(r io.Reader, w io.Writer) Transport

type Client struct { /* unexported */ }
func NewClient(t Transport, opts ...ClientOption) *Client
func WithClientInfo(info ClientInfo) ClientOption

type ServerInfo struct {
    Name    string
    Version string
}

type Server struct {
    Strict bool // reject requests received before initialize completes
}
func NewServer(reg *tool.Registry, info ServerInfo) *Server
```

`ServerInfo` defaults `Name` to `"galdor-mcp"` and `Version` to `"0"` when fields are empty.

## Consuming an MCP server (client)

Connect, initialize, list, call:

```go
import (
    "github.com/YasserCR/galdor/pkg/mcp"
    "github.com/YasserCR/galdor/pkg/agent"
)

cmd := exec.Command("./remote-mcp-server")
stdin, _  := cmd.StdinPipe()
stdout, _ := cmd.StdoutPipe()
_ = cmd.Start()

c := mcp.NewClient(mcp.NewStdioTransport(stdout, stdin))
defer c.Close()

_ = c.Initialize(ctx)
defs, _ := c.ListTools(ctx)
out, err := c.CallTool(ctx, "weather", json.RawMessage(`{"city":"Quito"}`))
```

`CallTool` returns the concatenated text content of the reply. When the server marks the result as `isError`, the text is returned as the error message so callers see what went wrong without parsing.

## Plug a remote server straight into an agent

`AsRegistry` adapts every advertised tool into a galdor `tool.AnyTool` and bundles them in a `*tool.Registry` — ready for `agent.Config.Tools`:

```go
c := mcp.NewClient(mcp.NewStdioTransport(stdout, stdin))
_ = c.Initialize(ctx)
reg, _ := c.AsRegistry(ctx)

answer, _ := agent.Run(ctx, agent.Config{
    Provider: p, Model: "claude-haiku-4-5", Tools: reg,
}, "What's the weather in Quito?")
```

Each adapter tool's `ExecuteJSON` proxies to `CallTool`; the agent's ReAct loop never knows the tools live in another process. The MCP tool's reply text is wrapped in `{"text": "..."}` so downstream consumers always see valid JSON.

## Exposing a galdor registry as an MCP server

```go
import (
    "github.com/YasserCR/galdor/pkg/mcp"
    "github.com/YasserCR/galdor/pkg/tool"
    "github.com/YasserCR/galdor/pkg/tool/builtins"
)

now, _  := builtins.NewTimeTool()
math, _ := builtins.NewMathTool()
reg, _  := tool.NewRegistry(now, math, yourCustomTool)

srv := mcp.NewServer(reg, mcp.ServerInfo{Name: "my-tools", Version: "0.1"})
srv.Strict = true

_ = srv.Serve(ctx, mcp.NewStdioTransport(os.Stdin, os.Stdout))
```

Point Claude Desktop's `claude_desktop_config.json` at the compiled binary and restart — the tools appear in the picker. Tool execution happens inside `Serve`'s receive loop; the server spawns a goroutine per inbound request so a slow tool doesn't block other calls. Sends are mutex-synchronized inside the transport so frames stay aligned.

## Strict mode

`Server.Strict = true` rejects every request received before `initialize` completes with a JSON-RPC `InvalidRequest` error. Well-behaved clients always send `initialize` first, so flipping this on catches misbehaving peers immediately. Off by default to preserve interop with the long tail of MCP implementations.

## Stdio framing

`NewStdioTransport` reads and writes one JSON object per line. Trailing `\r` is stripped (Windows pipes). Empty lines are skipped for the benefit of servers that emit them for human readability. Concurrent `Send` calls are safe — a mutex serializes writes so frames stay intact. `Recv` blocks on the underlying reader; ctx cancellation is best-effort because `os.Stdin` doesn't honor deadlines.

## Gotchas

- Tool errors come back as a regular response with `isError: true` and the error text in `content`. JSON-RPC errors (`Error != nil`) are reserved for transport-level failures (parse error, method not found, invalid params).
- The client's dispatch loop ignores server-initiated notifications. If you need to react to them (cancellation, progress, etc.), wire your own goroutine pulling from the transport before constructing the `Client`.
- `Close()` is idempotent on both sides — call it from a deferred shutdown path without checking error semantics.
- The stdio transport's `Close()` calls `Close()` on `w` if it implements `io.Closer`. For paired stdin/stdout pipes that matters; for `os.Stdin` / `os.Stdout` it's a no-op.
- An empty `tool.Registry` is legal; `tools/list` just returns `[]`.
- The protocol version (`mcp.ProtocolVersion`) is negotiated leniently: the server echoes its own version; older clients see "downgrade me to your level" per the spec.

## See also

- [tool](tool.md) — the `Registry` both sides traffic in.
- [agent](agent.md) — `AsRegistry` plugs straight into `agent.Config`.
- [a2a](a2a.md) — peer-to-peer agents (different problem; both ride JSON-RPC 2.0).
- [`examples/integration-mcp-server`](../../examples/integration-mcp-server/) — full Claude Desktop wiring.
