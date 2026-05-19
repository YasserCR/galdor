// Package council provides high-level multi-agent orchestration primitives.
//
// Two patterns ship in Session A of Phase 7:
//
//   - Supervisor: a router LLM picks which specialized worker handles
//     each turn. Workers are plain Go functions (typically wrapping a
//     ReAct runnable) so composition stays explicit. See NewSupervisor.
//
//   - Swarm: peer agents collaborate over a shared conversation and
//     hand off control to each other via synthetic tools the framework
//     injects. See NewSwarm.
//
// Hierarchy and the A2A protocol arrive in later sessions of Phase 7.
//
// "Council" is one of the few themed package names in galdor; it
// encapsulates a compound concept where the theming adds semantics
// without obscuring the technical primitives — NewSupervisor /
// NewSwarm are deliberately unthemed and Go-idiomatic.
package council
