package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os/signal"
	"syscall"

	"github.com/YasserCR/galdor/internal/store"
	"github.com/YasserCR/galdor/internal/ui"
)

// ui starts the embedded observability dashboard. The server binds
// to loopback by default — `--addr 0.0.0.0:7777` opts into LAN
// exposure deliberately, so an accidental `galdor ui` doesn't
// publish someone's traces.
func runUI(ctx context.Context, args []string, w io.Writer, errW io.Writer) int {
	fs := flag.NewFlagSet("ui", flag.ContinueOnError)
	fs.SetOutput(errW)
	db := fs.String("db", "", "path to the span store (defaults to $GALDOR_DB or ~/.galdor/traces.db)")
	addr := fs.String("addr", "127.0.0.1:7777", "address to bind the dashboard server to")
	if err := fs.Parse(args); err != nil {
		return 64
	}

	path, err := resolveDBPath(*db)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "ui: %v\n", err)
		return 70
	}
	s, err := store.OpenExisting(ctx, path)
	if err != nil {
		_, _ = fmt.Fprintf(errW, "ui: open %s: %v\n", path, err)
		return 70
	}
	defer func() { _ = s.Close() }()

	srv, err := ui.NewServer(s, ui.Options{DBPath: path})
	if err != nil {
		_, _ = fmt.Fprintf(errW, "ui: %v\n", err)
		return 70
	}

	// The dashboard has no authentication and serves captured prompts
	// and completions. Binding to anything but loopback exposes that
	// PII to the network, so warn loudly (but don't block — LAN
	// exposure can be deliberate).
	if !isLoopbackAddr(*addr) {
		_, _ = fmt.Fprintln(errW, "warning: binding to a non-loopback address with no authentication — the dashboard exposes captured prompts/completions to the network")
	}

	// Signal handling: SIGINT / SIGTERM trigger a graceful shutdown
	// inside Server.ListenAndServe.
	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := srv.ListenAndServe(sigCtx, *addr, func(actual string) {
		_, _ = fmt.Fprintf(w, "galdor scry dashboard listening on http://%s\n", actual)
		_, _ = fmt.Fprintf(w, "  db: %s\n", path)
		_, _ = fmt.Fprintln(w, "  (Ctrl-C to stop)")
	}); err != nil {
		_, _ = fmt.Fprintf(errW, "ui: %v\n", err)
		return 70
	}
	return 0
}

// isLoopbackAddr reports whether addr (a "host:port" bind address)
// resolves to the loopback interface. The empty host (":7777"), an
// explicit 0.0.0.0/:: wildcard, or any routable IP/hostname counts as
// non-loopback. Only 127.0.0.0/8, ::1, and "localhost" are treated as
// loopback. Used to decide whether to print the no-auth exposure
// warning at startup.
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// No port (or malformed): treat the whole string as the host.
		host = addr
	}
	if host == "" {
		return false // wildcard bind, e.g. ":7777"
	}
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	// A non-IP, non-localhost hostname could resolve anywhere; be safe.
	return false
}
