// Command structured-output shows provider.GenerateStructured: constrain
// a model's reply to a Go struct and get it back decoded. It uses a
// scripted provider so the example runs offline; swap it for anthropic /
// openai / google and the code is identical.
//
// Run with:
//
//	go run ./examples/structured-output
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// Recipe is the shape we want back. The json tags name the fields; the
// jsonschema tag adds a description the model sees.
type Recipe struct {
	Title       string   `json:"title" jsonschema:"the dish name"`
	Minutes     int      `json:"minutes" jsonschema:"total time in minutes"`
	Ingredients []string `json:"ingredients"`
}

func main() {
	ctx := context.Background()
	p := &scriptedProvider{reply: `{
		"title": "Buttermilk Pancakes",
		"minutes": 20,
		"ingredients": ["flour", "buttermilk", "egg", "butter", "baking powder"]
	}`}

	// One call: the schema is derived from Recipe, sent to the model, and
	// the reply is decoded back into a Recipe.
	recipe, err := provider.GenerateStructured[Recipe](ctx, p, provider.Request{
		Model:    "scripted-1",
		Messages: []schema.Message{schema.UserMessage("Give me a quick pancake recipe.")},
	})
	if err != nil {
		log.Fatalf("structured generate: %v", err)
	}

	fmt.Printf("%s — %d min\n", recipe.Title, recipe.Minutes)
	for _, ing := range recipe.Ingredients {
		fmt.Printf("  - %s\n", ing)
	}

	// The derived schema is also available on its own, e.g. to set
	// Request.ResponseFormat yourself or to inspect what the model saw.
	raw, _ := provider.JSONSchemaFor[Recipe]()
	fmt.Printf("\nschema sent to the model:\n%s\n", raw)
}

// scriptedProvider is a minimal Provider that returns a fixed reply. A
// real adapter (anthropic/openai/google) drops in unchanged — it reports
// StructuredOutput: true and honors Request.ResponseFormat.
type scriptedProvider struct {
	reply string
}

func (*scriptedProvider) Name() string { return "scripted" }
func (*scriptedProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{StructuredOutput: true}
}
func (*scriptedProvider) Stream(context.Context, provider.Request) (provider.StreamReader, error) {
	return nil, provider.ErrUnsupported
}

func (p *scriptedProvider) Generate(context.Context, provider.Request) (*provider.Response, error) {
	return &provider.Response{
		Message:    schema.AssistantMessage(p.reply),
		StopReason: schema.StopReasonEndTurn,
	}, nil
}
