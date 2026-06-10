package a2a

import (
	"encoding/json"
	"fmt"
	"time"
)

// AgentCardPath is the well-known location of an agent's card. The
// A2A spec mandates this exact suffix; clients hit
// <baseURL>/.well-known/agent.json to discover the card.
const AgentCardPath = "/.well-known/agent.json"

// ProtocolVersion is the A2A protocol revision this implementation
// targets. It is exported for callers who want to surface or compare
// it; the server does not currently embed it in the Agent Card, and
// no version negotiation or validation is performed on incoming
// requests.
const ProtocolVersion = "0.1"

// A2A JSON-RPC methods we implement.
const (
	MethodTasksSend = "tasks/send"
	MethodTasksGet  = "tasks/get"
)

// AgentCard is the metadata document served at /.well-known/agent.json.
// It tells clients what this agent can do, how to reach it, and how
// to authenticate. Required fields are Name and URL; everything else
// is optional but recommended.
type AgentCard struct {
	// Name is the agent's display name.
	Name string `json:"name"`

	// Description is a short, human-readable summary of what the
	// agent does.
	Description string `json:"description,omitempty"`

	// URL is the base URL clients POST tasks to. The JSON-RPC
	// endpoint is `URL` itself; the agent card is at
	// `URL` + AgentCardPath when the card is served from the same
	// host.
	URL string `json:"url"`

	// Version is the agent's own version string (semver, build SHA,
	// whatever the operator wants surfaced).
	Version string `json:"version,omitempty"`

	// Provider identifies the organization running the agent.
	Provider *AgentProvider `json:"provider,omitempty"`

	// Capabilities advertises optional protocol features.
	Capabilities AgentCapabilities `json:"capabilities"`

	// Skills lists the discrete things this agent can do. Skills are
	// purely descriptive in v0.1 — they don't constrain what
	// messages clients can send — but a client may use them to pick
	// the right agent.
	Skills []AgentSkill `json:"skills,omitempty"`

	// DefaultInputModes / DefaultOutputModes list content types the
	// agent accepts and produces by default. Use IANA media types
	// (e.g., "text/plain", "application/json"). Optional.
	DefaultInputModes  []string `json:"defaultInputModes,omitempty"`
	DefaultOutputModes []string `json:"defaultOutputModes,omitempty"`
}

// AgentProvider identifies the organization running the agent.
type AgentProvider struct {
	Organization string `json:"organization,omitempty"`
	URL          string `json:"url,omitempty"`
}

// AgentCapabilities lists optional protocol features this agent
// implements. All fields are false by default; we only set Streaming
// or PushNotifications when an explicit follow-up session adds them.
type AgentCapabilities struct {
	Streaming         bool `json:"streaming,omitempty"`
	PushNotifications bool `json:"pushNotifications,omitempty"`
}

// AgentSkill is one discrete capability the agent advertises.
type AgentSkill struct {
	// ID is a stable identifier (machine-readable).
	ID string `json:"id"`

	// Name is the human-readable name.
	Name string `json:"name"`

	// Description elaborates on what the skill does.
	Description string `json:"description,omitempty"`

	// Tags are free-form keywords for search/filtering.
	Tags []string `json:"tags,omitempty"`

	// Examples are sample user messages that would invoke this skill.
	// Optional; helpful for client-side routing UIs.
	Examples []string `json:"examples,omitempty"`
}

// TaskState is the discrete lifecycle state of a task.
type TaskState string

// TaskState values per the A2A spec.
const (
	TaskSubmitted     TaskState = "submitted"
	TaskWorking       TaskState = "working"
	TaskInputRequired TaskState = "input-required"
	TaskCompleted     TaskState = "completed"
	TaskFailed        TaskState = "failed"
	TaskCanceled      TaskState = "canceled"
)

// Task is the unit of work the protocol revolves around. Clients
// create a task by posting tasks/send; servers update its Status and
// History as they process it; clients poll via tasks/get until the
// state is terminal (Completed / Failed / Canceled).
type Task struct {
	// ID is the task identifier. Clients may supply one; if absent,
	// the server generates one.
	ID string `json:"id"`

	// SessionID, when set, groups related tasks for a single
	// logical conversation.
	SessionID string `json:"sessionId,omitempty"`

	// Status is the current state of the task plus an optional
	// status message (e.g., for input-required prompts).
	Status TaskStatus `json:"status"`

	// History is the message log: the initial user message followed
	// by any assistant / user turns. The server is free to drop old
	// turns from this list (subject to its own retention policy),
	// but the most recent turn must remain.
	History []Message `json:"history,omitempty"`

	// Artifacts are intermediate outputs the agent emitted that
	// don't belong in the message log (files, JSON blobs, …). Not
	// surfaced in Session C beyond the field declaration.
	Artifacts []Artifact `json:"artifacts,omitempty"`

	// Metadata is opaque key/value data the client and server can
	// attach to a task.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// TaskStatus is the status block of a Task.
type TaskStatus struct {
	State     TaskState `json:"state"`
	Message   *Message  `json:"message,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
}

// Artifact is a non-message output of a task.
type Artifact struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Parts       []Part `json:"parts"`
}

// Role identifies who sent a message.
type Role string

// Role values per the A2A spec.
const (
	RoleUser  Role = "user"
	RoleAgent Role = "agent"
)

// Message is one turn in a task's history.
type Message struct {
	Role  Role   `json:"role"`
	Parts []Part `json:"parts"`
}

// Part is one element of a Message's content. The A2A spec defines
// "text", "file" and "data" parts; we only implement "text" in this
// session. Future parts deserialize correctly because Type is the
// discriminator and the unused fields stay nil.
type Part struct {
	Type string `json:"type"`

	// Text is set when Type == "text".
	Text string `json:"text,omitempty"`

	// Metadata carries part-specific metadata.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// TextPart is a constructor for text parts.
func TextPart(s string) Part { return Part{Type: "text", Text: s} }

// UserText is a convenience for the most common case: a user-role
// message containing a single text part.
func UserText(s string) Message {
	return Message{Role: RoleUser, Parts: []Part{TextPart(s)}}
}

// AgentText is the agent-role counterpart of UserText.
func AgentText(s string) Message {
	return Message{Role: RoleAgent, Parts: []Part{TextPart(s)}}
}

// Text returns the concatenated text of all text parts in the message.
// Non-text parts are skipped. Useful for plumbing A2A into providers
// that expect a flat string.
func (m Message) Text() string {
	var out string
	for _, p := range m.Parts {
		if p.Type == "text" {
			if out != "" {
				out += "\n"
			}
			out += p.Text
		}
	}
	return out
}

// snapshot returns a copy of the task safe to hand out (or encode)
// after the caller releases the task's lock. The History slice is
// copied so a subsequent append on the live task can't mutate or
// re-slice the backing array observed by the snapshot's reader.
func (t *Task) snapshot() Task {
	cp := *t
	if t.History != nil {
		cp.History = make([]Message, len(t.History))
		copy(cp.History, t.History)
	}
	return cp
}

// deepCopy returns a fully independent copy of the task, safe to hand to
// a Handler that mutates it (appends history, sets artifacts, flips
// status) while concurrent readers observe the original through the
// task's lock. Unlike snapshot it also clones Artifacts, the status
// message and Metadata so a handler's mutations can't bleed into the
// stored task before the result is committed.
func (t *Task) deepCopy() *Task {
	cp := *t
	if t.History != nil {
		cp.History = make([]Message, len(t.History))
		copy(cp.History, t.History)
	}
	if t.Artifacts != nil {
		cp.Artifacts = make([]Artifact, len(t.Artifacts))
		copy(cp.Artifacts, t.Artifacts)
	}
	if t.Status.Message != nil {
		m := *t.Status.Message
		cp.Status.Message = &m
	}
	if t.Metadata != nil {
		cp.Metadata = make(map[string]any, len(t.Metadata))
		for k, v := range t.Metadata {
			cp.Metadata[k] = v
		}
	}
	return &cp
}

// Append adds a message to the task's history and updates the status
// timestamp. Convenience for handlers building responses.
func (t *Task) Append(m Message) {
	t.History = append(t.History, m)
	if t.Status.Timestamp.IsZero() {
		t.Status.Timestamp = time.Now().UTC()
	}
}

// JSON-RPC 2.0 standard error codes.
const (
	ErrCodeParseError     = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternalError  = -32603
)

// A2A-specific error codes per the spec.
const (
	ErrCodeTaskNotFound = -32001
	// ErrCodeInvalidTaskState is returned when a request targets a task
	// whose current state doesn't allow the operation — e.g. a
	// tasks/send against a task already in a terminal state.
	ErrCodeInvalidTaskState = -32002
)

// rpcMessage is the union JSON-RPC envelope. Same shape as MCP's
// rpcMessage but kept locally so the two packages don't couple.
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("a2a: JSON-RPC %d: %s", e.Code, e.Message)
}

// tasksSendParams is the params payload for `tasks/send`.
type tasksSendParams struct {
	ID        string         `json:"id,omitempty"`
	SessionID string         `json:"sessionId,omitempty"`
	Message   Message        `json:"message"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// tasksGetParams is the params payload for `tasks/get`.
type tasksGetParams struct {
	ID            string `json:"id"`
	HistoryLength int    `json:"historyLength,omitempty"`
}
