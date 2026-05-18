// Package tool provides galdor's type-safe tool system.
//
// A Tool[In, Out] is a Go function the LLM can invoke, with its input
// described by a JSON Schema derived from the In type via reflection.
// A Registry indexes tools by name and produces the schema.ToolDef
// slice that providers ship in their Request.Tools. The executor
// runs a batch of incoming tool calls concurrently and maps the
// results back to schema.ToolResultMessage instances.
//
// Typical use:
//
//	type WeatherIn struct {
//	    City string `json:"city" jsonschema:"City to look up"`
//	}
//	type WeatherOut struct {
//	    TempC int `json:"temp_c"`
//	}
//
//	wx := tool.MustNewTool("weather", "Look up the current weather",
//	    func(ctx context.Context, in WeatherIn) (WeatherOut, error) {
//	        return WeatherOut{TempC: 21}, nil
//	    })
//
//	reg, _ := tool.NewRegistry(wx)
//	defs, _ := reg.ToolDefs()
//
//	resp, _ := provider.Generate(ctx, provider.Request{
//	    Model:    "claude-3-5-haiku-20241022",
//	    Messages: ...,
//	    Tools:    defs,
//	})
//	// resp.Message.ToolCalls comes back populated:
//	results := tool.ExecuteCalls(ctx, reg, resp.Message.ToolCalls)
//	// Feed results back into the next Generate call:
//	follow := append(messages, resp.Message)
//	follow = append(follow, tool.AsToolResultMessages(results)...)
package tool
