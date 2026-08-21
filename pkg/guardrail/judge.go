package guardrail

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/YasserCR/galdor/internal/judgecall"
	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/schema"
)

// ErrJudgeBlocked is the underlying error when an LLMJudge votes BLOCK,
// or when the judge's reply cannot be parsed and the guard fails closed.
// It is surfaced inside a *BlockError, so errors.Is(err, ErrBlocked)
// still reports true.
var ErrJudgeBlocked = errors.New("llm judge blocked")

// LLMJudge is a guard that uses a second LLM to decide whether a message
// is allowed. It implements both InputGuard and OutputGuard, so a single
// instance can sit in either Config slice. The judge is prompted to reply
// with exactly ALLOW or BLOCK; BLOCK vetoes the message.
//
// LLMJudge is intentionally a thin wrapper: callers swap the Provider /
// Model independently from the agent under guard, so the judge can be a
// cheaper (or stronger) model than the one doing the work.
//
// The judge sees the message's content parts verbatim (a vision-capable
// judge model can vet non-text parts too) plus a textual rendering of any
// tool calls, which live outside Content and would otherwise escape the
// check. A message with nothing to judge — no content parts and no tool
// calls — is allowed without a model call.
//
// A judge that returns anything other than a clear ALLOW or BLOCK fails
// closed (the message is blocked) — for a guardrail, blocking on an
// ambiguous judge is safer than letting unvetted content through. A
// judge whose provider call fails also fails the run, but with an error
// matching ErrEvalFailed rather than ErrBlocked: an outage is not a
// policy verdict.
type LLMJudge struct {
	// Provider serves the judge LLM. Required.
	Provider provider.Provider

	// Model is the judge model ID. Required.
	Model string

	// Rule describes the policy to enforce, embedded verbatim in the
	// judge's system prompt. Keep it tight and specific: "block any
	// message that reveals a credit-card number" beats "be strict".
	Rule string

	// NameOverride lets callers distinguish multiple LLMJudge guards in
	// the same agent (e.g. "guard_toxicity" vs "guard_pii"). When empty,
	// Name() returns "llm_judge".
	NameOverride string

	// MaxTokens caps the judge's reply length. Default 16 — enough for
	// "ALLOW" / "BLOCK".
	MaxTokens int
}

// Name implements Guard.
func (j LLMJudge) Name() string {
	if j.NameOverride != "" {
		return j.NameOverride
	}
	return "llm_judge"
}

// Kind implements Kinded: an LLMJudge evaluates via a model call.
func (j LLMJudge) Kind() Kind { return KindLLM }

// CheckInput implements InputGuard.
func (j LLMJudge) CheckInput(ctx context.Context, msg schema.Message) error {
	return j.judge(ctx, msg)
}

// CheckOutput implements OutputGuard.
func (j LLMJudge) CheckOutput(ctx context.Context, msg schema.Message) error {
	return j.judge(ctx, msg)
}

func (j LLMJudge) judge(ctx context.Context, msg schema.Message) error {
	subject := judgeSubject(msg)
	if len(subject.Content) == 0 {
		// Nothing to judge: no content parts and no tool calls. A
		// content policy is vacuously satisfied, and skipping the call
		// keeps tool-call-free empty turns from costing a judge round
		// trip or failing closed on a confused reply.
		return nil
	}

	maxTok := j.MaxTokens
	if maxTok <= 0 {
		maxTok = 16
	}

	sys := strings.TrimSpace(`You are a policy guardrail. Read the message below and decide whether it violates the policy.
Respond with ONLY the word ALLOW or BLOCK. No prose. No punctuation. No code fences.`)
	if j.Rule != "" {
		sys += "\n\nPolicy:\n" + j.Rule
	}

	reply, err := judgecall.Do(ctx, j.Provider, j.Model, sys, subject, maxTok)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrEvalFailed, err)
	}

	switch classifyJudgeReply(reply.Text()) {
	case "ALLOW":
		return nil
	case "BLOCK":
		return ErrJudgeBlocked
	default:
		return fmt.Errorf("%w: unexpected reply %q", ErrJudgeBlocked, strings.TrimSpace(reply.Text()))
	}
}

// judgeSubject builds the user message shown to the judge: the judged
// message's content parts verbatim (minus empty text parts) plus a
// textual rendering of its tool calls, so content smuggled into tool
// arguments is vetted alongside the prose.
func judgeSubject(msg schema.Message) schema.Message {
	parts := make([]schema.ContentPart, 0, len(msg.Content)+1)
	for _, p := range msg.Content {
		if p.Type == schema.ContentTypeText && strings.TrimSpace(p.Text) == "" {
			continue
		}
		parts = append(parts, p)
	}
	if len(msg.ToolCalls) > 0 {
		var b strings.Builder
		b.WriteString("Tool calls requested by the message:\n")
		for _, tc := range msg.ToolCalls {
			fmt.Fprintf(&b, "- %s(%s)\n", tc.Name, string(tc.Arguments))
		}
		parts = append(parts, schema.TextPart(b.String()))
	}
	return schema.Message{Role: schema.RoleUser, Content: parts}
}

// classifyJudgeReply normalizes the judge's raw reply to "ALLOW",
// "BLOCK", or "" (unparseable). The judge is instructed to emit a bare
// word, but LLMs add punctuation, casing or surrounding prose, so the
// reply is scanned word by word. Matching is deliberately asymmetric:
// block-shaped words (BLOCK/BLOCKED, DISALLOW/DISALLOWED, DENY/DENIED)
// count as BLOCK, while ALLOW only counts when no negation ("not",
// "never", "don't", ...) appears anywhere — "Not allowed" must never
// read as ALLOW. Anything ambiguous yields "" and the guard fails
// closed.
func classifyJudgeReply(raw string) string {
	// Drop apostrophes first so "don't" survives as one word.
	raw = strings.Map(func(r rune) rune {
		if r == '\'' || r == '’' {
			return -1
		}
		return r
	}, raw)

	var hasAllow, hasBlock, negated bool
	words := strings.FieldsFunc(raw, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z')
	})
	for _, word := range words {
		switch w := strings.ToUpper(word); {
		case strings.HasPrefix(w, "BLOCK"),
			strings.HasPrefix(w, "DISALLOW"),
			strings.HasPrefix(w, "DENY"),
			w == "DENIED":
			hasBlock = true
		case strings.HasPrefix(w, "ALLOW"):
			hasAllow = true
		case w == "NOT", w == "NO", w == "NEVER", w == "DONT", w == "CANNOT", w == "CANT", w == "WONT":
			negated = true
		}
	}

	switch {
	case hasBlock && !hasAllow:
		return "BLOCK"
	case hasAllow && !hasBlock && !negated:
		return "ALLOW"
	default:
		return ""
	}
}

// Compile-time interface assertions.
var (
	_ InputGuard  = LLMJudge{}
	_ OutputGuard = LLMJudge{}
	_ Kinded      = LLMJudge{}
)
