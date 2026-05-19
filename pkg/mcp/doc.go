// Package mcp implements client and server sides of the Model Context
// Protocol (MCP) — Anthropic's spec for connecting LLM applications to
// external tools and data sources.
//
// What is supported in Session B of Phase 7:
//
//   - JSON-RPC 2.0 framing (the wire format MCP rides on)
//   - Initialize handshake + initialized notification
//   - tools/list  — discover the tools a server exposes
//   - tools/call  — invoke a remote tool with JSON arguments
//   - stdio transport (one JSON object per line over io.Reader / io.Writer)
//
// Client.AsRegistry converts a connected MCP server's tools into
// galdor tool.AnyTool values, ready to plug into pkg/agent's ReAct
// loop without glue code:
//
//	c, _ := mcp.NewClient(mcp.NewStdioTransport(stdin, stdout))
//	_ = c.Initialize(ctx)
//	reg, _ := c.AsRegistry(ctx)
//	r, _ := agent.NewReAct(agent.Config{
//	    Provider: p, Model: "claude-haiku-4-5", Tools: reg,
//	})
//
// Server wraps any tool.Registry and exposes it over the MCP wire so
// a galdor process can be consumed by Claude Desktop, IDE plugins,
// or any other MCP-compatible client:
//
//	srv := mcp.NewServer(reg, mcp.ServerInfo{Name: "galdor", Version: "0.1"})
//	_ = srv.Serve(ctx, mcp.NewStdioTransport(os.Stdin, os.Stdout))
//
// Out of scope for this session (planned follow-ups): resources,
// prompts, sampling, HTTP+SSE transport. Tools cover the majority of
// real-world MCP use today.
package mcp
