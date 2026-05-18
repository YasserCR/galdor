package jsonschema

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// marshal renders a schema to a stable JSON string so tests can do
// substring matching without worrying about field order.
func marshal(t *testing.T, s *Schema) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func TestFor_Primitives(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		fn   func() (*Schema, error)
		want string
	}{
		{"string", func() (*Schema, error) { return For[string]() }, `"type":"string"`},
		{"bool", func() (*Schema, error) { return For[bool]() }, `"type":"boolean"`},
		{"int", func() (*Schema, error) { return For[int]() }, `"type":"integer"`},
		{"int64", func() (*Schema, error) { return For[int64]() }, `"type":"integer"`},
		{"uint32", func() (*Schema, error) { return For[uint32]() }, `"type":"integer"`},
		{"float64", func() (*Schema, error) { return For[float64]() }, `"type":"number"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s, err := c.fn()
			if err != nil {
				t.Fatal(err)
			}
			if got := marshal(t, s); !strings.Contains(got, c.want) {
				t.Errorf("%s = %s, want substring %s", c.name, got, c.want)
			}
		})
	}
}

func TestFor_TimeIsDateTime(t *testing.T) {
	t.Parallel()
	s, err := For[time.Time]()
	if err != nil {
		t.Fatal(err)
	}
	js := marshal(t, s)
	if !strings.Contains(js, `"type":"string"`) || !strings.Contains(js, `"format":"date-time"`) {
		t.Errorf("got %s", js)
	}
}

func TestFor_RawMessageIsAny(t *testing.T) {
	t.Parallel()
	s, err := For[json.RawMessage]()
	if err != nil {
		t.Fatal(err)
	}
	if marshal(t, s) != "{}" {
		t.Errorf("RawMessage should yield empty schema, got %s", marshal(t, s))
	}
}

func TestFor_ByteSliceIsByteFormatString(t *testing.T) {
	t.Parallel()
	s, err := For[[]byte]()
	if err != nil {
		t.Fatal(err)
	}
	js := marshal(t, s)
	if !strings.Contains(js, `"type":"string"`) || !strings.Contains(js, `"format":"byte"`) {
		t.Errorf("got %s", js)
	}
}

func TestFor_StringSliceIsArray(t *testing.T) {
	t.Parallel()
	s, err := For[[]string]()
	if err != nil {
		t.Fatal(err)
	}
	js := marshal(t, s)
	if !strings.Contains(js, `"type":"array"`) || !strings.Contains(js, `"items":{"type":"string"}`) {
		t.Errorf("got %s", js)
	}
}

type Weather struct {
	City    string `json:"city" jsonschema:"City to look up"`
	Country string `json:"country,omitempty" jsonschema:"ISO-3166 country code (optional)"`
	Days    int    `json:"days" jsonschema:"description=Number of forecast days,format=int"`
	Detail  bool   `json:"detail,omitempty"`
	Skipped string `json:"-"`
	private string //nolint:unused // exercises unexported skip
}

func TestFor_StructWithTags(t *testing.T) {
	t.Parallel()
	s, err := For[Weather]()
	if err != nil {
		t.Fatal(err)
	}
	if s.Type != "object" {
		t.Errorf("Type = %q", s.Type)
	}
	if _, ok := s.Properties["city"]; !ok {
		t.Errorf("missing city property: %+v", s.Properties)
	}
	if _, ok := s.Properties["country"]; !ok {
		t.Errorf("missing country property: %+v", s.Properties)
	}
	if _, ok := s.Properties["-"]; ok {
		t.Errorf(`json:"-" field should be skipped`)
	}
	if _, ok := s.Properties["private"]; ok {
		t.Errorf("unexported field should be skipped")
	}
	if s.Properties["city"].Description != "City to look up" {
		t.Errorf("description not applied: %q", s.Properties["city"].Description)
	}
	if s.Properties["days"].Description != "Number of forecast days" {
		t.Errorf("description not applied: %q", s.Properties["days"].Description)
	}
	if s.Properties["days"].Format != "int" {
		t.Errorf("format not applied: %q", s.Properties["days"].Format)
	}
	// `country` is omitempty → not required. `detail` is omitempty → not required.
	// `city` and `days` are required.
	required := map[string]bool{}
	for _, r := range s.Required {
		required[r] = true
	}
	if !required["city"] || !required["days"] {
		t.Errorf("city and days should be required, got %+v", s.Required)
	}
	if required["country"] || required["detail"] {
		t.Errorf("omitempty fields should not be required, got %+v", s.Required)
	}
	if s.AdditionalProperties == nil || *s.AdditionalProperties {
		t.Errorf("AdditionalProperties should be false")
	}
}

type withPtr struct {
	Required string  `json:"required"`
	Optional *string `json:"optional"` // pointer → not required
}

func TestFor_PointerIsOptional(t *testing.T) {
	t.Parallel()
	s, err := For[withPtr]()
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{}
	for _, r := range s.Required {
		required[r] = true
	}
	if !required["required"] {
		t.Errorf("Required should be required, got %+v", s.Required)
	}
	if required["optional"] {
		t.Errorf("Pointer fields are optional by design, got %+v", s.Required)
	}
}

type nested struct {
	Outer string  `json:"outer"`
	Inner Weather `json:"inner"`
}

func TestFor_NestedStruct(t *testing.T) {
	t.Parallel()
	s, err := For[nested]()
	if err != nil {
		t.Fatal(err)
	}
	inner, ok := s.Properties["inner"]
	if !ok || inner.Type != "object" {
		t.Fatalf("inner missing or not object: %+v", inner)
	}
	if _, ok := inner.Properties["city"]; !ok {
		t.Errorf("nested object should expose its fields: %+v", inner.Properties)
	}
}

type embedded struct {
	Weather
	Extra string `json:"extra"`
}

func TestFor_EmbeddedFieldsPromoted(t *testing.T) {
	t.Parallel()
	s, err := For[embedded]()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"city", "country", "days", "extra"} {
		if _, ok := s.Properties[want]; !ok {
			t.Errorf("missing promoted field %q in %+v", want, s.Properties)
		}
	}
}

type recursive struct {
	Self *recursive `json:"self,omitempty"`
}

func TestFor_RecursiveRejected(t *testing.T) {
	t.Parallel()
	// A struct that contains a pointer to itself is the only form of
	// recursion Go's type system permits. The generator must reject it
	// rather than loop forever.
	if _, err := For[recursive](); err == nil {
		t.Fatal("expected error for recursive type")
	}
}

type withEnum struct {
	Unit string `json:"unit" jsonschema:"Unit of measure,enum=celsius;fahrenheit;kelvin"`
}

func TestFor_EnumTag(t *testing.T) {
	t.Parallel()
	s, err := For[withEnum]()
	if err != nil {
		t.Fatal(err)
	}
	unit := s.Properties["unit"]
	if len(unit.Enum) != 3 {
		t.Fatalf("enum = %+v", unit.Enum)
	}
	if unit.Enum[0] != "celsius" || unit.Enum[2] != "kelvin" {
		t.Errorf("enum values: %+v", unit.Enum)
	}
	if unit.Description != "Unit of measure" {
		t.Errorf("description: %q", unit.Description)
	}
}

func TestFor_MapStringT(t *testing.T) {
	t.Parallel()
	s, err := For[map[string]int]()
	if err != nil {
		t.Fatal(err)
	}
	if s.Type != "object" {
		t.Errorf("Type = %q", s.Type)
	}
	if s.AdditionalProperties == nil || !*s.AdditionalProperties {
		t.Errorf("AdditionalProperties should be true for map")
	}
}

func TestFor_MapNonStringKeyRejected(t *testing.T) {
	t.Parallel()
	if _, err := ForValue(map[int]string{}); err == nil {
		t.Fatal("expected error for map with non-string key")
	}
}

func TestFor_UnsupportedKind(t *testing.T) {
	t.Parallel()
	if _, err := ForValue(make(chan int)); err == nil {
		t.Fatal("expected error for channel")
	}
}

func TestMarshalJSON_NilSchema(t *testing.T) {
	t.Parallel()
	var s *Schema
	b, err := s.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "{}" {
		t.Errorf("nil schema should marshal to {}, got %s", b)
	}
}

func TestForType_NilTypeIsAny(t *testing.T) {
	t.Parallel()
	s, err := ForType(nil)
	if err != nil {
		t.Fatal(err)
	}
	if marshal(t, s) != "{}" {
		t.Errorf("nil type should yield empty schema, got %s", marshal(t, s))
	}
}
