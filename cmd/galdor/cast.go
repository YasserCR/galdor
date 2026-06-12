package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/YasserCR/galdor/pkg/agent"
	"github.com/YasserCR/galdor/pkg/graph"
	"github.com/YasserCR/galdor/pkg/observability"
	"github.com/YasserCR/galdor/pkg/schema"
)

// cast runs a ReAct agent described by a YAML file against an input. With
// --trace it records the run (provider + tool + node spans) to the span
// store so it shows up in `galdor scry` / `ui` / `weave`.
func cast(ctx context.Context, args []string, w io.Writer, errW io.Writer) int {
	fs := flag.NewFlagSet("cast", flag.ContinueOnError)
	fs.SetOutput(errW)
	traceFlag := fs.Bool("trace", false, "record the run to the span store (view with galdor scry/ui)")
	db := fs.String("db", "", "span store path for --trace (defaults to $GALDOR_DB or ~/.galdor/traces.db)")
	runID := fs.String("run-id", "", "run id stamped on the trace (default: generated)")
	// stdlib flag stops at the first non-flag token, so flags placed AFTER
	// the file/input (the natural place to type --trace) would be silently
	// dropped. Partition first so flags are honored wherever they appear.
	flags, rest := partitionArgs(args, map[string]bool{"db": true, "run-id": true})
	if err := fs.Parse(flags); err != nil {
		return 64
	}
	if len(rest) < 1 {
		_, _ = fmt.Fprintf(errW, "cast: expected an agent file\n\n%s\n", castUsage)
		return 64
	}
	agentPath := rest[0]

	// Input: the positional(s) after the file, or stdin when piped.
	input := readCommandInput(rest[1:])
	if input == "" {
		_, _ = fmt.Fprintf(errW, "cast: no input — pass it as an argument or pipe it on stdin\n\n%s\n", castUsage)
		return 64
	}

	var cc CastConfig
	if err := loadConfigFile(agentPath, &cc); err != nil {
		_, _ = fmt.Fprintf(errW, "cast: %v\n", err)
		return 2
	}
	cfg, cleanup, err := resolveAgentConfig(ctx, cc.Agent, errW)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "cast: %v\n", err)
		return 2
	}
	defer cleanup()

	if *traceFlag {
		return castTraced(ctx, cfg, cc.Agent.System, input, *db, *runID, w, errW)
	}

	answer, err := agent.Run(ctx, cfg, input, cc.Agent.System)
	if err != nil && !errors.Is(err, agent.ErrMaxIterations) {
		_, _ = fmt.Fprintf(errW, "cast: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(w, answer)
	if errors.Is(err, agent.ErrMaxIterations) {
		_, _ = fmt.Fprintln(errW, "cast: note — stopped at the iteration cap; the answer above may be incomplete")
	}
	return 0
}

const castUsage = `galdor cast — run an agent from a YAML file.

Usage:
  galdor cast <agent.yaml> <input>
  echo "<input>" | galdor cast <agent.yaml>
  galdor cast <agent.yaml> <input> --trace [--db PATH] [--run-id ID]

The file is an agent block under "agent:" — provider + model, optional
system prompt and tools (builtins + MCP). --trace records the run to the
span store so you can inspect it with galdor scry / ui / weave.`

// CastConfig is the top-level schema for `galdor cast`: an agent block
// plus the required version.
type CastConfig struct {
	Version int        `yaml:"version"`
	Agent   AgentBlock `yaml:"agent"`
}

func (c *CastConfig) schemaVersion() int { return c.Version }

// stdin is the input source for piped command input; overridable in tests.
var stdin io.Reader = os.Stdin

// partitionArgs separates flag tokens from positional ones so flags are
// honored wherever the user puts them (stdlib flag stops at the first
// positional). valueFlags names the flags that consume a following token
// (e.g. --db PATH). A "--" terminator forces everything after it to be
// positional, so free-form input that starts with "-" survives.
func partitionArgs(args []string, valueFlags map[string]bool) (flags, positional []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) > 1 && strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			if strings.Contains(a, "=") {
				continue // --flag=value is self-contained
			}
			name := strings.TrimLeft(a, "-")
			if valueFlags[name] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positional = append(positional, a)
	}
	return flags, positional
}

// readCommandInput returns the input for cast/council: the joined
// arguments after the config file, or piped stdin when none are given.
func readCommandInput(inputArgs []string) string {
	if len(inputArgs) > 0 {
		return strings.Join(inputArgs, " ")
	}
	if isPipedStdin() {
		piped, _ := io.ReadAll(io.LimitReader(stdin, 1<<20))
		return strings.TrimSpace(string(piped))
	}
	return ""
}

// isPipedStdin reports whether stdin is a pipe/redirect (not a terminal),
// so cast only blocks on a read when input is actually being piped. A
// non-file stdin (a test reader) counts as piped.
func isPipedStdin() bool {
	f, ok := stdin.(*os.File)
	if !ok {
		return true
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice == 0
}

// castTraced runs the agent with full instrumentation (provider, tools,
// node hops) exported to the SQLite span store, then prints how to view
// it. It mirrors agent.Run, but goes through NewReAct + InvokeWith so the
// graph hooks and run id can be attached.
func castTraced(ctx context.Context, cfg agent.Config, system, input, dbPath, runID string, w io.Writer, errW io.Writer) int {
	path, err := resolveDBPath(dbPath)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "cast: %v\n", err)
		return 2
	}
	exporter, err := observability.NewSQLiteExporter(path) //nolint:contextcheck // NewSQLiteExporter takes no ctx; its internal checkpointer has its own lifecycle (closed on Shutdown)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "cast: open span store: %v\n", err)
		return 2
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter))
	tracer := tp.Tracer("galdor")

	cfg.Provider = observability.InstrumentProvider(cfg.Provider, tracer, observability.WithCaptureContent(true))
	if cfg.Tools != nil {
		traced, terr := observability.InstrumentRegistry(cfg.Tools, tracer)
		if terr != nil {
			_, _ = fmt.Fprintf(errW, "cast: %v\n", terr)
			return 2
		}
		cfg.Tools = traced
	}

	r, err := agent.NewReAct(cfg)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "cast: %v\n", err)
		return 2
	}
	if runID == "" {
		runID = fmt.Sprintf("cast-%d", time.Now().UnixNano())
	}
	msgs := make([]schema.Message, 0, 2)
	if system != "" {
		msgs = append(msgs, schema.SystemMessage(system))
	}
	msgs = append(msgs, schema.UserMessage(input))

	final, runErr := r.InvokeWith(ctx, agent.State{Messages: msgs}, graph.RunOptions[agent.State]{
		Hooks: observability.TraceHooks[agent.State](tracer),
		RunID: runID,
	})
	// Always flush spans, even on error, so a partial run is still
	// inspectable. A fresh context is intentional: the run's ctx may be
	// cancelled (signal), and we still want the buffered spans flushed.
	if shutErr := tp.Shutdown(context.Background()); shutErr != nil { //nolint:contextcheck // deliberate fresh ctx so a cancelled run still flushes its spans
		_, _ = fmt.Fprintf(errW, "cast: flush spans: %v\n", shutErr)
	}
	if runErr != nil && !errors.Is(runErr, agent.ErrMaxIterations) {
		_, _ = fmt.Fprintf(errW, "cast: %v\n", runErr)
		return 1
	}

	_, _ = fmt.Fprintln(w, final.FinalText)
	if errors.Is(runErr, agent.ErrMaxIterations) {
		_, _ = fmt.Fprintln(errW, "cast: note — stopped at the iteration cap; the answer above may be incomplete")
	}
	_, _ = fmt.Fprintf(errW, "cast: traced as run %q\n", runID)
	_, _ = fmt.Fprintf(errW, "  galdor scry show %s --db %s\n", runID, path)
	_, _ = fmt.Fprintf(errW, "  galdor weave %s --db %s\n", runID, path)
	return 0
}
