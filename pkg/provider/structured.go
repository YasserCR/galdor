package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/YasserCR/galdor/internal/jsonschema"
	"github.com/YasserCR/galdor/pkg/schema"
)

// JSONSchemaFor returns the JSON Schema document describing T, ready to
// drop into Request.ResponseFormat.Schema. T should be a struct (or a
// pointer to one); the tags are the same ones tools use:
//
//	json:"name,omitempty"        field name; omitempty drops it from required
//	json:"-"                     skip the field
//	jsonschema:"a description"   field description (and ,format=… hints)
//
// Object schemas are closed (additionalProperties: false) and list their
// required fields, which is what strict structured-output modes expect.
func JSONSchemaFor[T any]() ([]byte, error) {
	s, err := jsonschema.For[T]()
	if err != nil {
		return nil, fmt.Errorf("provider: derive JSON schema: %w", err)
	}
	return json.Marshal(s)
}

// GenerateStructured runs a single Generate call constrained to T's JSON
// schema and decodes the reply into T. When req.ResponseFormat is nil it
// is filled in from T (a json_schema format named after the type); set it
// yourself to override the name or use a hand-written schema.
//
// The provider must report Capabilities.StructuredOutput — Generate
// returns ErrUnsupported otherwise. Today that's OpenAI (and
// OpenAI-compatible hosts), Google, and Anthropic.
func GenerateStructured[T any](ctx context.Context, p Provider, req Request) (T, error) {
	var zero T
	if req.ResponseFormat == nil {
		raw, err := JSONSchemaFor[T]()
		if err != nil {
			return zero, err
		}
		req.ResponseFormat = &ResponseFormat{
			Type:   ResponseFormatJSONSchema,
			Schema: raw,
			Name:   typeName[T](),
		}
	}
	resp, err := p.Generate(ctx, req)
	if err != nil {
		return zero, err
	}
	out, err := schema.ParseJSON[T](resp.Message.Text())
	if err != nil {
		return zero, fmt.Errorf("provider: decode structured response: %w", err)
	}
	return out, nil
}

// typeName returns T's type name (e.g. "Recipe") for use as the schema
// name, falling back to "response" for unnamed types.
func typeName[T any]() string {
	t := reflect.TypeOf((*T)(nil)).Elem()
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if n := t.Name(); n != "" {
		return n
	}
	return "response"
}
