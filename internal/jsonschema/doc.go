// Package jsonschema generates JSON Schema documents from Go struct
// types via reflection. It powers tool-input schema generation in
// pkg/tool (and, eventually, structured-output schemas in the eval
// framework).
//
// The package targets the small subset of JSON Schema draft 2020-12
// that LLM providers actually consume — type, properties, required,
// items, enum, format, additionalProperties. Recursive types and
// complex polymorphism (allOf, anyOf, oneOf) are intentionally out of
// scope; if a caller needs them they should hand-build a *Schema.
//
// See the README in pkg/tool for end-to-end usage.
package jsonschema
