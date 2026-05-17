package schema

// Role identifies who produced a Message.
//
// Roles are normalized across providers. Anthropic's "human" and OpenAI's
// "user" both map to RoleUser; Anthropic's "assistant" and OpenAI's
// "assistant" both map to RoleAssistant. Provider adapters are responsible
// for translating between the wire format and these canonical values.
type Role string

const (
	// RoleSystem carries system instructions that shape the assistant's
	// behavior. Some providers (Anthropic) expose this as a dedicated
	// field rather than a message; adapters translate accordingly.
	RoleSystem Role = "system"

	// RoleUser is the human-equivalent participant.
	RoleUser Role = "user"

	// RoleAssistant is the model's reply.
	RoleAssistant Role = "assistant"

	// RoleTool is the result of a tool call, returned to the model so it
	// can continue reasoning with the output.
	RoleTool Role = "tool"
)

// Valid reports whether r is one of the known role constants.
func (r Role) Valid() bool {
	switch r {
	case RoleSystem, RoleUser, RoleAssistant, RoleTool:
		return true
	default:
		return false
	}
}

// String returns the role as a string.
func (r Role) String() string { return string(r) }
