package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/YasserCR/galdor/internal/store"
	"github.com/YasserCR/galdor/pkg/observability"
	"github.com/YasserCR/galdor/pkg/schema"
)

// handleRunSteps serves the linear "time-travel" view at
// /runs/{runID}/steps. Each galdor.graph.node span becomes one
// step; provider + tool spans nested under that node are unfolded
// into the step's details (prompts, completions, tool I/O sizes).
//
// This complements the span tree on /runs/{runID}: the tree shows
// the structural shape, the steps view walks the conversation as
// a sequence of "what happened, then what, then what."
func (s *Server) handleRunSteps(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
	if runID == "" {
		http.NotFound(w, r)
		return
	}
	spans, err := s.store.SpansForRun(r.Context(), runID)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "load spans", err)
		return
	}
	if len(spans) == 0 {
		s.renderError(w, http.StatusNotFound, "run not found",
			fmt.Errorf("no spans recorded for run %q", runID))
		return
	}
	view := buildStepsView(spans, runID)
	s.renderTemplate(w, "steps.html", view)
}

// stepsPageData is the template contract for steps.html.
type stepsPageData struct {
	DBPath        string
	RunID         string
	Steps         []stepView
	HasCaptured   bool // whether at least one provider call has captured content
	ReplayHint    string
}

// stepView is one row in the time-travel view: a graph node and
// everything that happened inside it.
type stepView struct {
	Index    int
	SpanID   string
	NodeName string
	Status   string
	StatusOK bool
	Duration string
	Provider []providerCallView
	Tools    []toolCallView
	// Raw extras (anything that wasn't a recognized child) shown
	// at the end of the step for completeness.
	Extras []spanExtra
}

type providerCallView struct {
	SpanID         string
	Model          string
	Duration       string
	InputTokens    int
	OutputTokens   int
	Prompt         []renderedTurn
	Completion     *renderedTurn
	HasCaptured    bool
	Status         string
	StatusOK       bool
}

// renderedTurn is a single conversation turn formatted for display.
// Role goes on the left badge, Text on the right. Tool calls and
// tool results are folded into Text with a small prefix so the
// template doesn't need to know about all the shapes.
type renderedTurn struct {
	Role string
	Text string
}

type toolCallView struct {
	SpanID     string
	Name       string
	Duration   string
	InputSize  int
	OutputSize int
	Status     string
	StatusOK   bool
}

// buildStepsView linearizes the run's spans into step views.
// Steps are the graph.node spans; everything else (provider, tool)
// is folded into the closest ancestor node.
func buildStepsView(spans []store.Span, runID string) stepsPageData {
	view := stepsPageData{RunID: runID}

	// Build parent->children + by-ID indices.
	byID := make(map[string]store.Span, len(spans))
	children := map[string][]string{}
	var nodeSpans []store.Span
	for _, sp := range spans {
		byID[sp.SpanID] = sp
		children[sp.ParentSpanID] = append(children[sp.ParentSpanID], sp.SpanID)
		if sp.Name == "galdor.graph.node" {
			nodeSpans = append(nodeSpans, sp)
		}
	}

	// Order children chronologically so the unfolded view reads
	// in the same direction the run executed.
	for k, kids := range children {
		ks := kids
		sort.SliceStable(ks, func(i, j int) bool {
			return byID[ks[i]].StartTimeUnixNano < byID[ks[j]].StartTimeUnixNano
		})
		children[k] = ks
	}
	sort.SliceStable(nodeSpans, func(i, j int) bool {
		return nodeSpans[i].StartTimeUnixNano < nodeSpans[j].StartTimeUnixNano
	})

	for i, n := range nodeSpans {
		step := stepView{
			Index:    i + 1,
			SpanID:   n.SpanID,
			NodeName: stringAttr(n.Attributes, "galdor.node.name"),
			Status:   n.StatusCode,
			StatusOK: n.StatusCode == "" || n.StatusCode == "ok",
			Duration: formatDuration(n.Duration()),
		}
		if step.NodeName == "" {
			step.NodeName = n.Name
		}

		// Walk all descendants of this node and bucket them.
		var walk func(id string)
		walk = func(id string) {
			for _, childID := range children[id] {
				child := byID[childID]
				switch child.Name {
				case observability.SpanProviderGenerate:
					step.Provider = append(step.Provider, providerCallToView(child))
				case observability.SpanToolExecute:
					step.Tools = append(step.Tools, toolCallToView(child))
				}
				walk(childID)
			}
		}
		walk(n.SpanID)

		step.Extras = extractExtras(n)
		view.Steps = append(view.Steps, step)

		for _, pc := range step.Provider {
			if pc.HasCaptured {
				view.HasCaptured = true
			}
		}
	}

	if !view.HasCaptured {
		view.ReplayHint = "This run was recorded without prompt/completion capture. " +
			"Re-run with observability.WithCaptureContent(true) to enable replay and to see prompts/completions here."
	}
	return view
}

func providerCallToView(sp store.Span) providerCallView {
	out := providerCallView{
		SpanID:       sp.SpanID,
		Model:        firstNonEmpty(stringAttr(sp.Attributes, observability.AttrGenAIResponseModel), stringAttr(sp.Attributes, observability.AttrGenAIRequestModel)),
		Duration:     formatDuration(sp.Duration()),
		InputTokens:  intAttr(sp.Attributes, observability.AttrGenAIUsageInputTokens),
		OutputTokens: intAttr(sp.Attributes, observability.AttrGenAIUsageOutputTokens),
		Status:       sp.StatusCode,
		StatusOK:     sp.StatusCode == "" || sp.StatusCode == "ok",
	}
	if raw := stringAttr(sp.Attributes, observability.AttrGenAIPrompt); raw != "" {
		out.Prompt = decodePrompt(raw)
		out.HasCaptured = true
	}
	if raw := stringAttr(sp.Attributes, observability.AttrGenAICompletion); raw != "" {
		t := decodeCompletion(raw)
		out.Completion = &t
		out.HasCaptured = true
	}
	return out
}

func toolCallToView(sp store.Span) toolCallView {
	return toolCallView{
		SpanID:     sp.SpanID,
		Name:       stringAttr(sp.Attributes, observability.AttrGenAIToolName),
		Duration:   formatDuration(sp.Duration()),
		InputSize:  intAttr(sp.Attributes, observability.AttrGenAIToolInputSize),
		OutputSize: intAttr(sp.Attributes, observability.AttrGenAIToolOutputSize),
		Status:     sp.StatusCode,
		StatusOK:   sp.StatusCode == "" || sp.StatusCode == "ok",
	}
}

// decodePrompt parses the JSON-encoded []schema.Message that lives
// under gen_ai.prompt and returns one renderedTurn per message.
func decodePrompt(raw string) []renderedTurn {
	var msgs []schema.Message
	if err := json.Unmarshal([]byte(raw), &msgs); err != nil {
		return []renderedTurn{{Role: "raw", Text: raw}}
	}
	out := make([]renderedTurn, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, renderTurn(m))
	}
	return out
}

// decodeCompletion parses the JSON-encoded schema.Message that
// lives under gen_ai.completion.
func decodeCompletion(raw string) renderedTurn {
	var m schema.Message
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return renderedTurn{Role: "raw", Text: raw}
	}
	return renderTurn(m)
}

// renderTurn folds a message — including tool calls and tool
// results — into a single (role, text) tuple. Tool calls show as
// "→ tool_name(arguments)" lines; tool results show as
// "← result for <id>".
func renderTurn(m schema.Message) renderedTurn {
	var b strings.Builder
	if t := m.Text(); t != "" {
		b.WriteString(t)
	}
	for _, tc := range m.ToolCalls {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		args := string(tc.Arguments)
		if args == "" {
			args = "{}"
		}
		fmt.Fprintf(&b, "→ %s(%s)", tc.Name, args)
	}
	if m.ToolCallID != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "← result for %s", m.ToolCallID)
	}
	return renderedTurn{Role: string(m.Role), Text: b.String()}
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// stringAttr safely fetches a string attribute by key. Returns ""
// when missing or not a string.
func stringAttr(attrs map[string]any, key string) string {
	v, ok := attrs[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// intAttr safely fetches an int-ish attribute. JSON numbers decode
// as float64 through the store layer; we tolerate both.
func intAttr(attrs map[string]any, key string) int {
	v, ok := attrs[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}
