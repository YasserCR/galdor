package provider_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

type Recipe struct {
	Title       string   `json:"title" jsonschema:"the dish name"`
	Minutes     int      `json:"minutes"`
	Ingredients []string `json:"ingredients"`
}

// captureProvider records the last Request it received and returns a
// fixed reply, so the structured helpers can be tested without a network.
type captureProvider struct {
	reply string
	last  provider.Request
}

func (p *captureProvider) Name() string { return "capture" }
func (p *captureProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{StructuredOutput: true}
}
func (p *captureProvider) Stream(context.Context, provider.Request) (provider.StreamReader, error) {
	return nil, provider.ErrUnsupported
}
func (p *captureProvider) Generate(_ context.Context, req provider.Request) (*provider.Response, error) {
	p.last = req
	return &provider.Response{Message: schema.AssistantMessage(p.reply)}, nil
}

func TestJSONSchemaFor(t *testing.T) {
	t.Parallel()
	raw, err := provider.JSONSchemaFor[Recipe]()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["type"] != "object" {
		t.Errorf("type = %v", doc["type"])
	}
	// Closed object — strict structured-output modes need this.
	if doc["additionalProperties"] != false {
		t.Errorf("additionalProperties = %v, want false", doc["additionalProperties"])
	}
	props, _ := doc["properties"].(map[string]any)
	if _, ok := props["title"]; !ok {
		t.Errorf("missing title property: %v", props)
	}
	// The description tag flows through.
	if title, _ := props["title"].(map[string]any); title["description"] != "the dish name" {
		t.Errorf("title description = %v", title["description"])
	}
}

func TestGenerateStructured_FillsResponseFormatAndParses(t *testing.T) {
	t.Parallel()
	p := &captureProvider{reply: `{"title":"Pancakes","minutes":20,"ingredients":["flour","milk","egg"]}`}

	got, err := provider.GenerateStructured[Recipe](context.Background(), p, provider.Request{
		Model:    "m",
		Messages: []schema.Message{schema.UserMessage("a pancake recipe")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Pancakes" || got.Minutes != 20 || len(got.Ingredients) != 3 {
		t.Errorf("decoded = %+v", got)
	}

	// The helper set ResponseFormat from T.
	rf := p.last.ResponseFormat
	if rf == nil || rf.Type != provider.ResponseFormatJSONSchema {
		t.Fatalf("ResponseFormat not set as json_schema: %+v", rf)
	}
	if rf.Name != "Recipe" {
		t.Errorf("schema name = %q, want Recipe", rf.Name)
	}
	if len(rf.Schema) == 0 {
		t.Error("schema bytes empty")
	}
}

func TestGenerateStructured_ToleratesFencedJSON(t *testing.T) {
	t.Parallel()
	// A model that wraps the JSON in a markdown fence still decodes.
	p := &captureProvider{reply: "```json\n{\"title\":\"X\",\"minutes\":1,\"ingredients\":[]}\n```"}
	got, err := provider.GenerateStructured[Recipe](context.Background(), p, provider.Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "X" {
		t.Errorf("decoded = %+v", got)
	}
}

func TestGenerateStructured_RespectsPresetResponseFormat(t *testing.T) {
	t.Parallel()
	p := &captureProvider{reply: `{"title":"Y","minutes":2,"ingredients":[]}`}
	preset := &provider.ResponseFormat{Type: provider.ResponseFormatJSONObject}
	_, err := provider.GenerateStructured[Recipe](context.Background(), p, provider.Request{
		Model:          "m",
		ResponseFormat: preset,
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.last.ResponseFormat != preset {
		t.Error("a preset ResponseFormat must be left untouched")
	}
}

func TestGenerateStructured_BadJSONErrors(t *testing.T) {
	t.Parallel()
	p := &captureProvider{reply: "I cannot help with that."}
	_, err := provider.GenerateStructured[Recipe](context.Background(), p, provider.Request{Model: "m"})
	if err == nil {
		t.Fatal("non-JSON reply should error")
	}
}
