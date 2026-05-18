package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/YasserCR/galdor/pkg/schema"
)

// Result is the outcome of running one schema.ToolCall through the
// registry. The order of a []Result returned by ExecuteCalls matches
// the order of the input []schema.ToolCall, so callers can index
// directly or feed the results back to the assistant in the same
// order they were requested.
//
// When Err is non-nil, Output is also nil; otherwise Output carries
// the JSON-serialized tool output as the agent should ship it back
// to the LLM in a tool-result message.
type Result struct {
	ID     string
	Name   string
	Output json.RawMessage
	Err    error
}

// AsToolResultMessages converts the slice of results into the
// schema.Message sequence the assistant expects in a follow-up turn.
// Each result becomes one ToolResultMessage; errors are surfaced as
// the message body so the model can recover.
func AsToolResultMessages(results []Result) []schema.Message {
	out := make([]schema.Message, 0, len(results))
	for _, r := range results {
		var body string
		switch {
		case r.Err != nil:
			body = "error: " + r.Err.Error()
		case len(r.Output) == 0:
			body = "null"
		default:
			body = string(r.Output)
		}
		out = append(out, schema.ToolResultMessage(r.ID, body))
	}
	return out
}

// ErrUnknownTool is returned for a tool call whose name doesn't match
// any registered tool. Distinct sentinel so callers can decide whether
// to surface the error to the model (for self-recovery) or treat it
// as a programmer bug.
var ErrUnknownTool = errors.New("tool: unknown tool")

// ExecuteCalls dispatches each call in calls to the matching tool in
// the registry concurrently. The returned slice preserves the input
// order regardless of which goroutine finished first.
//
// Cancellation: when ctx is canceled, in-flight tool functions receive
// the cancellation through their context arg, and any not-yet-started
// calls return ctx.Err() in their Result.Err. The function itself
// always waits for every goroutine to finish before returning, so the
// caller doesn't observe a goroutine leak.
func ExecuteCalls(ctx context.Context, reg *Registry, calls []schema.ToolCall) []Result {
	if reg == nil {
		out := make([]Result, len(calls))
		for i, c := range calls {
			out[i] = Result{ID: c.ID, Name: c.Name, Err: fmt.Errorf("tool: nil registry")}
		}
		return out
	}
	out := make([]Result, len(calls))
	var wg sync.WaitGroup
	for i, c := range calls {
		wg.Add(1)
		go func(i int, c schema.ToolCall) {
			defer wg.Done()
			out[i] = executeOne(ctx, reg, c)
		}(i, c)
	}
	wg.Wait()
	return out
}

// executeOne resolves the tool by name and runs it. A canceled context
// short-circuits before the lookup.
func executeOne(ctx context.Context, reg *Registry, c schema.ToolCall) Result {
	res := Result{ID: c.ID, Name: c.Name}
	if err := ctx.Err(); err != nil {
		res.Err = err
		return res
	}
	t, ok := reg.Get(c.Name)
	if !ok {
		res.Err = fmt.Errorf("%w: %q", ErrUnknownTool, c.Name)
		return res
	}
	output, err := t.ExecuteJSON(ctx, c.Arguments)
	if err != nil {
		res.Err = err
		return res
	}
	res.Output = output
	return res
}
