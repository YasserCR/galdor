// Package a2a implements Google's Agent-to-Agent (A2A) protocol for
// interoperability between independently-developed agents.
//
// The A2A spec defines an HTTP + JSON-RPC 2.0 surface centered on the
// task lifecycle: one agent (the client) posts a task to another (the
// server), the server processes it, and the client polls or
// subscribes for the result. Agents discover each other via an "Agent
// Card" at /.well-known/agent.json that advertises their URL, skills
// and authentication requirements.
//
// What this package supports in Session C of Phase 7:
//
//   - Agent Card publishing (server) and discovery (client)
//   - tasks/send  — submit a task synchronously
//   - tasks/get   — fetch task state and history
//   - text-part messages (single-turn and multi-turn)
//
// Out of scope for this session: streaming responses (tasks/sendSubscribe),
// push notifications, file/data content parts, and the OpenAPI surface.
// All slot into the same package without API churn.
//
// A galdor agent is exposed as A2A by wrapping a Handler:
//
//	srv := a2a.NewServer(a2a.AgentCard{
//	    Name: "galdor-helper", URL: "https://example.com/a2a", Version: "0.1",
//	}, a2a.HandlerFunc(func(ctx context.Context, t *a2a.Task) error {
//	    // Run the user's ReAct agent / Supervisor / Swarm here.
//	    t.Append(a2a.AssistantText("answer"))
//	    t.Status = a2a.TaskCompleted
//	    return nil
//	}))
//	http.Handle("/", srv)
//
// Consuming an external A2A agent:
//
//	c := a2a.NewClient("https://other-agent.example.com")
//	card, _ := c.FetchAgentCard(ctx)
//	resp, _ := c.SendTask(ctx, a2a.UserText("hi"))
package a2a
