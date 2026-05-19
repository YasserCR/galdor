# integration-mcp-server

Wraps a galdor `tool.Registry` as an **MCP server over stdio** so
any MCP client (Claude Desktop, Cursor, your own code) can call its
tools as if they were native.

The four tools shipped here:

| name | source | what it does |
|---|---|---|
| `time` | `pkg/tool/builtins.NewTimeTool` | `now` / `parse` / `format` timestamps with timezone support |
| `math` | `pkg/tool/builtins.NewMathTool` | basic operations: add/sub/mul/div/mod/pow/sqrt/abs/ln/log10/exp |
| `lookup_doc` | local | stand-in for an internal knowledge-base lookup |
| `open_ticket` | local | stand-in for "create a ticket in the tracking system" |

`lookup_doc` and `open_ticket` are the **pattern** for wrapping
your internal APIs as MCP tools — replace the in-memory map and
the deterministic ID with real calls, and you have a production
MCP server in a few dozen lines.

## Running it standalone

A quick smoke test without any client:

```bash
cat <<'EOF' | go run ./examples/integration-mcp-server
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"lookup_doc","arguments":{"topic":"deployment"}}}
EOF
```

You'll see four JSON-RPC responses on stdout: the initialize ack,
the tool catalog, and the tool result.

## Connecting from Claude Desktop

1. Build the binary so Claude Desktop has a stable path to invoke:

   ```bash
   go install ./examples/integration-mcp-server
   # the binary lands in $(go env GOPATH)/bin/integration-mcp-server
   ```

2. Open `~/Library/Application Support/Claude/claude_desktop_config.json`
   (create it if missing) and add a `mcpServers` entry:

   ```json
   {
     "mcpServers": {
       "galdor-demo": {
         "command": "/Users/yasser/go/bin/integration-mcp-server"
       }
     }
   }
   ```

   Replace the path with the actual output of `go env GOPATH`.

3. Restart Claude Desktop.

4. In a new conversation, the four tools appear in the tool picker.
   Ask Claude something like *"open a high-severity ticket titled
   'database CPU spike' and tell me the URL"* — Claude will call
   `open_ticket` and surface the URL back to you.

## Wrapping your real APIs

Each tool is just a Go function returned by `tool.MustNewTool`:

```go
type lookupIn struct {
    Topic string `json:"topic" jsonschema:"the documentation topic to look up"`
}

lookup := tool.MustNewTool("lookup_doc",
    "Look up a documentation topic",
    func(ctx context.Context, in lookupIn) (lookupOut, error) {
        // your real HTTP / DB call here
    })
```

`pkg/tool` generates the JSON Schema from the input type by
reflection, so the only documentation you write is the struct
fields and the description string.

## Files

* `main.go` — server entry point, tool registry, four tools
