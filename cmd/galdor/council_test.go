package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// statefulOpenAIServer returns each of replies in turn (the i-th call
// gets replies[i], the last repeating). Used to drive the supervisor's
// multi-turn routing LLM, which must emit a delegation decision then a
// final decision.
func statefulOpenAIServer(t *testing.T, replies ...string) *httptest.Server {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		i := int(n.Add(1)) - 1
		if i >= len(replies) {
			i = len(replies) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{
			"id":"c","object":"chat.completion","model":"fake",
			"choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`, replies[i]))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runCouncil(t *testing.T, yaml, input string) (int, string, string) {
	t.Helper()
	path := writeTemp(t, "topology.yaml", yaml)
	var out, errOut bytes.Buffer
	code := councilCmd(context.Background(), []string{path, input}, &out, &errOut)
	return code, out.String(), errOut.String()
}

// TestCouncil_SupervisorDelegates drives the full supervisor loop: the
// routing LLM first delegates to a worker, then returns a final answer.
func TestCouncil_SupervisorDelegates(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	// Routing LLM: delegate once, then finish.
	router := statefulOpenAIServer(t,
		`{"worker":"helper","task":"greet the user"}`,
		`{"final":"all done"}`,
	)
	worker := fakeOpenAIServer(t, "hello from the helper")

	code, out, errOut := runCouncil(t, fmt.Sprintf(`version: 1
mode: supervisor
supervisor: {provider: openai, model: fake, base_url: %s}
workers:
  - name: helper
    description: "greets people"
    agent: {provider: openai, model: fake, base_url: %s}
`, router.URL, worker.URL), "say hi")

	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "all done") {
		t.Errorf("output = %q", out)
	}
}

// TestCouncil_Swarm runs a single-agent swarm: the start agent answers
// with no handoff, which terminates the swarm with that answer.
func TestCouncil_Swarm(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	agentSrv := fakeOpenAIServer(t, "the swarm answer")

	code, out, errOut := runCouncil(t, fmt.Sprintf(`version: 1
mode: swarm
start: solo
workers:
  - name: solo
    description: "answers directly"
    agent: {provider: openai, model: fake, base_url: %s}
`, agentSrv.URL), "question")

	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "swarm answer") {
		t.Errorf("output = %q", out)
	}
}

func TestCouncil_Validation(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		code int
		want string
	}{
		{
			"no workers",
			"version: 1\nmode: supervisor\nsupervisor: {provider: openai, model: m}\n",
			2, "at least one worker",
		},
		{
			"unknown mode",
			"version: 1\nmode: orchestra\nworkers: [{name: a, description: d, agent: {provider: openai, model: m}}]\n",
			64, "unknown mode",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OPENAI_API_KEY", "test-key")
			path := writeTemp(t, "t.yaml", tc.yaml)
			var out, errOut bytes.Buffer
			code := councilCmd(context.Background(), []string{path, "in"}, &out, &errOut)
			if code != tc.code {
				t.Fatalf("exit %d, want %d; stderr=%s", code, tc.code, errOut.String())
			}
			if !strings.Contains(errOut.String(), tc.want) {
				t.Errorf("stderr = %q, want %q", errOut.String(), tc.want)
			}
		})
	}
}
