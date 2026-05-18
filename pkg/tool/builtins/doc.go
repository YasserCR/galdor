// Package builtins ships a small set of out-of-the-box tools that
// agents commonly want: time, math, HTTP GET, and file read. Each
// builtin is a constructor returning a tool.Tool[In, Out] (or, in a
// few cases, an AnyTool) that callers register the same way as their
// own tools.
//
// The builtins favor safety over expressiveness: defaults are
// conservative, and where a tool touches the network or the file
// system, options exist to constrain what it can reach (host
// allowlists, base-directory confinement, size and timeout caps).
// The shell / process-execution tool intentionally lives elsewhere —
// it requires the sandboxing scheme tracked in ADR-008 and is not
// shipped from this package.
package builtins
