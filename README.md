# galdor

> *galdor* (n., Old English, c. 9th century): incantation, spell, a chanted word that bends reality.

**Speak your AI agents into being.**

galdor is a Go-native framework for building, orchestrating and observing real AI agents — where observability is native, packaged in the binary, and requires no paid external service.

---

## Status

**Work in progress — Phase 0 (Bootstrap).** APIs are unstable until v1.0.

See [`docs/PLAN.md`](docs/PLAN.md) for the full design plan and [`ROADMAP.md`](ROADMAP.md) for delivery phases.

---

## What galdor is

- A graph-based agent runtime executed with goroutines and channels
- Truly provider-agnostic (Anthropic, OpenAI, Google, Bedrock, Azure, Ollama, vLLM)
- Type-safe end-to-end using Go 1.25+ generics
- Native observability: tracing, metrics, replay, eval — all in the same binary
- Multi-agent as a first-class primitive
- MCP (Model Context Protocol) integrated from day one
- Self-hosted by design: one binary, optional Postgres/ClickHouse for scale

## What galdor is not

- Not a port of LangChain
- Not a wrapper around a single provider
- Not "yet another SDK" — providers exist (GoAI SDK, go-openai)
- Not a SaaS, no paid tiers, no locked features
- Not dependent on Langfuse, LangSmith, Laminar or Phoenix to be production-usable

---

## License

galdor is and will always be 100% open source under [Apache License 2.0](LICENSE). There will be no "Enterprise" edition, no paid features, and no cloud service behind a paywall. If a commercial offering ever exists, it will be support, consulting or optional hosting — never locked features.

## Contributing

Contributions are welcome. galdor uses the [Developer Certificate of Origin (DCO)](DCO.txt) — every commit must be signed off. See [`CONTRIBUTING.md`](CONTRIBUTING.md) for setup.

## Governance

galdor is currently maintained by a single BDFL with an explicit plan to transition to a multi-maintainer model once traction permits. See [`GOVERNANCE.md`](GOVERNANCE.md).

---

*"The incantation framework for Go agents."*
