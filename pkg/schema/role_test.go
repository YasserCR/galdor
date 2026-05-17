package schema

import "testing"

func TestRole_Valid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		role Role
		want bool
	}{
		{RoleSystem, true},
		{RoleUser, true},
		{RoleAssistant, true},
		{RoleTool, true},
		{Role(""), false},
		{Role("human"), false},
	}
	for _, c := range cases {
		if got := c.role.Valid(); got != c.want {
			t.Errorf("Role(%q).Valid() = %v, want %v", c.role, got, c.want)
		}
	}
}

func TestRole_String(t *testing.T) {
	t.Parallel()
	if RoleAssistant.String() != "assistant" {
		t.Errorf("got %q", RoleAssistant.String())
	}
}
