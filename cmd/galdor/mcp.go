package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/YasserCR/galdor/pkg/mcp"
	"github.com/YasserCR/galdor/pkg/tool"
	"github.com/YasserCR/galdor/pkg/tool/builtins"
)

// mcp is the entry point for the `galdor mcp` verb: serve galdor's
// builtin tools as an MCP server, or act as a debugging client against
// any MCP server (a galdor one or a third-party one).
func mcpCmd(ctx context.Context, args []string, w io.Writer, errW io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(errW, mcpUsage)
		return 64
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "serve":
		return mcpServe(ctx, rest, w, errW)
	case "ls", "list", "tools":
		return mcpList(ctx, rest, w, errW)
	case "call":
		return mcpCall(ctx, rest, w, errW)
	case "-h", "--help", "help":
		_, _ = fmt.Fprintln(w, mcpUsage)
		return 0
	default:
		_, _ = fmt.Fprintf(errW, "galdor mcp: unknown subcommand %q\n\n%s\n", sub, mcpUsage)
		return 64
	}
}

const mcpUsage = `galdor mcp — serve galdor's builtin tools over MCP, or inspect any MCP server.

Usage:
  galdor mcp serve [--http ADDR] [--tools LIST] [--base-dir DIR]
                   [--allow-host HOST]... [--allow-any-host] [--allow-http]
  galdor mcp ls    <URL> | -- <command> [args...]
  galdor mcp call  <URL> <tool> [JSON] | <tool> [JSON] -- <command> [args...]

Serve exposes builtin tools (time, math, http_get, file_read) as an MCP
server. By default it speaks stdio (the transport Claude Desktop and most
hosts launch); pass --http to bind a Streamable HTTP listener instead.

  --http ADDR       Serve Streamable HTTP on ADDR (e.g. 127.0.0.1:4000)
                    instead of stdio.
  --tools LIST      Comma-separated allowlist (default: every configured
                    tool). time and math are always available; http_get
                    and file_read appear only when their guard flags below
                    are set.
  --base-dir DIR    Confine file_read to DIR (enables file_read).
  --allow-host H    Restrict http_get to host H (repeatable; enables
                    http_get).
  --allow-any-host  Let http_get reach any host (enables http_get; unsafe).
  --allow-http      Permit plain http:// URLs in http_get (default https).

ls and call connect to a server. The target is either an http(s):// URL
(Streamable HTTP) or a subprocess after "--" (stdio):

  galdor mcp ls   http://127.0.0.1:4000
  galdor mcp ls   -- galdor mcp serve
  galdor mcp call http://127.0.0.1:4000 math '{"op":"add","a":2,"b":3}'
  galdor mcp call math '{"op":"add","a":2,"b":3}' -- galdor mcp serve`

// ---------------------------------------------------------------------
// serve
// ---------------------------------------------------------------------

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func mcpServe(ctx context.Context, args []string, w io.Writer, errW io.Writer) int {
	fs := flag.NewFlagSet("mcp serve", flag.ContinueOnError)
	fs.SetOutput(errW)
	httpAddr := fs.String("http", "", "serve Streamable HTTP on this address instead of stdio")
	toolsCSV := fs.String("tools", "", "comma-separated allowlist of tools to serve")
	baseDir := fs.String("base-dir", "", "confine file_read to this directory (enables file_read)")
	allowAnyHost := fs.Bool("allow-any-host", false, "let http_get reach any host (enables http_get)")
	allowHTTP := fs.Bool("allow-http", false, "permit plain http:// URLs in http_get")
	var allowHosts stringList
	fs.Var(&allowHosts, "allow-host", "restrict http_get to this host (repeatable; enables http_get)")
	if err := fs.Parse(args); err != nil {
		if helpRequested(err) {
			return 0
		}
		return 64
	}

	reg, served, err := buildServeRegistry(*baseDir, allowHosts, *allowAnyHost, *allowHTTP, *toolsCSV)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "mcp serve: %v\n", err)
		return 64
	}
	if len(served) == 0 {
		_, _ = fmt.Fprintln(errW, "mcp serve: no tools selected to serve")
		return 64
	}

	srv := mcp.NewServer(reg, mcp.ServerInfo{Name: "galdor-builtins", Version: resolvedVersion})

	// SIGINT/SIGTERM trigger a clean shutdown of the serve loop.
	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if *httpAddr != "" {
		transport := mcp.NewStreamableHTTPTransport(*httpAddr)
		// Notices go to stderr; stdout is reserved (it's the wire on stdio).
		_, _ = fmt.Fprintf(errW, "galdor mcp serve: Streamable HTTP on %s — tools: %s\n", *httpAddr, strings.Join(served, ", "))
		_, _ = fmt.Fprintln(errW, "  (Ctrl-C to stop)")
		return serveResult(srv.Serve(sigCtx, transport), sigCtx, errW)
	}

	// stdio: the wire is stdin/stdout; every human-facing line must go to
	// stderr so it doesn't corrupt JSON-RPC framing.
	_, _ = fmt.Fprintf(errW, "galdor mcp serve: stdio — tools: %s\n", strings.Join(served, ", "))
	transport := mcp.NewStdioTransport(os.Stdin, w)
	return serveResult(srv.Serve(sigCtx, transport), sigCtx, errW)
}

// serveResult maps a Server.Serve return into a CLI exit code. A signal
// (sigCtx cancelled) surfaces from Serve as context.Canceled — that's a
// clean shutdown, exit 0, not an error.
func serveResult(err error, sigCtx context.Context, errW io.Writer) int {
	if err == nil || (errors.Is(err, context.Canceled) && sigCtx.Err() != nil) {
		return 0
	}
	_, _ = fmt.Fprintf(errW, "mcp serve: %v\n", err)
	return 70
}

// buildServeRegistry assembles the tool registry for `mcp serve`. time
// and math are always available; http_get and file_read are added only
// when their guard flags are set (so an LLM-facing server is never
// silently handed an unconfined filesystem read or open-web fetch).
// toolsCSV, when non-empty, narrows the set to the named tools and errors
// if a named tool isn't available.
func buildServeRegistry(baseDir string, allowHosts []string, allowAnyHost, allowHTTP bool, toolsCSV string) (*tool.Registry, []string, error) {
	available := map[string]tool.AnyTool{
		"time": builtins.MustNewTimeTool(),
		"math": builtins.MustNewMathTool(),
	}
	if baseDir != "" {
		fr, err := builtins.NewFileReadTool(builtins.FileReadOptions{BaseDir: baseDir})
		if err != nil {
			return nil, nil, fmt.Errorf("file_read: %w", err)
		}
		available["file_read"] = fr
	}
	if len(allowHosts) > 0 || allowAnyHost {
		hg, err := builtins.NewHTTPGetTool(builtins.HTTPGetOptions{
			AllowedHosts: allowHosts,
			AllowHTTP:    allowHTTP,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("http_get: %w", err)
		}
		available["http_get"] = hg
	}

	// Resolve the requested set.
	var wantNames []string
	if strings.TrimSpace(toolsCSV) != "" {
		for _, n := range strings.Split(toolsCSV, ",") {
			n = strings.TrimSpace(n)
			if n == "" {
				continue
			}
			if _, ok := available[n]; !ok {
				return nil, nil, fmt.Errorf("tool %q is not available — configure it (--base-dir for file_read, --allow-host/--allow-any-host for http_get) or remove it from --tools", n)
			}
			wantNames = append(wantNames, n)
		}
	} else {
		for n := range available {
			wantNames = append(wantNames, n)
		}
	}
	sort.Strings(wantNames)

	tools := make([]tool.AnyTool, 0, len(wantNames))
	for _, n := range wantNames {
		tools = append(tools, available[n])
	}
	reg, err := tool.NewRegistry(tools...)
	if err != nil {
		return nil, nil, err
	}
	return reg, wantNames, nil
}

// ---------------------------------------------------------------------
// client (ls / call)
// ---------------------------------------------------------------------

func mcpList(ctx context.Context, args []string, w io.Writer, errW io.Writer) int {
	fs := flag.NewFlagSet("mcp ls", flag.ContinueOnError)
	fs.SetOutput(errW)
	timeout := fs.Duration("timeout", 30*time.Second, "overall timeout for the connection")
	jsonOut := fs.Bool("json", false, "emit the tool list as JSON")
	front, command, err := parseClientArgs(fs, args)
	if helpRequested(err) {
		_, _ = fmt.Fprintln(w, mcpUsage)
		return 0
	}
	if err != nil {
		_, _ = fmt.Fprintf(errW, "mcp ls: %v\n\n%s\n", err, mcpUsage)
		return 64
	}

	cctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	client, cleanup, op, err := dialTarget(cctx, front, command, errW)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "mcp ls: %v\n", err)
		return 70
	}
	defer cleanup()
	if len(op) > 0 {
		_, _ = fmt.Fprintf(errW, "mcp ls: unexpected extra arguments: %v\n", op)
		return 64
	}

	tools, err := client.ListTools(cctx)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "mcp ls: %v\n", err)
		return 70
	}
	if *jsonOut {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return encodeJSONAs("mcp ls", errW, enc, tools)
	}
	info := client.ServerInfo()
	_, _ = fmt.Fprintf(w, "%s %s — %d tool(s)\n", info.Name, info.Version, len(tools))
	for _, td := range tools {
		desc := td.Description
		if desc == "" {
			desc = "(no description)"
		}
		_, _ = fmt.Fprintf(w, "  %-16s %s\n", td.Name, desc)
	}
	return 0
}

func mcpCall(ctx context.Context, args []string, w io.Writer, errW io.Writer) int {
	fs := flag.NewFlagSet("mcp call", flag.ContinueOnError)
	fs.SetOutput(errW)
	timeout := fs.Duration("timeout", 30*time.Second, "overall timeout for the call")
	front, command, err := parseClientArgs(fs, args)
	if helpRequested(err) {
		_, _ = fmt.Fprintln(w, mcpUsage)
		return 0
	}
	if err != nil {
		_, _ = fmt.Fprintf(errW, "mcp call: %v\n\n%s\n", err, mcpUsage)
		return 64
	}

	cctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	client, cleanup, op, err := dialTarget(cctx, front, command, errW)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "mcp call: %v\n", err)
		return 70
	}
	defer cleanup()

	if len(op) == 0 {
		_, _ = fmt.Fprintf(errW, "mcp call: missing <tool> name\n\n%s\n", mcpUsage)
		return 64
	}
	toolName := op[0]
	rawArgs := json.RawMessage("{}")
	switch {
	case len(op) == 2:
		if !json.Valid([]byte(op[1])) {
			_, _ = fmt.Fprintf(errW, "mcp call: arguments are not valid JSON: %s\n", op[1])
			return 64
		}
		rawArgs = json.RawMessage(op[1])
	case len(op) > 2:
		_, _ = fmt.Fprintf(errW, "mcp call: too many arguments after tool name: %v\n", op[2:])
		return 64
	}

	out, err := client.CallTool(cctx, toolName, rawArgs)
	if err != nil {
		// CallTool returns the server's error text in out as well; surface it.
		_, _ = fmt.Fprintf(errW, "mcp call: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(w, out)
	return 0
}

// parseClientArgs splits a client subcommand's args at the "--" separator
// (everything after is a subprocess command for the stdio transport),
// then parses the flags that precede it. It returns the non-flag
// positionals before "--" (front) and the subprocess command (command).
func parseClientArgs(fs *flag.FlagSet, args []string) (front, command []string, err error) {
	pre := args
	for i, a := range args {
		if a == "--" {
			pre = args[:i]
			command = args[i+1:]
			break
		}
	}
	if perr := fs.Parse(pre); perr != nil {
		return nil, nil, perr
	}
	return fs.Args(), command, nil
}

// dialTarget connects to the MCP server described by front+command and
// returns an initialized client. Exactly one of the two transports is
// used: a subprocess (command non-empty → stdio) or an http(s):// URL
// (front[0]). The returned op slice is the remaining positional tokens
// that describe the operation (tool name + JSON for call; empty for ls).
func dialTarget(ctx context.Context, front, command []string, errW io.Writer) (client *mcp.Client, cleanup func(), op []string, err error) {
	switch {
	case len(command) > 0:
		// stdio: front carries the operation positionals (tool + json).
		c, clean, derr := dialStdio(ctx, command, errW)
		if derr != nil {
			return nil, nil, nil, derr
		}
		if ierr := c.Initialize(ctx); ierr != nil {
			clean()
			return nil, nil, nil, fmt.Errorf("initialize: %w", ierr)
		}
		return c, clean, front, nil
	case len(front) > 0:
		// http: front[0] is the URL, the rest are operation positionals.
		target := front[0]
		if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
			return nil, nil, nil, fmt.Errorf("target %q is not an http(s):// URL — use \"-- <command>\" for a stdio server", target)
		}
		transport, terr := mcp.NewStreamableHTTPClientTransport(target)
		if terr != nil {
			return nil, nil, nil, terr
		}
		c := mcp.NewClient(transport, mcp.WithClientInfo(mcp.ClientInfo{Name: "galdor-cli", Version: resolvedVersion})) //nolint:contextcheck // the client's dispatch loop has its own lifecycle (ended by Close), not the per-call ctx
		if ierr := c.Initialize(ctx); ierr != nil {
			_ = c.Close()
			return nil, nil, nil, fmt.Errorf("initialize: %w", ierr)
		}
		return c, func() { _ = c.Close() }, front[1:], nil
	default:
		return nil, nil, nil, fmt.Errorf("no target: pass an http(s):// URL or \"-- <command>\"")
	}
}

// dialStdio launches command as a subprocess and wires a stdio MCP client
// to it. The subprocess's stderr is forwarded to errW so server-side
// diagnostics remain visible.
func dialStdio(ctx context.Context, command []string, errW io.Writer) (*mcp.Client, func(), error) {
	cmd := exec.CommandContext(ctx, command[0], command[1:]...) // #nosec G204 G702 -- command is supplied by the CLI user invoking their own MCP server (analogous to env/timeout/watch)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	cmd.Stderr = errW
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start %q: %w", command[0], err)
	}
	// Client reads the subprocess's stdout and writes to its stdin.
	transport := mcp.NewStdioTransport(stdout, stdin)
	client := mcp.NewClient(transport, mcp.WithClientInfo(mcp.ClientInfo{Name: "galdor-cli", Version: resolvedVersion})) //nolint:contextcheck // the client's dispatch loop has its own lifecycle (ended by Close), not the per-call ctx
	cleanup := func() {
		_ = client.Close() // closes the pipes, signaling EOF to the child
		// Give the child a moment to exit cleanly; CommandContext kills it
		// when ctx is cancelled, so this won't hang.
		_ = cmd.Wait()
	}
	return client, cleanup, nil
}
