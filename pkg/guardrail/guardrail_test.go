package guardrail

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/YasserCR/galdor/pkg/schema"
	"github.com/YasserCR/galdor/pkg/testprovider"
)

func TestCheckInput_NoGuardsPasses(t *testing.T) {
	t.Parallel()
	if err := CheckInput(context.Background(), nil, schema.UserMessage("hi")); err != nil {
		t.Fatalf("nil guards must pass, got %v", err)
	}
}

func TestCheckInput_FirstVetoWins(t *testing.T) {
	t.Parallel()
	boom := errors.New("pii")
	var order []string
	guards := []InputGuard{
		InputGuardFunc{ID: "a", Check: func(_ context.Context, _ schema.Message) error {
			order = append(order, "a")
			return boom
		}},
		InputGuardFunc{ID: "b", Check: func(_ context.Context, _ schema.Message) error {
			order = append(order, "b")
			return nil
		}},
	}
	err := CheckInput(context.Background(), guards, schema.UserMessage("hi"))
	if err == nil {
		t.Fatal("expected a block")
	}
	if len(order) != 1 || order[0] != "a" {
		t.Errorf("guards run after a veto: %v", order)
	}
	if !errors.Is(err, ErrBlocked) {
		t.Errorf("err must match ErrBlocked: %v", err)
	}
	if !errors.Is(err, boom) {
		t.Errorf("err must wrap the guard's error: %v", err)
	}
	var block *BlockError
	if !errors.As(err, &block) {
		t.Fatalf("err must be a *BlockError: %T", err)
	}
	if block.Guard != "a" {
		t.Errorf("BlockError.Guard = %q, want a", block.Guard)
	}
}

func TestCheckInput_SkipsNilGuards(t *testing.T) {
	t.Parallel()
	guards := []InputGuard{nil, InputGuardFunc{ID: "ok", Check: func(_ context.Context, _ schema.Message) error { return nil }}}
	if err := CheckInput(context.Background(), guards, schema.UserMessage("hi")); err != nil {
		t.Fatalf("nil guards must be skipped: %v", err)
	}
}

func TestCheckOutput_BlockMatchesSentinel(t *testing.T) {
	t.Parallel()
	guards := []OutputGuard{
		OutputGuardFunc{ID: "bad-words", Check: func(_ context.Context, m schema.Message) error {
			return errors.New("refusal")
		}},
	}
	err := CheckOutput(context.Background(), guards, schema.AssistantMessage("I'm sorry"))
	if !errors.Is(err, ErrBlocked) {
		t.Errorf("err must match ErrBlocked: %v", err)
	}
}

func TestKindOf_DefaultsToDeterministic(t *testing.T) {
	t.Parallel()
	g := InputGuardFunc{ID: "regex", Check: func(_ context.Context, _ schema.Message) error { return nil }}
	if got := KindOf(g); got != KindDeterministic {
		t.Errorf("KindOf(func guard) = %v, want deterministic", got)
	}
}

func TestKindOf_LLMJudgeIsLLM(t *testing.T) {
	t.Parallel()
	if got := KindOf(LLMJudge{}); got != KindLLM {
		t.Errorf("KindOf(LLMJudge) = %v, want llm", got)
	}
}

func TestBlockError_CarriesGuardKind(t *testing.T) {
	t.Parallel()
	guards := []OutputGuard{LLMJudge{Provider: testprovider.New(testprovider.Responses("BLOCK")), Model: "x"}}
	err := CheckOutput(context.Background(), guards, schema.AssistantMessage("secret"))
	var block *BlockError
	if !errors.As(err, &block) {
		t.Fatalf("err must be a *BlockError, got %T", err)
	}
	if block.Kind != KindLLM {
		t.Errorf("BlockError.Kind = %v, want llm", block.Kind)
	}
	if block.Guard != "llm_judge" {
		t.Errorf("BlockError.Guard = %q, want llm_judge", block.Guard)
	}
}

func TestLLMJudge_AllowsOnAllow(t *testing.T) {
	t.Parallel()
	j := LLMJudge{Provider: testprovider.New(testprovider.Responses("ALLOW")), Model: "x"}
	if err := j.CheckInput(context.Background(), schema.UserMessage("hello")); err != nil {
		t.Fatalf("ALLOW must pass, got %v", err)
	}
}

func TestLLMJudge_BlocksOnBlock(t *testing.T) {
	t.Parallel()
	j := LLMJudge{Provider: testprovider.New(testprovider.Responses("BLOCK")), Model: "x"}
	err := j.CheckOutput(context.Background(), schema.AssistantMessage("secret"))
	if !errors.Is(err, ErrJudgeBlocked) {
		t.Fatalf("err must match ErrJudgeBlocked, got %v", err)
	}
}

func TestLLMJudge_FailsClosedOnUnparseableReply(t *testing.T) {
	t.Parallel()
	j := LLMJudge{Provider: testprovider.New(testprovider.Responses("maybe?")), Model: "x"}
	err := j.CheckInput(context.Background(), schema.UserMessage("hello"))
	if !errors.Is(err, ErrJudgeBlocked) {
		t.Fatalf("unparseable reply must fail closed (ErrJudgeBlocked), got %v", err)
	}
}

func TestLLMJudge_RejectsNilProvider(t *testing.T) {
	t.Parallel()
	j := LLMJudge{Model: "x"}
	if err := j.CheckInput(context.Background(), schema.UserMessage("hi")); err == nil {
		t.Fatal("expected error for nil Provider")
	}
}

func TestClassifyJudgeReply(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"ALLOW", "ALLOW"},
		{"allow", "ALLOW"},
		{"BLOCK", "BLOCK"},
		{"Block.", "BLOCK"},
		{"This should be BLOCKED", "BLOCK"},
		{"I say allow it", "ALLOW"},
		{"It should be allowed", "ALLOW"},
		{"ALLOW and BLOCK", ""}, // ambiguous
		{"maybe", ""},
		{"", ""},
		// Negated allows must never fail open.
		{"Not allowed.", ""},
		{"Do not allow this", ""},
		{"Don't allow it", ""},
		{"This cannot be allowed", ""},
		// Block synonyms the judge may reach for.
		{"Disallowed", "BLOCK"},
		{"I disallow this", "BLOCK"},
		{"Denied", "BLOCK"},
		{"Deny it", "BLOCK"},
	}
	for _, c := range cases {
		if got := classifyJudgeReply(c.in); got != c.want {
			t.Errorf("classifyJudgeReply(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestKind_String(t *testing.T) {
	t.Parallel()
	if got := KindDeterministic.String(); got != "deterministic" {
		t.Errorf("KindDeterministic.String() = %q", got)
	}
	if got := KindLLM.String(); got != "llm" {
		t.Errorf("KindLLM.String() = %q", got)
	}
}

func TestBlockError_Error(t *testing.T) {
	t.Parallel()
	withErr := &BlockError{Guard: "g", Err: errors.New("nope")}
	if got := withErr.Error(); got != "guardrail: g: nope" {
		t.Errorf("Error() = %q", got)
	}
	nilErr := &BlockError{Guard: "g"}
	if got := nilErr.Error(); got != "guardrail: g blocked" {
		t.Errorf("Error() with nil Err = %q", got)
	}
}

func TestCheckOutput_NoGuardsPasses(t *testing.T) {
	t.Parallel()
	if err := CheckOutput(context.Background(), nil, schema.AssistantMessage("ok")); err != nil {
		t.Fatalf("nil guards must pass, got %v", err)
	}
}

func TestLLMJudge_NameOverride(t *testing.T) {
	t.Parallel()
	j := LLMJudge{NameOverride: "guard_toxicity"}
	if got := j.Name(); got != "guard_toxicity" {
		t.Errorf("Name() = %q, want guard_toxicity", got)
	}
	if got := (LLMJudge{}).Name(); got != "llm_judge" {
		t.Errorf("default Name() = %q, want llm_judge", got)
	}
}

func TestLLMJudge_RejectsEmptyModel(t *testing.T) {
	t.Parallel()
	j := LLMJudge{Provider: testprovider.New(testprovider.Responses("ALLOW"))}
	if err := j.CheckInput(context.Background(), schema.UserMessage("hi")); err == nil {
		t.Fatal("expected error for empty Model")
	}
}

func TestLLMJudge_PropagatesProviderError(t *testing.T) {
	t.Parallel()
	boom := errors.New("judge down")
	j := LLMJudge{
		Provider: testprovider.New(testprovider.Errors(boom)),
		Model:    "x",
	}
	err := j.CheckInput(context.Background(), schema.UserMessage("hi"))
	if !errors.Is(err, boom) {
		t.Fatalf("err must wrap the provider error, got %v", err)
	}
	if !errors.Is(err, ErrEvalFailed) {
		t.Fatalf("provider errors must match ErrEvalFailed, got %v", err)
	}
}

func TestCheckInput_ProviderErrorIsNotABlock(t *testing.T) {
	t.Parallel()
	boom := errors.New("judge down")
	guards := []InputGuard{LLMJudge{
		Provider:     testprovider.New(testprovider.Errors(boom)),
		Model:        "x",
		NameOverride: "guard_pii",
	}}
	err := CheckInput(context.Background(), guards, schema.UserMessage("hi"))
	if err == nil {
		t.Fatal("a failed evaluation must still fail the run")
	}
	if errors.Is(err, ErrBlocked) {
		t.Errorf("an infrastructure failure must not match ErrBlocked: %v", err)
	}
	if !errors.Is(err, ErrEvalFailed) {
		t.Errorf("err must match ErrEvalFailed: %v", err)
	}
	if !errors.Is(err, boom) {
		t.Errorf("err must wrap the provider error: %v", err)
	}
	if !strings.Contains(err.Error(), "guard_pii") {
		t.Errorf("err must carry the guard's NameOverride, got %q", err.Error())
	}
}

func TestLLMJudge_SkipsMessagesWithNothingToJudge(t *testing.T) {
	t.Parallel()
	// The provider would error on any call; an empty message must not
	// reach it at all.
	j := LLMJudge{
		Provider: testprovider.New(testprovider.Errors(errors.New("must not be called"))),
		Model:    "x",
	}
	if err := j.CheckOutput(context.Background(), schema.AssistantMessage("")); err != nil {
		t.Fatalf("a message with nothing to judge must pass without a model call, got %v", err)
	}
}

func TestLLMJudge_JudgesToolCallArguments(t *testing.T) {
	t.Parallel()
	p := testprovider.New(testprovider.Responses("BLOCK"))
	j := LLMJudge{Provider: p, Model: "x"}
	msg := schema.Message{
		Role: schema.RoleAssistant,
		ToolCalls: []schema.ToolCall{
			{ID: "1", Name: "send_email", Arguments: []byte(`{"body":"card 4111"}`)},
		},
	}
	err := j.CheckOutput(context.Background(), msg)
	if !errors.Is(err, ErrJudgeBlocked) {
		t.Fatalf("tool-call-only message must be judged, got %v", err)
	}
	reqs := p.Requests()
	if len(reqs) != 1 {
		t.Fatalf("judge calls = %d, want 1", len(reqs))
	}
	subject := reqs[0].Messages[len(reqs[0].Messages)-1].Text()
	if !strings.Contains(subject, "send_email") || !strings.Contains(subject, "card 4111") {
		t.Errorf("judge subject must include the tool call name and arguments, got %q", subject)
	}
}
