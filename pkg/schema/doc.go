// Package schema defines the shared types used across galdor: Role, Message,
// ContentPart, ToolCall, ToolDef, Usage, StopReason and CacheControl.
//
// These types form the lingua franca between provider, tool, graph and
// observability. They are intentionally minimal, free of provider
// specifics, and JSON-friendly so they can be serialized for traces,
// checkpoints and the wire.
package schema
