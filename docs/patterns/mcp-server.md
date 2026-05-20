# MCP server: expose galdor tools to MCP clients

## When to use this pattern

You have a `tool.Registry` — internal-API wrappers, domain
operations, search shims — and you want Claude Desktop, Cursor,
Zed, or any other MCP-aware client to call them as if they were
the client's own tools. Wrap the registry as an MCP server over
stdio and the client picks them up automatically.

This is the dual of the MCP client (`pkg/mcp.Client`): instead
of consuming a remote MCP server's tools, you're exposing your
local ones.

## Minimal sketch

```go
package main

import (
    "context"
    "os"
    "os/signal"
    "syscall"

    "github.com/YasserCR/galdor/pkg/mcp"
    "github.com/YasserCR/galdor/pkg/tool"
    "github.com/YasserCR/galdor/pkg/tool/builtins"
)

func main() {
    now, _ := builtins.NewTimeTool()
    math, _ := builtins.NewMathTool()
    reg, _ := tool.NewRegistry(now, math, yourCustomTool)

    srv := mcp.NewServer(reg, mcp.ServerInfo{
        Name: "my-tools", Version: "0.1",
    })
    srv.Strict = true

    transport := mcp.NewStdioTransport(os.Stdin, os.Stdout)
    ctx, cancel := signal.NotifyContext(context.Background(),
        os.Interrupt, syscall.SIGTERM)
    defer cancel()

    _ = srv.Serve(ctx, transport)
}
```

Build it once:

```bash
go build -o ./mytools ./cmd/mytools
```

Then point Claude Desktop at the binary.

## Claude Desktop wiring

Edit
`~/Library/Application Support/Claude/claude_desktop_config.json`
(macOS) or the equivalent on your platform:

```json
{
  "mcpServers": {
    "my-tools": {
      "command": "/absolute/path/to/mytools"
    }
  }
}
```

Restart Claude Desktop. Your tools appear in the picker.

For IDE plugins (Cursor, Zed, etc.) the config file location
varies, but the entry shape is the same: a `command` (and
optionally `args`, `env`) that the client spawns and talks to
over stdio.

## Walkthrough

1. **Build the registry.** Any `tool.Registry` works — builtins,
   your own `tool.MustNewTool`s, a mix. Each tool's JSON schema
   is derived from its input struct via reflection.
2. **Construct the server.** `mcp.NewServer(reg, info)` returns
   a `*Server`. `ServerInfo` populates the `serverInfo` block of
   the MCP `initialize` handshake. Set `Strict = true` to reject
   pre-initialize traffic (catches misbehaving clients early).
3. **Pick a transport.** `mcp.NewStdioTransport(os.Stdin,
   os.Stdout)` is what every desktop-class client speaks. The
   transport reads newline-delimited JSON-RPC frames; writes are
   mutex-synchronized so concurrent replies stay frame-aligned.
4. **Handle signals.** `signal.NotifyContext` gives the server a
   chance to drain in-flight requests when the parent
   (Claude Desktop) terminates the child. Without it, an
   uncooperative shutdown orphans the goroutine.
5. **Serve.** `srv.Serve` blocks until the transport closes
   (peer disconnects) or `ctx` is cancelled. Requests are
   dispatched in goroutines so a slow tool doesn't block the
   receive loop.

## Smoke test locally

The same binary works without Claude Desktop if you can write
JSON-RPC by hand:

```bash
./mytools <<'EOF'
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"now","arguments":{}}}
EOF
```

Three replies come back on stdout. `tools/list` enumerates the
registry; `tools/call` invokes one tool and returns its result.

## Common variations

### Domain tools wrapping an internal API

The point of MCP is not to ship the builtins — it's to surface
your internal stuff. The pattern is identical to any galdor
tool:

```go
type lookupIn struct {
    Topic string `json:"topic" jsonschema:"the topic to look up"`
}
type lookupOut struct {
    Title string `json:"title"`
    Body  string `json:"body"`
}

lookup := tool.MustNewTool("lookup_doc",
    "Look up a doc from our internal KB",
    func(ctx context.Context, in lookupIn) (lookupOut, error) {
        return kbClient.Get(ctx, in.Topic)
    })
```

Add `lookup` to the registry and Claude Desktop can call it.

### Side-effecting tools

Tools can mutate the world (create tickets, open PRs, post
messages). The MCP protocol doesn't distinguish read from write;
the *user* approves each call in the client. Keep the tool's
behaviour predictable from its input — surprising side effects
are how MCP-shaped agents go wrong.

### Streaming output

The shipped stdio transport is request-response. For long-running
tools, return progress in the response body and run the work
behind a job-id pattern; or implement a custom `Transport` over
SSE / WebSocket if your client supports it.

## Exposing over HTTP

Instead of having every IDE spawn a fresh subprocess, run the
server once as a long-lived daemon and let clients connect over
HTTP. `pkg/mcp` ships two HTTP transports; both satisfy the same
`Transport` interface so `srv.Serve` doesn't need to change.

### SSE — works with every MCP client today

`mcp.NewSSETransport(addr)` mounts `GET /sse` (the event stream)
and `POST /messages` (request inbox). Cursor, Claude Desktop
pre-2024-11, and the original TypeScript SDK all speak it. Use
this for maximum compatibility:

```go
srv := mcp.NewServer(reg, mcp.ServerInfo{Name: "my-tools", Version: "0.1"})
srv.Strict = true

ctx, cancel := signal.NotifyContext(context.Background(),
    os.Interrupt, syscall.SIGTERM)
defer cancel()

_ = srv.Serve(ctx, mcp.NewSSETransport(":8080"))
```

Client wiring (Cursor `~/.cursor/mcp.json` style):

```json
{
  "mcpServers": {
    "my-tools": {
      "url": "http://localhost:8080/sse"
    }
  }
}
```

The transport demuxes a single active session at a time. If a
second client connects, the older session's stream is closed and
its stale POSTs return 404 — in practice every MCP client opens
exactly one session, so this is invisible.

### Streamable HTTP — the future default

`mcp.NewStreamableHTTPTransport(addr)` mounts a single `POST /`
endpoint. Session id is carried on the `Mcp-Session-Id` header.
This is the post-2024-11-05 spec direction and what the modern
TypeScript SDK now ships:

```go
srv := mcp.NewServer(reg, mcp.ServerInfo{Name: "my-tools", Version: "0.1"})
_ = srv.Serve(ctx, mcp.NewStreamableHTTPTransport(":8080"))
```

Client config:

```json
{
  "mcpServers": {
    "my-tools": {
      "url": "http://localhost:8080/"
    }
  }
}
```

`POST /` returns `application/json` for synchronous replies and
`202 Accepted` (no body) for notifications. `DELETE /` with the
session header terminates a session. Clients must send
`Accept: application/json, text/event-stream` so the transport can
upgrade to a streamed response in future revisions (progress
events, server-initiated notifications).

### Running both side-by-side

`*Server` is transport-agnostic; bind whichever transports your
clients need. The simplest approach is to start one process per
transport — they're independent listeners with their own ports:

```go
go func() { _ = srv.Serve(ctx, mcp.NewSSETransport(":8080")) }()
go func() { _ = srv.Serve(ctx, mcp.NewStreamableHTTPTransport(":8081")) }()
<-ctx.Done()
```

Use SSE for compat with shipping clients today; route new clients
at the Streamable HTTP port.

### When to pick HTTP over stdio

- You want one process to serve many users / IDE sessions.
- You want the server to live across IDE restarts (warm caches,
  persistent connections to internal APIs).
- The server needs to run on a different host from the IDE.
- You want web clients (browser, hosted UIs) — only HTTP works.

Otherwise stdio is simpler: no port to allocate, no auth story,
and IDEs already manage the process lifecycle.

### Limiting what gets exposed

Build a *subset* registry for MCP. The tools you give your own
agent (e.g., destructive ones) don't need to be the same set you
expose to a third-party client.

## Gotchas

- **stderr is not silent.** Whatever you write to stderr ends up
  in the client's MCP log. Useful for debugging; embarrassing for
  user-visible binaries. Default to no log output unless
  `--verbose` is passed.
- **No long-lived state across clients.** Each client spawn is a
  fresh process. If your tools need to share state (rate limits,
  caches), put it behind a network service the tool calls into.
- **JSON schemas are derived from Go types.** A `time.Time` field
  in your input struct won't have a sensible JSON-Schema
  representation. Prefer plain strings, ints, floats, structs
  composed of those, and slices.
- **Capability advertisement is fixed.** `mcp.NewServer`
  advertises tool-calling capability. Resources and prompts are
  not implemented yet — clients that need them will fall back
  cleanly, but the surface is tools-only today.
- **One process per client.** stdio transport is single-peer by
  definition. Multi-client support means launching one process
  per connection (which is what desktop clients already do).

## Links

- Runnable example: [examples/integration-mcp-server](../../examples/integration-mcp-server/)
- Concept: [mcp](../concepts/mcp.md)
- Concept: [tool](../concepts/tool.md)
