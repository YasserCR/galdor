package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

type person struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

// TestGenerate_StructuredOutput verifies the json_schema → forced-tool
// translation: the request must carry a single tool whose input_schema is
// the requested schema, with tool_choice forcing it; and the tool_use
// reply must read back as the message text (the structured JSON).
func TestGenerate_StructuredOutput(t *testing.T) {
	t.Parallel()
	var gotReq messageRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotReq)
		w.Header().Set("content-type", "application/json")
		// The model "calls" the forced tool with the structured result.
		_, _ = io.WriteString(w, `{
			"id":"msg_1","type":"message","role":"assistant","model":"claude-haiku-4-5",
			"content":[{"type":"tool_use","id":"tu_1","name":"person","input":{"name":"Ada","age":36}}],
			"stop_reason":"tool_use",
			"usage":{"input_tokens":5,"output_tokens":7}
		}`)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)

	schemaBytes, err := provider.JSONSchemaFor[person]()
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.Generate(context.Background(), provider.Request{
		Model:    "claude-haiku-4-5",
		Messages: []schema.Message{schema.UserMessage("Ada is 36")},
		ResponseFormat: &provider.ResponseFormat{
			Type:   provider.ResponseFormatJSONSchema,
			Schema: schemaBytes,
			Name:   "person",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Request side: one forced tool named "person".
	if len(gotReq.Tools) != 1 || gotReq.Tools[0].Name != "person" {
		t.Fatalf("expected one forced tool 'person', got %+v", gotReq.Tools)
	}
	if gotReq.ToolChoice == nil || gotReq.ToolChoice.Type != "tool" || gotReq.ToolChoice.Name != "person" {
		t.Fatalf("tool_choice should force 'person': %+v", gotReq.ToolChoice)
	}

	// Response side: the tool input reads back as the message text.
	if resp.Message.Text() != `{"name":"Ada","age":36}` {
		t.Errorf("Message.Text() = %q, want the structured JSON", resp.Message.Text())
	}
	// And there are no leftover tool calls on the surfaced message.
	if len(resp.Message.ToolCalls) != 0 {
		t.Errorf("structured response should carry no tool calls, got %+v", resp.Message.ToolCalls)
	}
}

// TestGenerateStructured_EndToEnd drives the generic helper through the
// real adapter against a fake server.
func TestGenerateStructured_EndToEnd(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"msg_2","type":"message","role":"assistant","model":"m",
			"content":[{"type":"tool_use","id":"tu_2","name":"person","input":{"name":"Linus","age":54}}],
			"stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1}
		}`)
	}))
	defer srv.Close()

	p := newTestProvider(t, srv)
	got, err := provider.GenerateStructured[person](context.Background(), p, provider.Request{
		Model:    "claude-haiku-4-5",
		Messages: []schema.Message{schema.UserMessage("who?")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Linus" || got.Age != 54 {
		t.Errorf("decoded = %+v", got)
	}
}

func TestCapabilities_StructuredOutputEnabled(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t, httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})))
	if !p.Capabilities().StructuredOutput {
		t.Error("anthropic should report StructuredOutput = true")
	}
}
