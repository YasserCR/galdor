package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/YasserCR/galdor/pkg/agent"
	"github.com/YasserCR/galdor/pkg/council"
	"github.com/YasserCR/galdor/pkg/schema"
)

// council runs a multi-agent orchestration from a YAML file: a Supervisor
// (a routing LLM delegating to named worker agents) or a Swarm (peer
// agents handing off to each other). Each worker is an agent block.
func councilCmd(ctx context.Context, args []string, w io.Writer, errW io.Writer) int {
	fs := flag.NewFlagSet("council", flag.ContinueOnError)
	fs.SetOutput(errW)
	if err := fs.Parse(args); err != nil {
		return 64
	}
	rest := fs.Args()
	if len(rest) < 1 {
		_, _ = fmt.Fprintf(errW, "council: expected a topology file\n\n%s\n", councilUsage)
		return 64
	}
	input := readCommandInput(rest[1:])
	if input == "" {
		_, _ = fmt.Fprintf(errW, "council: no input — pass it as an argument or pipe it on stdin\n\n%s\n", councilUsage)
		return 64
	}

	var cc CouncilConfig
	if err := loadConfigFile(rest[0], &cc); err != nil {
		_, _ = fmt.Fprintf(errW, "council: %v\n", err)
		return 2
	}
	if len(cc.Workers) == 0 {
		_, _ = fmt.Fprintln(errW, "council: at least one worker is required")
		return 2
	}

	switch cc.Mode {
	case "", "supervisor":
		return runSupervisor(ctx, cc, input, w, errW)
	case "swarm":
		return runSwarm(ctx, cc, input, w, errW)
	default:
		_, _ = fmt.Fprintf(errW, "council: unknown mode %q (want supervisor or swarm)\n", cc.Mode)
		return 64
	}
}

const councilUsage = `galdor council — run a multi-agent orchestration from a YAML file.

Usage:
  galdor council <topology.yaml> <input>
  echo "<input>" | galdor council <topology.yaml>

The file declares a mode (supervisor, the default, or swarm) and a list of
workers, each an agent block. In supervisor mode a routing LLM (the
"supervisor:" block) delegates to the workers; in swarm mode the workers
hand off to each other (via per-worker "handoffs:").`

// CouncilConfig is the top-level schema for `galdor council`.
type CouncilConfig struct {
	Version int    `yaml:"version"`
	Mode    string `yaml:"mode,omitempty"` // supervisor (default) | swarm

	// Supervisor is the routing LLM (supervisor mode).
	Supervisor   AgentBlock `yaml:"supervisor,omitempty"`
	SystemPrompt string     `yaml:"system_prompt,omitempty"`

	// Start names the first agent (swarm mode); defaults to the first
	// worker.
	Start string `yaml:"start,omitempty"`

	MaxHops int           `yaml:"max_hops,omitempty"`
	Workers []WorkerBlock `yaml:"workers"`
}

func (c *CouncilConfig) schemaVersion() int { return c.Version }

// WorkerBlock is one worker: a named agent block, plus (swarm mode) the
// names of the peers it may hand off to.
type WorkerBlock struct {
	Name        string     `yaml:"name"`
	Description string     `yaml:"description"`
	Agent       AgentBlock `yaml:"agent"`
	Handoffs    []string   `yaml:"handoffs,omitempty"`
}

func runSupervisor(ctx context.Context, cc CouncilConfig, input string, w io.Writer, errW io.Writer) int {
	p, err := resolveProvider(cc.Supervisor) //nolint:contextcheck // providerset.New has no ctx parameter; nothing to propagate
	if err != nil {
		_, _ = fmt.Fprintf(errW, "council: supervisor: %v\n", err)
		return 2
	}

	var (
		workers  []council.Worker
		cleanups []func()
	)
	cleanupAll := func() {
		for _, c := range cleanups {
			c()
		}
	}
	for _, wb := range cc.Workers {
		wcfg, clean, werr := resolveAgentConfig(ctx, wb.Agent, errW)
		if werr != nil {
			cleanupAll()
			_, _ = fmt.Fprintf(errW, "council: worker %q: %v\n", wb.Name, werr)
			return 2
		}
		cleanups = append(cleanups, clean)
		system, serr := effectiveSystem(wb.Agent)
		if serr != nil {
			cleanupAll()
			_, _ = fmt.Fprintf(errW, "council: worker %q: %v\n", wb.Name, serr)
			return 2
		}
		cfg := wcfg
		workers = append(workers, council.Worker{
			Name:        wb.Name,
			Description: wb.Description,
			Run: func(ctx context.Context, task string) (string, error) {
				if system != "" {
					return agent.Run(ctx, cfg, task, system)
				}
				return agent.Run(ctx, cfg, task)
			},
		})
	}
	defer cleanupAll()

	sup, err := council.NewSupervisor(council.SupervisorConfig{
		Provider:     p,
		Model:        cc.Supervisor.Model,
		Workers:      workers,
		MaxHops:      cc.MaxHops,
		SystemPrompt: cc.SystemPrompt,
	})
	if err != nil {
		_, _ = fmt.Fprintf(errW, "council: %v\n", err)
		return 2
	}
	final, err := sup.Invoke(ctx, council.SupervisorState{Input: input})
	if err != nil {
		_, _ = fmt.Fprintf(errW, "council: %v\n", err)
		if final.Final == "" {
			return 1
		}
	}
	_, _ = fmt.Fprintln(w, final.Final)
	return 0
}

func runSwarm(ctx context.Context, cc CouncilConfig, input string, w io.Writer, errW io.Writer) int {
	var (
		agents   []*council.SwarmAgent
		cleanups []func()
	)
	cleanupAll := func() {
		for _, c := range cleanups {
			c()
		}
	}
	for _, wb := range cc.Workers {
		p, err := resolveProvider(wb.Agent) //nolint:contextcheck // providerset.New has no ctx parameter; nothing to propagate
		if err != nil {
			cleanupAll()
			_, _ = fmt.Fprintf(errW, "council: agent %q: %v\n", wb.Name, err)
			return 2
		}
		reg, clean, err := resolveToolRegistry(ctx, wb.Agent.Tools, errW)
		if err != nil {
			cleanupAll()
			_, _ = fmt.Fprintf(errW, "council: agent %q: %v\n", wb.Name, err)
			return 2
		}
		cleanups = append(cleanups, clean)
		system, serr := effectiveSystem(wb.Agent)
		if serr != nil {
			cleanupAll()
			_, _ = fmt.Fprintf(errW, "council: agent %q: %v\n", wb.Name, serr)
			return 2
		}
		agents = append(agents, &council.SwarmAgent{
			Name:          wb.Name,
			Description:   wb.Description,
			Provider:      p,
			Model:         wb.Agent.Model,
			Tools:         reg,
			Handoffs:      wb.Handoffs,
			SystemPrompt:  system,
			MaxIterations: wb.Agent.MaxIterations,
		})
	}
	defer cleanupAll()

	start := cc.Start
	if start == "" {
		start = cc.Workers[0].Name
	}
	sw, err := council.NewSwarm(council.SwarmConfig{
		Agents:  agents,
		Start:   start,
		MaxHops: cc.MaxHops,
	})
	if err != nil {
		_, _ = fmt.Fprintf(errW, "council: %v\n", err)
		return 2
	}
	final, err := sw.Invoke(ctx, council.SwarmState{
		Messages: []schema.Message{schema.UserMessage(input)},
		Active:   start,
	})
	if err != nil {
		_, _ = fmt.Fprintf(errW, "council: %v\n", err)
		if final.Final == "" {
			return 1
		}
	}
	_, _ = fmt.Fprintln(w, final.Final)
	return 0
}
