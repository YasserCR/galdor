# mcp

`pkg/mcp` is galdor's implementation of Anthropic's Model Context Protocol — the spec for connecting LLM applications to external tools and data sources over JSON-RPC 2.0. Two sides are supported in one package: a **Client** that consumes a remote MCP server's tools (and exposes them as a galdor `tool.Registry`), and a **Server** that wraps any galdor `tool.Registry` and serves it to MCP clients like Claude Desktop. Three transports ship today: **stdio** (child-process), **SSE** (HTTP+SSE, the pre-2024-11-05 default), and **Streamable HTTP** (the post-2024-11-05 default).

What's in scope: JSON-RPC framing, the `initialize` handshake plus `notifications/initialized`, `tools/list`, and `tools/call`. Out of scope for now: resources, prompts, sampling.

## Core types

```go
type Transport interface {
    Send(ctx context.Context, msg any) error
    Recv(ctx context.Context) ([]byte, error)
    Close() error
}

func NewStdioTransport(r io.Reader, w io.Writer) Transport
func NewSSETransport(addr string) Transport
func NewStreamableHTTPTransport(addr string) Transport

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

## SSE transport (pre-2024-11-05)

`NewSSETransport(addr)` binds an HTTP server to `addr` and mounts two routes:

- `GET /sse` — the client opens a Server-Sent Events stream. The first event is `event: endpoint`, with the URL the client must POST requests to (e.g. `/messages?sessionId=xxx`). Subsequent events are `event: message` carrying JSON-RPC reply frames.
- `POST /messages?sessionId=...` — the client POSTs JSON-RPC requests. The server returns `202 Accepted` and pushes the matching response down the SSE stream.

```go
srv := mcp.NewServer(reg, mcp.ServerInfo{Name: "my-tools", Version: "0.1"})
_ = srv.Serve(ctx, mcp.NewSSETransport(":8080"))
```

`Close()` shuts the listener down with `http.Server.Close` (not `Shutdown`), because an SSE GET handler never returns on its own — the spec intends the stream to live as long as the server. Sessions are demuxed on the server side: at most one SSE stream is active at a time. If a second client connects the first session's stream is torn down (its stale `POST /messages?sessionId=...` returns 404). In normal MCP usage one client owns one server, so this is invisible.

This is the transport Cursor, Claude Desktop pre-2024-11, and the original `@modelcontextprotocol/sdk` still speak today. Keep it on for compatibility.

## Streamable HTTP transport (2024-11-05+)

`NewStreamableHTTPTransport(addr)` binds an HTTP server with a single endpoint:

- `POST /` — request body is one JSON-RPC frame. The response is the matching reply frame as `application/json`. Notifications (no `id` field) return `202 Accepted` with an empty body.
- `DELETE /` (optional) — terminate a session.

Session id rides on the `Mcp-Session-Id` HTTP header. The server assigns one on the response to `initialize`; the client echoes it back on every subsequent request. Requests with a wrong session id get `404 Not Found`.

```go
srv := mcp.NewServer(reg, mcp.ServerInfo{Name: "my-tools", Version: "0.1"})
_ = srv.Serve(ctx, mcp.NewStreamableHTTPTransport(":8080"))
```

Clients must send `Accept: application/json, text/event-stream` because future replies may stream multiple frames as SSE before closing (long-running tools, progress notifications). The current implementation only emits single JSON replies; the SSE-response path will land alongside notifications support.

Streamable HTTP is the post-2024-11-05 default and what the official TypeScript SDK now ships. Prefer it for new deployments; keep SSE on alongside for older clients.

## Choosing a transport

- **stdio** — your binary is spawned as a subprocess by the IDE. Single client per process, no networking. The default for Claude Desktop, Cursor, Zed, Antigravity.
- **SSE** — your server is a long-lived daemon and the client predates the 2024-11-05 spec revision. Browser-friendly because SSE is a standard reconnecting stream.
- **Streamable HTTP** — same daemon model, modern clients. Cheaper to operate (no long-lived GET stream when there are no server-initiated frames to push) and the spec's future direction.

Bind both HTTP transports if you need maximum compatibility — they're independent listeners and the same `*Server` can `Serve` either.

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
