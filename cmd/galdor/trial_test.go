package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeOpenAIServer returns a chat-completions endpoint that answers every
// request with reply, so a trial suite can run end-to-end offline through
// the real openai adapter (provider: openai + base_url).
func fakeOpenAIServer(t *testing.T, reply string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := fmt.Sprintf(`{
			"id": "chatcmpl-test",
			"object": "chat.completion",
			"model": "fake",
			"choices": [{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],
			"usage": {"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`, reply)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runTrialSuite(t *testing.T, yaml string) (int, string, string) {
	t.Helper()
	path := writeTemp(t, "suite.yaml", yaml)
	var out, errOut bytes.Buffer
	code := trial(context.Background(), []string{path}, &out, &errOut)
	return code, out.String(), errOut.String()
}

// TestTrial_EndToEndPasses drives the whole verb against a fake OpenAI
// server: YAML → config → providerset → openai adapter → eval → report.
func TestTrial_EndToEndPasses(t *testing.T) {
	srv := fakeOpenAIServer(t, "Quito is the capital of Ecuador.")
	t.Setenv("OPENAI_API_KEY", "test-key")

	code, out, errOut := runTrialSuite(t, fmt.Sprintf(`version: 1
dataset:
  name: geo
  cases:
    - {id: c1, input: "Capital of Ecuador?", expected: Quito}
subject:
  provider: openai
  model: fake
  base_url: %s
scorers:
  - {type: contains}
min_pass: 1.0
`, srv.URL))

	if code != 0 {
		t.Fatalf("exit %d (want 0); stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "Pass rate: 100") {
		t.Errorf("summary should show 100%% pass:\n%s", out)
	}
}

// TestTrial_EndToEndFailsBelowThreshold confirms the CI gate: a wrong
// answer drops the pass rate below min_pass and the verb exits 1.
func TestTrial_EndToEndFailsBelowThreshold(t *testing.T) {
	srv := fakeOpenAIServer(t, "Lima.") // wrong answer for the case below
	t.Setenv("OPENAI_API_KEY", "test-key")

	code, _, errOut := runTrialSuite(t, fmt.Sprintf(`version: 1
dataset:
  name: geo
  cases:
    - {id: c1, input: "Capital of Ecuador?", expected: Quito}
subject:
  provider: openai
  model: fake
  base_url: %s
scorers:
  - {type: contains}
min_pass: 1.0
`, srv.URL))

	if code != 1 {
		t.Fatalf("exit %d (want 1 for below-threshold); stderr=%s", code, errOut)
	}
	if !strings.Contains(errOut, "threshold") {
		t.Errorf("stderr should mention the threshold: %s", errOut)
	}
}

// TestTrial_EndToEndWithLLMJudge exercises the llm_judge scorer end to
// end: a second fake server stands in for the judge model.
func TestTrial_EndToEndWithLLMJudge(t *testing.T) {
	subject := fakeOpenAIServer(t, "Quito.")
	judge := fakeOpenAIServer(t, "95") // judge returns a high score
	t.Setenv("OPENAI_API_KEY", "test-key")

	code, out, errOut := runTrialSuite(t, fmt.Sprintf(`version: 1
dataset:
  name: geo
  cases:
    - {id: c1, input: "Capital of Ecuador?", expected: Quito}
subject:
  provider: openai
  model: fake
  base_url: %s
scorers:
  - type: llm_judge
    judge: {provider: openai, model: fake, base_url: %s}
    rubric: "Score 100 if the answer names Quito."
    pass_threshold: 0.7
min_pass: 1.0
`, subject.URL, judge.URL))

	if code != 0 {
		t.Fatalf("exit %d (want 0); stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "llm_judge") {
		t.Errorf("summary should list the llm_judge scorer:\n%s", out)
	}
}

func TestTrial_JSONOutput(t *testing.T) {
	srv := fakeOpenAIServer(t, "Quito.")
	t.Setenv("OPENAI_API_KEY", "test-key")
	code, out, errOut := runTrialSuite(t, fmt.Sprintf(`version: 1
dataset: {name: geo, cases: [{id: c1, input: "q", expected: Quito}]}
subject: {provider: openai, model: fake, base_url: %s}
scorers: [{type: contains}]
min_pass: 1.0
`, srv.URL))
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, errOut)
	}
	// trial --json isn't set here, so default text; just assert it ran.
	if !strings.Contains(out, "geo") {
		t.Errorf("output: %s", out)
	}
}

func TestTrial_SetupErrors(t *testing.T) {
	t.Parallel()
	// No cases.
	code, _, _ := runTrialSuite(t, "version: 1\ndataset: {name: x}\nsubject: {provider: openai, model: m}\nscorers: [{type: contains}]\n")
	if code != 2 {
		t.Errorf("empty dataset should be setup error (2), got %d", code)
	}
}
