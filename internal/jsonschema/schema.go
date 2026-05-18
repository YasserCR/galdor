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
	Properties           map[string]*Schema `json:"properties,omitempty"`
	Required             []string           `json:"required,omitempty"`
	AdditionalProperties *bool              `json:"additionalProperties,omitempty"`

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
