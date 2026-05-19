// Command integration-mcp-server exposes a galdor tool registry as
// an MCP server over stdio. Any MCP client (Claude Desktop, Cursor,
// custom code) can connect and call the tools as if they were its
// own.
//
// What this exercises end-to-end:
//
//   - pkg/mcp.Server wrapping a tool.Registry
//   - pkg/mcp stdio transport (one JSON object per line)
//   - tool.builtins (math, time) as production-grade tools
//   - A custom domain tool (lookup_doc) showing the path for
//     wrapping an internal API as an MCP-exposed tool
//
// Run with:
//
//	go run ./examples/integration-mcp-server
//
// The binary reads JSON-RPC frames from stdin and writes them to
// stdout. To connect from Claude Desktop, add an entry to
// ~/Library/Application Support/Claude/claude_desktop_config.json:
//
//	{
//	  "mcpServers": {
//	    "galdor-demo": {
//	      "command": "/absolute/path/to/the/built/binary"
//	    }
//	  }
//	}
//
// Then restart Claude Desktop. The four tools (now, math, lookup_doc,
// open_ticket) appear in the tool picker.
//
// To smoke-test locally without Claude Desktop:
//
//	go run ./examples/integration-mcp-server <<EOF
//	{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}
//	{"jsonrpc":"2.0","method":"notifications/initialized"}
//	{"jsonrpc":"2.0","id":2,"method":"tools/list"}
//	{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"now","arguments":{}}}
//	EOF
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/YasserCR/galdor/pkg/mcp"
	"github.com/YasserCR/galdor/pkg/tool"
	"github.com/YasserCR/galdor/pkg/tool/builtins"
)

func main() {
	reg := buildRegistry()
	srv := mcp.NewServer(reg, mcp.ServerInfo{
		Name:    "galdor-demo",
		Version: "0.1",
	})
	srv.Strict = true // reject pre-initialize traffic

	// stdio transport — exactly what Claude Desktop spawns and
	// pipes its conversation through.
	transport := mcp.NewStdioTransport(os.Stdin, os.Stdout)

	// Catch SIGINT/SIGTERM so a Ctrl-C from the parent shuts us
	// down cleanly instead of orphaning the goroutine.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := srv.Serve(ctx, transport); err != nil {
		// cancel() is invoked explicitly before exit so the signal
		// handler goroutine gets a chance to drain; log.Fatal would
		// skip the defer above.
		cancel()
		log.Printf("mcp server: %v", err)
		os.Exit(1) //nolint:gocritic // defer cancel() was invoked explicitly above
	}
}

// buildRegistry wires together every tool this MCP server exposes.
// The mix is deliberately small but realistic:
//
//   - now: zero-config builtin (most clients want "what time is it")
//   - math: zero-config builtin (lots of LLM use cases need math)
//   - lookup_doc: a custom tool wrapping a fake internal KB —
//     stand-in for "wrap your internal API and expose it via MCP"
//   - open_ticket: a custom tool showing how a tool can have side
//     effects beyond returning data
func buildRegistry() *tool.Registry {
	now, err := builtins.NewTimeTool()
	if err != nil {
		log.Fatal(err)
	}
	mathTool, err := builtins.NewMathTool()
	if err != nil {
		log.Fatal(err)
	}

	type lookupIn struct {
		Topic string `json:"topic" jsonschema:"the documentation topic to look up"`
	}
	type lookupOut struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	lookup := tool.MustNewTool("lookup_doc",
		"Look up a documentation topic from our internal knowledge base",
		func(_ context.Context, in lookupIn) (lookupOut, error) {
			// Stand-in for a real KB lookup (Confluence, Notion,
			// a Postgres FTS index, whatever).
			kb := map[string]struct{ title, body string }{
				"deployment": {
					title: "How to deploy the service",
					body:  "Push to main, the CI pipeline runs tests, builds the container, and rolls out to staging.",
				},
				"oncall": {
					title: "Oncall rotation",
					body:  "Weekly rotation, primary handles alerts during business hours, secondary covers nights.",
				},
				"refund": {
					title: "Refund policy",
					body:  "Refunds within 30 days, requires invoice ID and reason.",
				},
			}
			for k, v := range kb {
				if strings.Contains(strings.ToLower(in.Topic), k) {
					return lookupOut{Title: v.title, Body: v.body}, nil
				}
			}
			return lookupOut{
				Title: "no match",
				Body:  fmt.Sprintf("No KB entry matched topic %q. Try: deployment, oncall, refund.", in.Topic),
			}, nil
		})

	type ticketIn struct {
		Title    string `json:"title"`
		Severity string `json:"severity" jsonschema:"one of: low, medium, high"`
	}
	type ticketOut struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	openTicket := tool.MustNewTool("open_ticket",
		"Open a new support ticket in our tracking system",
		func(_ context.Context, in ticketIn) (ticketOut, error) {
			// Stand-in for a real ticket-system API call. The ID is
			// deterministic-ish (based on title hash) so repeated
			// calls with the same title don't litter the demo.
			id := fmt.Sprintf("TKT-%04d", simpleHash(in.Title)%10000)
			return ticketOut{
				ID:  id,
				URL: "https://tickets.example.com/" + id,
			}, nil
		})

	reg, err := tool.NewRegistry(now, mathTool, lookup, openTicket)
	if err != nil {
		log.Fatal(err)
	}
	return reg
}

func simpleHash(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}
