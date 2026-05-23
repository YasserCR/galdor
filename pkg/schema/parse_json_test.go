package schema

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type sampleShape struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func TestParseJSON_Clean(t *testing.T) {
	t.Parallel()
	out, err := ParseJSON[sampleShape](`{"name":"alpha","count":3}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Name != "alpha" || out.Count != 3 {
		t.Errorf("got %+v", out)
	}
}

func TestParseJSON_StripsJSONFence(t *testing.T) {
	t.Parallel()
	raw := "```json\n{\"name\":\"beta\",\"count\":7}\n```"
	out, err := ParseJSON[sampleShape](raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Name != "beta" || out.Count != 7 {
		t.Errorf("got %+v", out)
	}
}

func TestParseJSON_StripsBareFence(t *testing.T) {
	t.Parallel()
	raw := "```\n{\"name\":\"gamma\",\"count\":1}\n```\n"
	out, err := ParseJSON[sampleShape](raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Name != "gamma" {
		t.Errorf("Name = %q", out.Name)
	}
}

func TestParseJSON_TolerateLeadingProse(t *testing.T) {
	t.Parallel()
	raw := `Sure! Here's the data you asked for: {"name":"delta","count":42} hope that helps.`
	out, err := ParseJSON[sampleShape](raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Name != "delta" || out.Count != 42 {
		t.Errorf("got %+v", out)
	}
}

func TestParseJSON_TopLevelArray(t *testing.T) {
	t.Parallel()
	raw := "```json\n[{\"name\":\"a\",\"count\":1},{\"name\":\"b\",\"count\":2}]\n```"
	out, err := ParseJSON[[]sampleShape](raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 2 || out[1].Name != "b" {
		t.Errorf("got %+v", out)
	}
}

func TestParseJSON_PrimitiveString(t *testing.T) {
	t.Parallel()
	out, err := ParseJSON[string](`"hello"`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "hello" {
		t.Errorf("got %q", out)
	}
}

func TestParseJSON_PrimitiveNumber(t *testing.T) {
	t.Parallel()
	out, err := ParseJSON[int](`42`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != 42 {
		t.Errorf("got %d", out)
	}
}

func TestParseJSON_NoPayloadFound(t *testing.T) {
	t.Parallel()
	_, err := ParseJSON[sampleShape](`I cannot help with that.`)
	if err == nil {
		t.Fatal("expected error")
	}
	var bad *BadOutputError
	if !errors.As(err, &bad) {
		t.Fatalf("errors.As(*BadOutputError) failed: %v", err)
	}
	if bad.Reason != "no JSON payload found" {
		t.Errorf("Reason = %q", bad.Reason)
	}
}

func TestParseJSON_MalformedReturnsTypedError(t *testing.T) {
	t.Parallel()
	raw := `{"name":"x","count": "this is a string not a number"}`
	_, err := ParseJSON[sampleShape](raw)
	if err == nil {
		t.Fatal("expected error")
	}
	var bad *BadOutputError
	if !errors.As(err, &bad) {
		t.Fatalf("errors.As(*BadOutputError) failed: %v", err)
	}
	if !strings.HasPrefix(bad.Reason, "invalid JSON:") {
		t.Errorf("Reason = %q", bad.Reason)
	}
	// json error must be reachable via Unwrap chain.
	var jsonErr *json.UnmarshalTypeError
	if !errors.As(err, &jsonErr) {
		t.Errorf("expected underlying *json.UnmarshalTypeError; got %T", errors.Unwrap(err))
	}
	if bad.Raw == "" {
		t.Errorf("Raw should capture the input")
	}
}

func TestParseJSON_CapsRawInOversizedInput(t *testing.T) {
	t.Parallel()
	huge := strings.Repeat("x", 5000) + " { not json"
	_, err := ParseJSON[sampleShape](huge)
	if err == nil {
		t.Fatal("expected error")
	}
	var bad *BadOutputError
	if !errors.As(err, &bad) {
		t.Fatalf("errors.As failed: %v", err)
	}
	if len(bad.Raw) > 2100 {
		t.Errorf("Raw length = %d; should be capped", len(bad.Raw))
	}
	if !strings.Contains(bad.Raw, "truncated") {
		t.Errorf("expected truncation marker in Raw")
	}
}

func TestParseJSON_TrailingCommaIsRejected(t *testing.T) {
	// Intentional: we do NOT silently fix trailing commas. Document
	// the behavior so a future contributor doesn't add it on a whim.
	t.Parallel()
	_, err := ParseJSON[sampleShape](`{"name":"x","count":1,}`)
	if err == nil {
		t.Fatal("expected error on trailing comma — recovery is intentionally NOT supported")
	}
}

func TestBadOutputError_ErrorString(t *testing.T) {
	t.Parallel()
	cause := errors.New("unexpected end of JSON input")
	err := &BadOutputError{
		Provider: "google", Raw: "{partial", Reason: "invalid JSON", Cause: cause,
	}
	if got := err.Error(); got != "google: bad output: invalid JSON" {
		t.Errorf("Error() = %q", got)
	}
	if !errors.Is(err, cause) {
		t.Errorf("errors.Is(err, cause) = false")
	}
}

func TestBadOutputError_DefaultPrefix(t *testing.T) {
	t.Parallel()
	err := &BadOutputError{Reason: "trailing prose"}
	if got := err.Error(); got != "schema: bad output: trailing prose" {
		t.Errorf("Error() = %q", got)
	}
}

func TestBadOutputError_NilSafe(t *testing.T) {
	t.Parallel()
	var e *BadOutputError
	if got := e.Error(); got != "<nil>" {
		t.Errorf("nil BadOutputError.Error() = %q", got)
	}
}
