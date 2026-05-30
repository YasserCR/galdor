package jsonschema

import "encoding/json"

// Schema is a minimal JSON Schema (draft 2020-12 subset) representation
// sufficient for the shapes tool inputs and structured outputs need.
// Fields that are unset are omitted on marshal.
type Schema struct {
	Type string `json:"type,omitempty"`

	Description string `json:"description,omitempty"`
	Format      string `json:"format,omitempty"`

	// Object schemas.
	Properties map[string]*Schema `json:"properties,omitempty"`
	Required   []string           `json:"required,omitempty"`
	// AdditionalProperties is either a *bool (false = closed object, the
	// canonical "no extra fields" form) or a *Schema describing the
	// permitted value shape (used for map[string]T). nil omits the field.
	AdditionalProperties any `json:"additionalProperties,omitempty"`

	// Array schema.
	Items *Schema `json:"items,omitempty"`

	// Constraints.
	Enum []any `json:"enum,omitempty"`

	// Reference / nullability.
	Ref string `json:"$ref,omitempty"`
}

// MarshalJSON ensures that an empty Schema marshals to an empty object,
// not to `null`. The default encoder would skip an entirely empty value;
// this preserves the JSON Schema convention that a missing constraint
// is expressed as `{}`.
func (s *Schema) MarshalJSON() ([]byte, error) {
	if s == nil {
		return []byte("{}"), nil
	}
	type alias Schema
	return json.Marshal((*alias)(s))
}

// boolPtr is a small helper for setting AdditionalProperties to a
// non-nil pointer to false (the canonical "no extra fields" form).
func boolPtr(b bool) *bool { return &b }

// FromRaw decodes raw JSON Schema bytes into a Schema. Useful when
// schemas arrive over the wire from an external source (MCP server,
// remote API) rather than being reflection-derived from a Go type.
// An empty input returns an empty Schema, matching the JSON Schema
// convention that `{}` means "no constraint".
func FromRaw(raw []byte) (*Schema, error) {
	s := &Schema{}
	if len(raw) == 0 || string(raw) == "{}" {
		return s, nil
	}
	if err := json.Unmarshal(raw, s); err != nil {
		return nil, err
	}
	return s, nil
}
