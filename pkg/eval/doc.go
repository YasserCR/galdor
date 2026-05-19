// Package eval ships the inline regression framework for prompts
// and agents. You declare a Dataset (a list of input/expected
// Cases), a Subject (the agent under test) and one or more Scorers,
// and Evaluator runs every case in parallel and produces a Report.
//
// Typical use from a Go test or CI script:
//
//	cfg := eval.Config{
//	    Dataset: eval.MustLoadDataset("./datasets/math.json"),
//	    Subject: func(ctx context.Context, in string) (string, error) {
//	        return agent.Run(ctx, agentCfg, in)
//	    },
//	    Scorers: []eval.Scorer{
//	        eval.Contains{},
//	        eval.LLMJudge{Provider: p, Model: "claude-haiku-4-5", Rubric: "is the answer correct?"},
//	    },
//	    Parallel: 4,
//	    MinPass:  0.9,
//	}
//	eval.RunAndExit(ctx, cfg)
//
// Built-in scorers:
//
//   - ExactMatch:  actual == expected (after trimming)
//   - Contains:    expected substring appears in actual (case-insensitive)
//   - Regex:       actual matches Pattern
//   - LLMJudge:    another LLM rates actual against a Rubric (0..1)
//   - ScorerFunc:  arbitrary user function
//
// Datasets are JSON files; the schema is the Dataset Go type, so
// round-tripping is trivial. Version is recorded in every Report so
// regressions can be attributed to dataset drift vs. model drift.
//
// The framework is provider-agnostic — Subject is just a func, so it
// can wrap a ReAct loop, a Supervisor, a Plan-and-Execute pipeline,
// or anything else you can put behind a string-in/string-out signature.
package eval
