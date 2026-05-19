# a2a

`pkg/a2a` implements Google's Agent-to-Agent protocol — the spec for interoperability between independently-developed agents over HTTP + JSON-RPC 2.0. The model is task-centric: a client agent posts a task (`tasks/send`), the server processes it, the client polls (`tasks/get`) until the state is terminal. Discovery happens via an "Agent Card" at `/.well-known/agent.json` that advertises the agent's URL, skills, and capabilities.

Supported today: agent card publishing and discovery, `tasks/send`, `tasks/get`, text-part messages, multi-turn `input-required` flow. Out of scope for now: `tasks/sendSubscribe` streaming, push notifications, file/data content parts.

## Core types

```go
type AgentCard struct {
    Name               string
    Description        string
    URL                string
    Version            string
    Provider           *AgentProvider
    Capabilities       AgentCapabilities
    Skills             []AgentSkill
    DefaultInputModes  []string
    DefaultOutputModes []string
}

type TaskState string
const (
    TaskSubmitted     TaskState = "submitted"
    TaskWorking       TaskState = "working"
    TaskInputRequired TaskState = "input-required"
    TaskCompleted     TaskState = "completed"
    TaskFailed        TaskState = "failed"
    TaskCanceled      TaskState = "canceled"
)

type Task struct {
    ID        string
    SessionID string
    Status    TaskStatus
    History   []Message
    Artifacts []Artifact
    Metadata  map[string]any
}

type Handler interface {
    Handle(ctx context.Context, t *Task) error
}
type HandlerFunc func(ctx context.Context, t *Task) error
```

Constructors for the common cases: `TextPart(s)`, `UserText(s)`, `AgentText(s)`, `Task.Append(m)`.

## Exposing a galdor agent over A2A

The server is an `http.Handler` so it slots into any net/http stack:

```go
import "github.com/YasserCR/galdor/pkg/a2a"

card := a2a.AgentCard{
    Name:    "galdor-helper",
    URL:     "https://example.com/a2a",
    Version: "0.1",
    Skills: []a2a.AgentSkill{
        {ID: "qa", Name: "Q&A", Description: "answers general questions"},
    },
}

srv := a2a.NewServer(card, a2a.HandlerFunc(func(ctx context.Context, t *a2a.Task) error {
    user := t.History[len(t.History)-1].Text()
    answer, err := agent.Run(ctx, agentCfg, user)
    if err != nil {
        return err
    }
    t.Append(a2a.AgentText(answer))
    t.Status.State = a2a.TaskCompleted
    return nil
}))

http.ListenAndServe(":8080", srv)
```

Two routes are served:

```
GET  /.well-known/agent.json   the AgentCard verbatim
POST <any other path>          JSON-RPC: tasks/send, tasks/get
```

The user message is appended to `Task.History` before the handler runs, and the state flips to `TaskWorking`. Returning an error transitions to `TaskFailed` with the error attached to `Status.Message`. Returning nil without setting a terminal state assumes `TaskCompleted`. Setting `Status.State = TaskInputRequired` pauses the task — the client supplies the next user turn by re-posting `tasks/send` with the same `Task.ID`.

## Consuming an external A2A agent

```go
c := a2a.NewClient("https://other-agent.example.com")
card, _ := c.FetchAgentCard(ctx)
fmt.Println(card.Name, card.URL)

t, _ := c.SendTask(ctx, a2a.UserText("hello"))
for !isTerminal(t.Status.State) {
    time.Sleep(500 * time.Millisecond)
    t, _ = c.GetTask(ctx, t.ID, 0)
}
fmt.Println(t.History[len(t.History)-1].Text())
```

`SendTask` takes options: `WithTaskID(id)` (continue an existing task), `WithSessionID(id)` (group related tasks), `WithMetadata(map)`. The default `http.Client` has a 60s timeout — override with `a2a.WithHTTPClient(client)`.

## Multi-turn (`input-required`)

```go
srv := a2a.NewServer(card, a2a.HandlerFunc(func(ctx context.Context, t *a2a.Task) error {
    last := t.History[len(t.History)-1].Text()
    if needsConfirmation(last) {
        t.Append(a2a.AgentText("Please confirm the transfer (yes/no)."))
        t.Status.State = a2a.TaskInputRequired
        return nil
    }
    t.Append(a2a.AgentText(process(last)))
    t.Status.State = a2a.TaskCompleted
    return nil
}))
```

The client sees `state: "input-required"` in the response, presents the agent's question to the user, then resends with the same `Task.ID`:

```go
t, _ := c.SendTask(ctx, a2a.UserText("transfer $500"))
// t.Status.State == TaskInputRequired
// t.History[-1].Text() == "Please confirm the transfer (yes/no)."

t, _ = c.SendTask(ctx, a2a.UserText("yes"), a2a.WithTaskID(t.ID))
// t.Status.State == TaskCompleted
```

The same handler runs again with the full history visible; it's the handler's job to recognize a confirmation reply and proceed.

## Gotchas

- Tasks are stored in-memory keyed by ID. Single-process deployments only — a pluggable persistent store is on the roadmap.
- JSON-RPC over HTTP always returns 200, even for protocol-level errors. The error lives in the response envelope, not the HTTP status. Real network failures still surface as non-2xx.
- The well-known path is exactly `/.well-known/agent.json`. The Agent Card is served with `Cache-Control: no-store` so clients always see the current capabilities.
- `Task.History` returned from `tasks/get` is a copy; mutating it on the client doesn't race with the server.
- `GetTask`'s third argument truncates the returned history to the last n turns (`0` = full history) — useful for long-running tasks.
- Empty `Message.Parts` is rejected with `InvalidParams`. Always include at least one `TextPart`.
- `AgentCapabilities.Streaming` and `PushNotifications` are advertised as `false` today; the field exists so newer revisions can flip it on without an API change.

## See also

- [agent](agent.md), [council](council.md) — the in-process equivalents.
- [mcp](mcp.md) — the other JSON-RPC protocol galdor speaks; different problem (tool exposure vs. agent-to-agent).
- [provider](provider.md) — what your `Handler` typically calls.
