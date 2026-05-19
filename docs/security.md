# Security

galdor is pre-alpha. Treat everything in this document as the
current state of the project, not as a guarantee for production
deployments. A formal third-party audit hasn't happened yet.

This file covers:

1. [Automated security tooling and how it's wired into CI](#tooling)
2. [Triage of every accepted (suppressed) finding](#accepted-findings)
3. [OWASP LLM Top 10 self-assessment](#owasp-llm-top-10-self-assessment)
4. [Reporting a vulnerability](#reporting-a-vulnerability)

---

## Tooling

Two security checks run on every PR and every push to `main`:

| Tool | Purpose | Scope |
|---|---|---|
| `govulncheck` | Detects known CVEs in the Go standard library, in modules we import, and in modules we transitively require (`golang.org/x/vuln`). | Every module — `.`, the four provider adapters, the three memory backends. |
| `gosec` | Static analysis for ~30 common security smells: weak crypto, hardcoded credentials, unsafe file paths, weak randomness, integer overflow, etc. | Same module matrix. |

Run them locally before sending a PR:

```bash
# Install once.
go install golang.org/x/vuln/cmd/govulncheck@latest
go install github.com/securego/gosec/v2/cmd/gosec@latest

# Main module.
govulncheck ./...
gosec ./...

# Each submodule with its own go.mod.
for m in providers/anthropic providers/openai providers/google providers/bedrock \
         memory/sqlite memory/pgvector memory/qdrant; do
  echo "== $m =="
  (cd "$m" && govulncheck ./... && gosec ./...)
done
```

Both tools must pass with **zero findings** before a commit lands on
`main`. Suppressions are allowed when justified; see the next section.

### Toolchain pin

Every `go.mod` requires `go 1.25.10` as the floor and pins the
`toolchain` to `go1.25.10`. This is deliberate: Go 1.25.0 through
1.25.9 carry 21 stdlib CVEs that affect our code paths (HTML
template escape bugs, x509 quadratic complexity, HTTP/2 frame
loop, TLS handshake info leak, etc.). Bumping the `go` directive
itself — not just the toolchain — is what convinces `govulncheck`
the stdlib is patched.

---

## Accepted findings

`gosec` produces a small set of warnings on patterns we use
deliberately. Each suppression is annotated inline with a `// #nosec
Gxxx -- reason` comment and listed here for review:

| File | gosec rule | What & why |
|---|---|---|
| `pkg/observability/attrs.go` | G101 (hardcoded credential) | The strings `gen_ai.usage.input_tokens` and `gen_ai.usage.output_tokens` are OpenTelemetry semantic-convention attribute *names* containing the substring "tokens" — not actual credentials. |
| `pkg/memory/embed.go` | G115 (int → uint32 overflow) | `uint32(dim)` for the hash-bucket modulus. `dim` is the embedding dimensionality, returned by `Dimensions()` which always returns a positive int (256+). The conversion has no runtime overflow path. |
| `pkg/provider/retry.go` | G404 (weak randomness) | `rand.Float64()` is used for *jitter* on exponential backoff — to avoid thundering-herd retries across a fleet. Jitter is not a security primitive; `crypto/rand` would be overkill. |
| `pkg/eval/loader.go` | G304 (file inclusion via variable), G306 (write permissions) | The dataset path is operator-supplied input. Eval datasets are public regression fixtures by definition; `0644` is correct for repo-committed test data. |
| `pkg/replay/loader.go` | G304, G306 | Same reasoning as eval: replay fixtures are non-secret regression artifacts that get committed to repos and read back on every CI run. |
| `pkg/tool/builtins/file.go` | G304 | The `file_read` builtin opens a caller-supplied path. The path is `BaseDir`-confined and symlink-gated upstream by `validatePath` so any traversal attempt is rejected before this line runs. |

When triaging a new gosec finding the question is always: *what
realistic threat model would this protect against, and does the
context here match that model?* If yes, fix the code. If no,
annotate with `// #nosec Gxxx -- specific reason` and add a row
to the table above.

---

## OWASP LLM Top 10 self-assessment

The [OWASP Top 10 for LLM Applications](https://owasp.org/www-project-top-10-for-large-language-model-applications/)
is the closest industry consensus on LLM-specific risks. This is
where galdor stands against each. Bear in mind: galdor is a
framework — many of these risks live with the operator who deploys
an agent, not with the framework itself.

### LLM01: Prompt Injection
**Risk**: malicious user input manipulates the model into
disregarding instructions, calling unauthorized tools, or leaking
data.
**Galdor's position**: the framework cannot prevent prompt
injection — that's a property of the model + prompt design. Galdor
does help mitigate the damage:
- Tools have explicit, named, typed inputs. A successful prompt
  injection still has to produce a valid `schema.ToolCall` to
  execute anything.
- `pkg/council.Supervisor` with separate tool registries per
  specialist isolates the blast radius — a billing agent that
  gets injected can't call the `delete_user` tool because it
  doesn't have it.
- `graph.InterruptBefore` is the answer for irreversible actions.
  See `examples/integration-approval-gate`.
**Operator responsibility**: write tight system prompts, separate
tool registries by sensitivity, gate destructive tools behind
human approval.

### LLM02: Insecure Output Handling
**Risk**: model output is fed downstream (shell, SQL, HTML) without
validation, leading to RCE/SQLi/XSS.
**Galdor's position**: galdor never executes arbitrary model
output. Tool calls are dispatched to pre-registered tools with
typed inputs decoded from JSON. The only output that hits HTML is
the embedded dashboard's rendering of spans, which uses Go's
`html/template` (auto-escaping). The capture-content feature
records raw bytes but only the operator's dashboard reads them
back.
**Operator responsibility**: don't pipe tool outputs into a shell
without validation; don't render assistant text as raw HTML in
your own UI.

### LLM03: Training Data Poisoning
**Out of scope** — galdor doesn't train models.

### LLM04: Model Denial of Service
**Risk**: an adversary crafts inputs that cause runaway token
consumption.
**Galdor's mitigations**:
- `graph.RunOptions.Timeout` and `NodeTimeout` cap wall time.
- `agent.Config.MaxIterations` caps ReAct loop iterations.
- `provider.Retry` doesn't retry non-retryable errors, so a
  malformed-prompt loop doesn't amplify.
- `examples/integration-cost-tracked` shows the `BudgetProvider`
  pattern for per-run token caps with `$`-denominated reporting.
**Operator responsibility**: configure timeouts and budgets on
every user-facing run.

### LLM05: Supply Chain Vulnerabilities
**Galdor's mitigations**:
- Core module has 6 direct + 14 indirect dependencies (the OTel +
  SQLite stack). Easy to audit.
- Per-provider Go modules so users only pull what they use.
- `govulncheck` runs on every push to `main` against every module.
- Toolchain pinned to a CVE-patched Go release (`go 1.25.10`+).
- Apache 2.0 license; no Contributor License Agreement; copyright
  stays with the contributor (DCO sign-off).
**Operator responsibility**: review the dependency tree of each
adapter you import; rebuild on every Go security release.

### LLM06: Sensitive Information Disclosure
**Risk**: agent surfaces PII / secrets in responses.
**Galdor's mitigations**:
- `observability.WithCaptureContent` is **opt-in**. Prompts and
  completions are NOT recorded by default. This is the
  privacy-conscious default.
- When capture is on, the operator owns the SQLite file. There's
  no SaaS in the path — nothing is shipped externally.
**Operator responsibility**: when content capture is on,
encrypt-at-rest the trace store; restrict who can read it; redact
PII upstream if your prompts carry it; pair with a future ADR-008
(tool sandboxing) when running untrusted prompts.

### LLM07: Insecure Plugin Design
**Risk**: tools (galdor's name: "tools" / "plugins") have
permissions wider than the LLM needs, or accept input without
validation.
**Galdor's mitigations**:
- Tools have typed, schema-derived inputs (`tool.NewTool[In, Out]`).
  Invalid JSON is rejected before the tool body runs.
- Built-in tools enforce their own limits:
  - `file_read`: `BaseDir` confinement + symlink gate + size cap.
  - `http_get`: URL allowlist + size cap + timeout.
- Tools register by name and only the registered names can be
  called — there is no eval-style dynamic dispatch.
**Operator responsibility**: register only the tools the agent
actually needs; use `pkg/council.Supervisor` to scope tool
registries by specialist; document expected input ranges.

### LLM08: Excessive Agency
**Risk**: agent has permissions beyond what its task requires
(e.g., a customer-support agent that can also delete database
records).
**Galdor's mitigations**:
- Same as LLM07 — narrow registries, named tool dispatch.
- `InterruptBefore` for destructive operations.
- `BudgetProvider` for cost caps.
**Operator responsibility**: principle of least privilege when
designing tool registries; gate destructive actions behind
approval; audit production runs via the trace store + steps view.

### LLM09: Overreliance
**Risk**: humans accept model output without verification.
**Galdor's mitigations**:
- The `eval` package + the `replay` package give you the
  primitives to catch regressions without trusting a single run.
- The dashboard's steps view shows every prompt + completion when
  capture is on, so a human can verify what actually happened.
**Operator responsibility**: keep humans in the loop for any
decision that matters; treat agent output as a draft, not as the
final answer.

### LLM10: Model Theft
**Out of scope** — galdor doesn't host model weights.

---

## Reporting a vulnerability

Until a formal security policy lands, report any vulnerability by:

1. Opening a private security advisory on GitHub
   (Settings → Security → Report a vulnerability) for the affected
   repo. Do NOT open a public issue.
2. Include reproduction steps, the affected version (git SHA),
   and the impact you observed.
3. Expect an initial response within seven days during pre-alpha;
   we will work with you on a disclosure timeline.

There is no bounty program yet.

---

## Last verified

This document and the suppressions in code were reviewed in May
2026. Re-run `govulncheck` and `gosec` on every release; update
the "Accepted findings" table when new suppressions land.
