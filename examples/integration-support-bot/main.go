// Command integration-support-bot demonstrates a real-world
// customer-support pattern: a routing supervisor delegates each
// incoming user message to one of three specialist agents
// (billing, technical, general), each with its own tool registry.
//
// What this exercises end-to-end:
//
//   - Supervisor pattern from pkg/council
//   - Per-specialist ReAct sub-agents with their own tools
//   - Cross-agent context passing through the supervisor's history
//   - Observability instrumentation around every LLM call
//   - SQLite trace store + steps view in the dashboard
//
// The Provider is scripted so the example runs offline and
// deterministically — no API key required. In a real deployment
// you swap it for anthropic.New(...) / openai.New(...); the rest
// of the wiring stays exactly the same.
//
// Run with:
//
//	go run ./examples/integration-support-bot
//
// Then inspect the trace:
//
//	galdor ui --db ./traces.db
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/YasserCR/galdor/pkg/agent"
	"github.com/YasserCR/galdor/pkg/council"
	"github.com/YasserCR/galdor/pkg/observability"
	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
	"github.com/YasserCR/galdor/pkg/tool"
)

const dbPath = "./traces.db"

func main() {
	ctx := context.Background()

	exporter, err := observability.NewSQLiteExporter(dbPath)
	if err != nil {
		log.Fatal(err)
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter))
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tp.Shutdown(shutCtx)
	}()
	tracer := tp.Tracer("integration-support-bot")

	prov := observability.InstrumentProvider(buildScriptedProvider(), tracer,
		observability.WithCaptureContent(true))

	billing := mustReAct(prov, buildBillingTools())
	technical := mustReAct(prov, buildTechnicalTools())
	general := mustReAct(prov, buildGeneralTools())

	supervisor, err := council.NewSupervisor(council.SupervisorConfig{
		Provider: prov,
		Model:    "scripted-1",
		Workers: []council.Worker{
			{Name: "billing", Description: "handles invoices, refunds, charges, subscription questions",
				Run: workerRunner(billing)},
			{Name: "technical", Description: "handles bugs, outages, login issues, system status",
				Run: workerRunner(technical)},
			{Name: "general", Description: "handles FAQs, hours, policies — anything not billing or technical",
				Run: workerRunner(general)},
		},
		MaxHops: 4,
	})
	if err != nil {
		log.Fatal(err)
	}

	scenarios := []string{
		"My last invoice charged me twice for the same item. Can you check?",
		"The login page returns 500 every time I try.",
		"What are your opening hours on Sundays?",
	}
	for i, q := range scenarios {
		fmt.Printf("\n=== scenario %d ===\n", i+1)
		fmt.Printf("user: %s\n", q)
		ctx2, span := tracer.Start(ctx, "integration-support-bot.scenario")
		final, err := supervisor.Invoke(ctx2, council.SupervisorState{Input: q})
		span.End()
		if err != nil {
			fmt.Printf("  err: %v\n", err)
			continue
		}
		fmt.Println("  routing history:")
		for j, h := range final.History {
			fmt.Printf("    %d. [%s] task=%q -> %s\n", j+1, h.Worker, h.Task, truncate(h.Output, 80))
		}
		fmt.Printf("  bot: %s\n", truncate(final.Final, 120))
	}

	if err := tp.ForceFlush(ctx); err != nil {
		log.Printf("flush: %v", err)
	}
	fmt.Println()
	fmt.Println("Traces stored in:", dbPath)
	fmt.Println("  galdor ui --db", dbPath)
}

// mustReAct builds a single ReAct specialist sharing the supervisor's
// provider but with its own tool set.
type runnableReAct = interface {
	Invoke(context.Context, agent.State) (agent.State, error)
}

func mustReAct(p provider.Provider, tools *tool.Registry) runnableReAct {
	r, err := agent.NewReAct(agent.Config{
		Provider:      p,
		Tools:         tools,
		Model:         "scripted-1",
		MaxIterations: 4,
	})
	if err != nil {
		log.Fatal(err)
	}
	return r
}

// workerRunner wraps a ReAct specialist behind the council.Worker
// signature. The supervisor only sees a single string in / single
// string out — the ReAct loop happens inside.
func workerRunner(r runnableReAct) func(context.Context, string) (string, error) {
	return func(ctx context.Context, task string) (string, error) {
		final, err := r.Invoke(ctx, agent.State{
			Messages: []schema.Message{schema.UserMessage(task)},
		})
		if err != nil {
			return "", err
		}
		return final.FinalText, nil
	}
}

// ----- specialist tool registries ------------------------------------------

type invoiceLookupIn struct {
	InvoiceID string `json:"invoice_id" jsonschema:"required, the invoice ID to look up"`
}
type invoiceLookupOut struct {
	Customer  string  `json:"customer"`
	Amount    float64 `json:"amount"`
	Duplicate bool    `json:"duplicate"`
}

type refundIn struct {
	InvoiceID string `json:"invoice_id"`
	Reason    string `json:"reason"`
}
type refundOut struct {
	RefundID string `json:"refund_id"`
}

func buildBillingTools() *tool.Registry {
	lookup := tool.MustNewTool("lookup_invoice", "Look up an invoice by ID",
		func(_ context.Context, in invoiceLookupIn) (invoiceLookupOut, error) {
			dup := (len(in.InvoiceID) % 2) == 0
			return invoiceLookupOut{Customer: "ACME Co.", Amount: 42.50, Duplicate: dup}, nil
		})
	refund := tool.MustNewTool("issue_refund", "Issue a refund for an invoice",
		func(_ context.Context, in refundIn) (refundOut, error) {
			return refundOut{RefundID: "RF-" + in.InvoiceID}, nil
		})
	reg, err := tool.NewRegistry(lookup, refund)
	if err != nil {
		log.Fatal(err)
	}
	return reg
}

type ticketStatusIn struct {
	TicketID string `json:"ticket_id"`
}
type ticketStatusOut struct {
	Status   string `json:"status"`
	Summary  string `json:"summary"`
	Priority string `json:"priority"`
}

type createTicketIn struct {
	Title    string `json:"title"`
	Severity string `json:"severity"`
}
type createTicketOut struct {
	TicketID string `json:"ticket_id"`
}

func buildTechnicalTools() *tool.Registry {
	status := tool.MustNewTool("ticket_status", "Look up the status of a support ticket",
		func(_ context.Context, _ ticketStatusIn) (ticketStatusOut, error) {
			return ticketStatusOut{
				Status:   "investigating",
				Summary:  "Login outage affecting US-East",
				Priority: "P1",
			}, nil
		})
	create := tool.MustNewTool("create_ticket", "Create a new support ticket",
		func(_ context.Context, _ createTicketIn) (createTicketOut, error) {
			return createTicketOut{TicketID: "TKT-" + fmt.Sprint(time.Now().Unix())}, nil
		})
	reg, err := tool.NewRegistry(status, create)
	if err != nil {
		log.Fatal(err)
	}
	return reg
}

type faqIn struct {
	Topic string `json:"topic"`
}
type faqOut struct {
	Answer string `json:"answer"`
}

func buildGeneralTools() *tool.Registry {
	faq := tool.MustNewTool("faq_search", "Search the company FAQ",
		func(_ context.Context, in faqIn) (faqOut, error) {
			kb := map[string]string{
				"hours":   "We are open Mon-Sat, 9am-6pm. Closed Sundays.",
				"refund":  "Refunds within 30 days, no questions asked.",
				"contact": "support@example.com or +1-555-0100.",
			}
			for k, v := range kb {
				if strings.Contains(strings.ToLower(in.Topic), k) {
					return faqOut{Answer: v}, nil
				}
			}
			return faqOut{Answer: "No FAQ entry matched."}, nil
		})
	reg, err := tool.NewRegistry(faq)
	if err != nil {
		log.Fatal(err)
	}
	return reg
}

// ----- scripted provider ---------------------------------------------------
//
// The provider is what makes this run offline. It branches on the
// system prompt: if it sees the supervisor's "routing supervisor"
// header it returns a routing JSON; otherwise it's a specialist
// ReAct turn and either emits a tool call or a final text based
// on whether a tool result is already in the conversation.

type scriptedProvider struct {
	calls atomic.Int32
}

func buildScriptedProvider() *scriptedProvider { return &scriptedProvider{} }

func (*scriptedProvider) Name() string { return "scripted" }
func (*scriptedProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{ToolCalling: true}
}
func (*scriptedProvider) Stream(_ context.Context, _ provider.Request) (provider.StreamReader, error) {
	return nil, provider.ErrUnsupported
}

func (p *scriptedProvider) Generate(_ context.Context, req provider.Request) (*provider.Response, error) {
	p.calls.Add(1)

	var sys, lastUser string
	for _, m := range req.Messages {
		if m.Role == schema.RoleSystem && sys == "" {
			sys = m.Text()
		}
		if m.Role == schema.RoleUser {
			lastUser = m.Text()
		}
	}

	if strings.Contains(sys, "routing supervisor") {
		return supervisorReply(lastUser), nil
	}
	return specialistReply(req, lastUser), nil
}

// supervisorReply emits a routing decision or a final answer
// depending on whether work has been done yet. The supervisor's
// user payload always contains a "Work completed so far:" block.
func supervisorReply(payload string) *provider.Response {
	// First call: no work done yet → route based on the user request.
	// Subsequent call: work done → finish with a summary.
	if !strings.Contains(payload, "(none)") {
		final := "Done. " + extractFirstWorkerOutput(payload)
		raw, _ := json.Marshal(map[string]string{"final": final})
		return jsonReply(string(raw))
	}
	// Look ONLY at the "User request:" block, not the whole
	// payload — otherwise our keyword match collides with the
	// workers' descriptions (which mention "refund", "login", etc).
	q := strings.ToLower(extractUserRequest(payload))
	switch {
	case strings.Contains(q, "invoice") || strings.Contains(q, "refund") || strings.Contains(q, "charged"):
		return jsonReply(`{"worker":"billing","task":"investigate the customer's billing concern"}`)
	case strings.Contains(q, "login") || strings.Contains(q, "500") || strings.Contains(q, "bug") || strings.Contains(q, "outage"):
		return jsonReply(`{"worker":"technical","task":"diagnose and resolve the technical issue"}`)
	default:
		return jsonReply(`{"worker":"general","task":"answer the customer's question from the FAQ"}`)
	}
}

// extractUserRequest pulls out the body of the "User request:"
// section from the supervisor's payload — everything between that
// header and the next blank line.
func extractUserRequest(payload string) string {
	const header = "User request:"
	i := strings.Index(payload, header)
	if i < 0 {
		return payload
	}
	rest := payload[i+len(header):]
	// The block ends at the blank line before "Work completed".
	if end := strings.Index(rest, "\n\n"); end >= 0 {
		return strings.TrimSpace(rest[:end])
	}
	return strings.TrimSpace(rest)
}

// specialistReply: first turn emits a tool call; second turn (after
// a tool result is in the conversation) emits a final answer that
// summarizes the tool output.
func specialistReply(req provider.Request, task string) *provider.Response {
	var toolText string
	for _, m := range req.Messages {
		if m.Role == schema.RoleTool {
			toolText = m.Text()
		}
	}
	if toolText != "" {
		return assistantReply("Based on the tool result: " + truncate(toolText, 120))
	}

	t := strings.ToLower(task)
	switch {
	case strings.Contains(t, "billing") || strings.Contains(t, "invoice") || strings.Contains(t, "refund"):
		return toolCallReply("lookup_invoice", map[string]string{"invoice_id": "INV-12345"})
	case strings.Contains(t, "technical") || strings.Contains(t, "ticket") || strings.Contains(t, "login") || strings.Contains(t, "outage"):
		return toolCallReply("ticket_status", map[string]string{"ticket_id": "TKT-99"})
	default:
		return toolCallReply("faq_search", map[string]string{"topic": "hours"})
	}
}

// extractFirstWorkerOutput pulls the first worker's response text
// out of the supervisor's history payload, for the "Done. <text>"
// final answer.
func extractFirstWorkerOutput(payload string) string {
	if i := strings.Index(payload, "1. ["); i >= 0 {
		rest := payload[i:]
		if arrow := strings.Index(rest, "-> "); arrow >= 0 {
			tail := rest[arrow+3:]
			if nl := strings.IndexByte(tail, '\n'); nl >= 0 {
				return strings.TrimSpace(tail[:nl])
			}
			return strings.TrimSpace(tail)
		}
	}
	return "see worker history above"
}

func jsonReply(body string) *provider.Response {
	return &provider.Response{
		Message:    schema.AssistantMessage(body),
		StopReason: schema.StopReasonEndTurn,
		Model:      "scripted-1",
	}
}

func assistantReply(text string) *provider.Response {
	return &provider.Response{
		Message:    schema.AssistantMessage(text),
		StopReason: schema.StopReasonEndTurn,
		Model:      "scripted-1",
	}
}

func toolCallReply(name string, args any) *provider.Response {
	raw, _ := json.Marshal(args)
	return &provider.Response{
		Message: schema.Message{
			Role: schema.RoleAssistant,
			ToolCalls: []schema.ToolCall{{
				ID: "c-" + name, Name: name, Arguments: raw,
			}},
		},
		StopReason: schema.StopReasonToolUse,
		Model:      "scripted-1",
	}
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "..."
}
