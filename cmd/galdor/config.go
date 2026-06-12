package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	yaml "github.com/goccy/go-yaml"

	"github.com/YasserCR/galdor/pkg/agent"
	"github.com/YasserCR/galdor/pkg/mcp"
	"github.com/YasserCR/galdor/pkg/provider"
	"github.com/YasserCR/galdor/pkg/tool"
	"github.com/YasserCR/galdor/pkg/tool/builtins"
	"github.com/YasserCR/galdor/providerset"
)

// configVersion is the only schema version understood by the CLI. Every
// config file must declare `version: 1`; additive changes stay on 1, a
// breaking change bumps it. (ADR-014.)
const configVersion = 1

// AgentBlock is the reusable agent definition shared across the
// config-driven verbs (trial subject, and later cast/council). It maps to
// agent.Config, with the Go-only fields resolved declaratively: the
// provider interface from a name + env-resolved key (providerset), and the
// tool registry from builtins + MCP servers.
//
// A config-driven agent can bind builtin tools and MCP-served tools only —
// custom Go tools stay a library feature (ADR-014).
type AgentBlock struct {
	// Provider names the backend (anthropic, openai, google, bedrock, or
	// an OpenAI-compatible alias). Resolved via providerset.
	Provider string `yaml:"provider"`
	// Model is the model id. Required.
	Model string `yaml:"model"`
	// BaseURL overrides the provider endpoint (OpenAI-compatible hosts).
	BaseURL string `yaml:"base_url,omitempty"`
	// APIKeyEnv overrides which env var holds the API key. Default is the
	// `<PROVIDER>_API_KEY` convention (e.g. ANTHROPIC_API_KEY), then
	// LLM_API_KEY. The secret itself never lives in the file.
	APIKeyEnv string `yaml:"api_key_env,omitempty"`

	System        string     `yaml:"system,omitempty"`
	MaxIterations int        `yaml:"max_iterations,omitempty"`
	Temperature   *float64   `yaml:"temperature,omitempty"`
	TopP          *float64   `yaml:"top_p,omitempty"`
	MaxTokens     *int       `yaml:"max_tokens,omitempty"`
	Tools         ToolsBlock `yaml:"tools,omitempty"`
}

// ToolsBlock declares the tools an agent may call.
type ToolsBlock struct {
	// Builtins lists builtin tool names: time, math, http_get, file_read.
	Builtins []string `yaml:"builtins,omitempty"`
	// BaseDir confines file_read (required when file_read is listed).
	BaseDir string `yaml:"base_dir,omitempty"`
	// AllowedHosts restricts http_get (required when http_get is listed,
	// unless AllowAnyHost is set).
	AllowedHosts []string `yaml:"allowed_hosts,omitempty"`
	AllowAnyHost bool     `yaml:"allow_any_host,omitempty"`
	AllowHTTP    bool     `yaml:"allow_http,omitempty"`
	// MCP lists MCP servers whose tools are adopted into the registry.
	MCP []MCPRef `yaml:"mcp,omitempty"`
}

// MCPRef points at one MCP server: either an http(s):// URL (Streamable
// HTTP) or a subprocess command (stdio). Exactly one must be set.
type MCPRef struct {
	URL     string   `yaml:"url,omitempty"`
	Command []string `yaml:"command,omitempty"`
}

// versioned is implemented by every top-level config struct so the loader
// can enforce `version: 1` generically.
type versioned interface{ schemaVersion() int }

// loadConfigFile reads path, strict-decodes it into dst (unknown keys are
// an error with a line:col position, courtesy of goccy/go-yaml), and
// verifies the declared schema version.
func loadConfigFile(path string, dst versioned) error {
	raw, err := os.ReadFile(path) // #nosec G304 -- config path is supplied by the CLI user
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	// Strict: reject unknown fields so a mistyped key fails loudly with
	// its position instead of being silently ignored.
	if derr := yaml.UnmarshalWithOptions(raw, dst, yaml.Strict()); derr != nil {
		return fmt.Errorf("parse %s: %w", path, derr)
	}
	if v := dst.schemaVersion(); v != configVersion {
		if v == 0 {
			return fmt.Errorf("%s: missing required `version: %d`", path, configVersion)
		}
		return fmt.Errorf("%s: unsupported version %d (this binary understands version %d)", path, v, configVersion)
	}
	return nil
}

// resolveAPIKey reads the API key from the environment. Order: an explicit
// api_key_env, then the `<PROVIDER>_API_KEY` convention, then LLM_API_KEY.
// The secret is never read from the config file itself.
func resolveAPIKey(block AgentBlock) string {
	if block.APIKeyEnv != "" {
		return os.Getenv(block.APIKeyEnv)
	}
	if prov := strings.ToUpper(strings.TrimSpace(block.Provider)); prov != "" {
		if v := os.Getenv(prov + "_API_KEY"); v != "" {
			return v
		}
	}
	return os.Getenv("LLM_API_KEY")
}

// resolveProvider constructs the provider interface for an AgentBlock from
// its name + an env-resolved API key.
func resolveProvider(block AgentBlock) (provider.Provider, error) {
	if strings.TrimSpace(block.Provider) == "" {
		return nil, fmt.Errorf("agent: provider is required")
	}
	if strings.TrimSpace(block.Model) == "" {
		return nil, fmt.Errorf("agent: model is required")
	}
	p, err := providerset.New(providerset.Config{
		Provider: block.Provider,
		APIKey:   resolveAPIKey(block),
		BaseURL:  block.BaseURL,
	})
	if err != nil {
		return nil, fmt.Errorf("agent: %w", err)
	}
	return p, nil
}

// resolveAgentConfig turns an AgentBlock into an agent.Config plus a
// cleanup func that releases any MCP connections opened for its tools. The
// cleanup must be called once the agent is done running; it is always
// non-nil and safe to call.
func resolveAgentConfig(ctx context.Context, block AgentBlock, errW io.Writer) (agent.Config, func(), error) {
	p, err := resolveProvider(block) //nolint:contextcheck // providerset.New has no ctx parameter (it loads the AWS config internally for bedrock); nothing to propagate
	if err != nil {
		return agent.Config{}, func() {}, err
	}
	reg, cleanup, err := resolveToolRegistry(ctx, block.Tools, errW)
	if err != nil {
		return agent.Config{}, func() {}, err
	}
	cfg := agent.Config{
		Provider:      p,
		Model:         block.Model,
		Tools:         reg,
		MaxIterations: block.MaxIterations,
		Temperature:   block.Temperature,
		TopP:          block.TopP,
		MaxTokens:     block.MaxTokens,
	}
	return cfg, cleanup, nil
}

// resolveToolRegistry builds the tool registry for an agent: builtins
// (guard-gated like `mcp serve`) plus every MCP server's adopted tools.
// Returns a nil registry when no tools are declared. The cleanup func
// closes all MCP clients; it is always non-nil and safe to call.
func resolveToolRegistry(ctx context.Context, tb ToolsBlock, errW io.Writer) (*tool.Registry, func(), error) {
	var (
		tools   []tool.AnyTool
		closers []func()
	)
	cleanup := func() {
		for _, c := range closers {
			c()
		}
	}

	for _, name := range tb.Builtins {
		t, err := builtinByName(strings.TrimSpace(name), tb)
		if err != nil {
			cleanup()
			return nil, func() {}, err
		}
		tools = append(tools, t)
	}

	for i, ref := range tb.MCP {
		client, clean, err := dialMCPRef(ctx, ref, errW)
		if err != nil {
			cleanup()
			return nil, func() {}, fmt.Errorf("tools.mcp[%d]: %w", i, err)
		}
		closers = append(closers, clean)
		reg, err := client.AsRegistry(ctx)
		if err != nil {
			cleanup()
			return nil, func() {}, fmt.Errorf("tools.mcp[%d]: list tools: %w", i, err)
		}
		tools = append(tools, reg.Tools()...)
	}

	if len(tools) == 0 {
		return nil, cleanup, nil
	}
	reg, err := tool.NewRegistry(tools...)
	if err != nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("tools: %w", err)
	}
	return reg, cleanup, nil
}

// builtinByName constructs one builtin tool, applying the same guards as
// `mcp serve`: file_read needs base_dir; http_get needs an allowlist or
// allow_any_host.
func builtinByName(name string, tb ToolsBlock) (tool.AnyTool, error) {
	switch name {
	case "time":
		return builtins.MustNewTimeTool(), nil
	case "math":
		return builtins.MustNewMathTool(), nil
	case "file_read":
		if tb.BaseDir == "" {
			return nil, fmt.Errorf("tool file_read requires tools.base_dir")
		}
		return builtins.NewFileReadTool(builtins.FileReadOptions{BaseDir: tb.BaseDir})
	case "http_get":
		if len(tb.AllowedHosts) == 0 && !tb.AllowAnyHost {
			return nil, fmt.Errorf("tool http_get requires tools.allowed_hosts or tools.allow_any_host")
		}
		return builtins.NewHTTPGetTool(builtins.HTTPGetOptions{
			AllowedHosts: tb.AllowedHosts,
			AllowHTTP:    tb.AllowHTTP,
		})
	default:
		return nil, fmt.Errorf("unknown builtin tool %q (have: time, math, file_read, http_get)", name)
	}
}

// dialMCPRef connects to one MCP server reference (URL or subprocess) and
// returns an initialized client plus its cleanup.
func dialMCPRef(ctx context.Context, ref MCPRef, errW io.Writer) (*mcp.Client, func(), error) {
	switch {
	case ref.URL != "" && len(ref.Command) > 0:
		return nil, nil, fmt.Errorf("set either url or command, not both")
	case ref.URL != "":
		c, clean, _, err := dialTarget(ctx, []string{ref.URL}, nil, errW)
		return c, clean, err
	case len(ref.Command) > 0:
		c, clean, _, err := dialTarget(ctx, nil, ref.Command, errW)
		return c, clean, err
	default:
		return nil, nil, fmt.Errorf("empty mcp reference: set url or command")
	}
}
