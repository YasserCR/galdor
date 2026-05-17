package schema

import "testing"

func TestSystemMessage(t *testing.T) {
	t.Parallel()
	m := SystemMessage("be terse")
	if m.Role != RoleSystem {
		t.Errorf("Role = %q", m.Role)
	}
	if got := m.Text(); got != "be terse" {
		t.Errorf("Text = %q", got)
	}
}

func TestUserMessage(t *testing.T) {
	t.Parallel()
	m := UserMessage("hi")
	if m.Role != RoleUser || m.Text() != "hi" {
		t.Errorf("got %+v", m)
	}
}

func TestAssistantMessage(t *testing.T) {
	t.Parallel()
	m := AssistantMessage("ok")
	if m.Role != RoleAssistant || m.Text() != "ok" {
		t.Errorf("got %+v", m)
	}
}

func TestToolResultMessage(t *testing.T) {
	t.Parallel()
	m := ToolResultMessage("call-1", "42")
	if m.Role != RoleTool {
		t.Errorf("Role = %q", m.Role)
	}
	if m.ToolCallID != "call-1" {
		t.Errorf("ToolCallID = %q", m.ToolCallID)
	}
	if m.Text() != "42" {
		t.Errorf("Text = %q", m.Text())
	}
}

func TestMessage_Text_ConcatenatesTextPartsOnly(t *testing.T) {
	t.Parallel()
	m := Message{
		Role: RoleUser,
		Content: []ContentPart{
			TextPart("hello "),
			ImagePartURL("https://example.com/x.png"),
			TextPart("world"),
		},
	}
	if got := m.Text(); got != "hello world" {
		t.Errorf("Text = %q, want %q", got, "hello world")
	}
}

func TestEphemeralCache(t *testing.T) {
	t.Parallel()
	c := EphemeralCache()
	if c == nil || c.Type != CacheTypeEphemeral {
		t.Errorf("EphemeralCache = %+v", c)
	}
}
