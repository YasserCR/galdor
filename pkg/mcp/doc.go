// Package mcp implements client and server sides of the Model Context
// Protocol (MCP) — Anthropic's spec for connecting LLM applications to
// external tools and data sources.
//
// What is supported:
//
//   - JSON-RPC 2.0 framing (the wire format MCP rides on)
//   - Initialize handshake + initialized notification
//   - tools/list  — discover the tools a server exposes
//   - tools/call  — invoke a remote tool with JSON arguments
//   - stdio transport — one JSON object per line over io.Reader / io.Writer
//     (NewStdioTransport). What every desktop client and IDE plugin speaks
//     when they spawn a child-process MCP server.
//   - SSE transport — the HTTP+SSE transport from the 2024-11-05 spec
//     (NewSSETransport). One GET /sse stream per client + a POST /messages
//     endpoint for requests. Use when the server is a long-lived daemon
//     and the client is anything older than the post-2024-11-05 revision.
//   - Streamable HTTP transport — the single-endpoint transport from
//     the post-2024-11-05 revision (NewStreamableHTTPTransport). POST /,
//     session id rides on the Mcp-Session-Id header. The future default.
//
// All three transports satisfy the same Transport interface so a Server
// or Client doesn't care which one it's running over:
//
//	srv := mcp.NewServer(reg, mcp.ServerInfo{Name: "galdor"})
//	_ = srv.Serve(ctx, mcp.NewStdioTransport(os.Stdin, os.Stdout))
//	// or
//	_ = srv.Serve(ctx, mcp.NewSSETransport(":8080"))
//	// or
//	_ = srv.Serve(ctx, mcp.NewStreamableHTTPTransport(":8080"))
//
// Client.AsRegistry converts a connected MCP server's tools into
// galdor tool.AnyTool values, ready to plug into pkg/agent's ReAct
// loop without glue code:
//
//	c := mcp.NewClient(mcp.NewStdioTransport(stdin, stdout))
//	_ = c.Initialize(ctx)
//	reg, _ := c.AsRegistry(ctx)
//	r, _ := agent.NewReAct(agent.Config{
//	    Provider: p, Model: "claude-haiku-4-5", Tools: reg,
//	})
//
// Server wraps any tool.Registry and exposes it over the MCP wire so
// a galdor process can be consumed by Claude Desktop, IDE plugins,
// or any other MCP-compatible client.
//
// Out of scope (planned follow-ups): resources, prompts, sampling,
// server-initiated notifications over the GET-stream side of
// Streamable HTTP. Tools cover the majority of real-world MCP use today.
package mcp
