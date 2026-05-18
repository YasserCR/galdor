package jsonschema

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// For builds a JSON Schema document describing the layout of T. T should
// be a struct or a pointer to a struct; primitive top-level types are
// also accepted but produce a trivial schema.
//
// Tag conventions:
//
//   - `json:"name,omitempty"` — field name + omitempty marks the field
//     as not required.
//   - `json:"-"` — field skipped.
//   - `jsonschema:"<description>"` — field description.
//   - `jsonschema:"<description>,format=<fmt>"` — description plus an
//     explicit format hint (date-time, byte, etc.).
//
// Recursive types are detected and rejected with an error so the caller
// can't accidentally produce an infinite schema. Future revisions may
// add `$ref` / `$defs` support; for now keep input shapes tree-like.
func For[T any]() (*Schema, error) {
	var zero T
	return ForType(reflect.TypeOf(zero))
}

// ForValue is the reflect.Type-free variant. Useful when the type is
// only known dynamically.
func ForValue(v any) (*Schema, error) {
	return ForType(reflect.TypeOf(v))
}

// ForType is the entry point used internally and by callers who already
// have a reflect.Type in hand.
func ForType(t reflect.Type) (*Schema, error) {
	if t == nil {
		// `any` / interface{} — schema-less.
		return &Schema{}, nil
	}
	return buildSchema(t, map[reflect.Type]bool{})
}

// buildSchema walks the reflect.Type. seen carries the recursion stack
// to detect cycles.
func buildSchema(t reflect.Type, seen map[reflect.Type]bool) (*Schema, error) {
	// Unwrap pointers — pointer-ness is communicated via the parent
	// struct's "required" decision, not via a nullable type here.
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	// Special cases.
	switch t {
	case reflect.TypeOf(time.Time{}):
		return &Schema{Type: "string", Format: "date-time"}, nil
	case reflect.TypeOf(json.RawMessage{}):
		return &Schema{}, nil // any
	}

	if seen[t] {
		return nil, fmt.Errorf("jsonschema: recursive type %s not supported", t.String())
	}

	switch t.Kind() {
	case reflect.String:
		return &Schema{Type: "string"}, nil
	case reflect.Bool:
		return &Schema{Type: "boolean"}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return &Schema{Type: "integer"}, nil
	case reflect.Float32, reflect.Float64:
		return &Schema{Type: "number"}, nil

	case reflect.Slice, reflect.Array:
		// []byte → JSON Schema string with format=byte (base64).
		if t.Elem().Kind() == reflect.Uint8 {
			return &Schema{Type: "string", Format: "byte"}, nil
		}
		seen[t] = true
		defer delete(seen, t)
		item, err := buildSchema(t.Elem(), seen)
		if err != nil {
			return nil, err
		}
		return &Schema{Type: "array", Items: item}, nil

	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return nil, fmt.Errorf("jsonschema: map keys must be strings, got %s", t.Key())
		}
		seen[t] = true
		defer delete(seen, t)
		val, err := buildSchema(t.Elem(), seen)
		if err != nil {
			return nil, err
		}
		// We model `map[string]T` as an object whose additional
		// properties match T's schema. The known-properties set is
		// empty.
		out := &Schema{Type: "object"}
		// Smuggle the value schema through Properties[""] is the
		// common hack; instead we leave properties empty and rely on
		// describing it via Items? No — JSON Schema uses
		// additionalProperties with a Schema value. We can't model
		// that without adding it. Future ADR.
		_ = val
		out.AdditionalProperties = boolPtr(true)
		return out, nil

	case reflect.Struct:
		seen[t] = true
		defer delete(seen, t)
		return buildStruct(t, seen)

	case reflect.Interface:
		// `any` or other interface — leave unconstrained.
		return &Schema{}, nil

	default:
		return nil, fmt.Errorf("jsonschema: unsupported type %s (kind %s)", t.String(), t.Kind())
	}
}

// buildStruct walks the fields of a struct type and produces an object
// schema. Embedded structs have their fields promoted to the parent.
func buildStruct(t reflect.Type, seen map[reflect.Type]bool) (*Schema, error) {
	out := &Schema{
		Type:                 "object",
		Properties:           map[string]*Schema{},
		AdditionalProperties: boolPtr(false),
	}
	if err := collectFields(t, out, seen); err != nil {
		return nil, err
	}
	if len(out.Required) == 0 {
		out.Required = nil
	}
	if len(out.Properties) == 0 {
		out.Properties = nil
	}
	return out, nil
}

func collectFields(t reflect.Type, out *Schema, seen map[reflect.Type]bool) error {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}

		// Anonymous (embedded) struct → promote its fields.
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			if err := collectFields(f.Type, out, seen); err != nil {
				return err
			}
			continue
		}

		name, opts := parseJSONTag(f)
		if name == "-" {
			continue
		}

		sub, err := buildSchema(f.Type, seen)
		if err != nil {
			return err
		}
		applyJSONSchemaTag(sub, f.Tag.Get("jsonschema"))

		out.Properties[name] = sub
		if !opts.omitempty && f.Type.Kind() != reflect.Pointer {
			out.Required = append(out.Required, name)
		}
	}
	return nil
}

// jsonTagOpts captures the parsed bits of a json:"..." struct tag that
// the schema builder needs.
type jsonTagOpts struct {
	omitempty bool
}

func parseJSONTag(f reflect.StructField) (name string, opts jsonTagOpts) {
	raw := f.Tag.Get("json")
	if raw == "" {
		return f.Name, opts
	}
	parts := strings.Split(raw, ",")
	if parts[0] != "" {
		name = parts[0]
	} else {
		name = f.Name
	}
	for _, p := range parts[1:] {
		if p == "omitempty" {
			opts.omitempty = true
		}
	}
	return name, opts
}

// applyJSONSchemaTag overlays description / format hints from the
// `jsonschema` struct tag onto an already-built sub-schema.
//
// Tag grammar:
//
//	jsonschema:"description text"
//	jsonschema:"description text,format=date-time"
//	jsonschema:"format=byte"
//	jsonschema:"description text,enum=a;b;c"
//
// The first comma-separated segment without an `=` is the description.
// Subsequent `key=value` segments override format / enum. The enum
// value list is `;`-separated to avoid colliding with the outer comma.
func applyJSONSchemaTag(s *Schema, tag string) {
	if tag == "" {
		return
	}
	for _, raw := range strings.Split(tag, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		k, v, hasEq := strings.Cut(raw, "=")
		if !hasEq {
			// Bare segment → description (only honored once).
			if s.Description == "" {
				s.Description = raw
			}
			continue
		}
		switch strings.TrimSpace(k) {
		case "description":
			s.Description = strings.TrimSpace(v)
		case "format":
			s.Format = strings.TrimSpace(v)
		case "enum":
			vals := strings.Split(v, ";")
			s.Enum = make([]any, 0, len(vals))
			for _, x := range vals {
				s.Enum = append(s.Enum, strings.TrimSpace(x))
			}
		}
	}
}
