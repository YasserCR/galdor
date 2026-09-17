package schema

import "encoding/json"

// ToolDef describes a tool that the assistant is allowed to call.
//
// Schema is a JSON Schema document describing the tool's input. It is held
// as json.RawMessage so the provider can forward it verbatim without an
// extra round-trip through generic Go types. The pkg/tool package builds
// these definitions from Go struct types via reflection.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Schema      json.RawMessage `json:"schema"`
}

// ToolCall is a request from the assistant to invoke a tool. Arguments is
// raw JSON matching the tool's input Schema; the tool runtime is
// responsible for decoding it into a typed value.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`

	// Signature is an opaque token some providers attach to a call and
	// require back, unchanged, when the call is replayed with its result.
	// Gemini 3 sends one as thoughtSignature on every functionCall part
	// and rejects a follow-up request whose functionCall lacks it. Empty
	// for providers that have no such thing; callers that persist a
	// conversation must persist it with the call.
	Signature string `json:"signature,omitempty"`
}
