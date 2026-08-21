// Package guardrail defines the input and output guardrail contracts
// used to vet messages before they reach a model and before they are
// returned to a caller.
//
// A guard is a named policy check: an InputGuard validates inbound
// (user) messages, an OutputGuard validates outbound (model) messages.
// A guard vetoes a message by returning a non-nil error, which fails the
// run. Guards run in the order they are configured; the first veto wins.
//
// There are two flavors, distinguished by how they evaluate, not by any
// runtime tag — both satisfy the same interfaces:
//
//   - deterministic guards: pure code (regex, denylist, schema checks).
//     Build one inline with InputGuardFunc / OutputGuardFunc.
//   - LLM-as-judge guards: a second model votes ALLOW/BLOCK. Build one
//     with LLMJudge (it implements both InputGuard and OutputGuard).
//
// Guards may self-describe their evaluation mechanism by implementing
// the optional Kinded interface; KindOf reports it (KindDeterministic
// or KindLLM) and BlockError carries it for observability.
//
// The agent package consumes guards via its Config fields (InputGuards,
// OutputGuards), so you only pass the guards you want:
//
//	cfg := agent.Config{
//	    Provider: p,
//	    Model:    "claude-haiku-4-5",
//	    InputGuards: []guardrail.InputGuard{
//	        guardrail.InputGuardFunc{
//	            ID: "no-pii",
//	            Check: func(_ context.Context, m schema.Message) error {
//	                if strings.Contains(m.Text(), "password") {
//	                    return errors.New("PII detected")
//	                }
//	                return nil
//	            },
//	        },
//	    },
//	    OutputGuards: []guardrail.OutputGuard{
//	        guardrail.OutputGuardFunc{
//	            ID: "no-refusal",
//	            Check: func(_ context.Context, m schema.Message) error {
//	                if strings.HasPrefix(m.Text(), "I'm sorry") {
//	                    return errors.New("model refused")
//	                }
//	                return nil
//	            },
//	        },
//	        guardrail.LLMJudge{
//	            Provider: judgeProvider,
//	            Model:    "claude-haiku-4-5",
//	            Rule:     "block any reply that reveals a credit-card number",
//	        },
//	    },
//	}
//
// Callers detect a guard block with errors.Is(err, guardrail.ErrBlocked);
// the blocking guard's name, Kind and its own error are available via
// errors.As(err, &blockErr). A guard that could not evaluate a message at
// all (e.g. an LLM judge whose provider call failed) fails the run with
// an error matching ErrEvalFailed instead — an infrastructure failure is
// not a policy verdict.
package guardrail
