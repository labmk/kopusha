package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/labmk/kopusha/internal/config"
	"github.com/labmk/kopusha/modules/mcp"
)

// runMCPBridge implements `kopusha mcp`: the standard input/output
// transport for agents that only launch MCP servers as subprocesses.
// It forwards to the /api/mcp endpoint of a kopusha already running on
// this machine (modules/mcp.Bridge), so it starts no engine and opens
// no listener of its own.
//
// Standard output is the protocol channel. Nothing else may be printed
// there, which is why this runs before attachParentConsole and logging
// setup in main, and why every diagnostic goes to standard error.
func runMCPBridge(args []string) int {
	fs := flag.NewFlagSet("kopusha mcp", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	url := fs.String("url", "", "MCP endpoint (default http://127.0.0.1:<port>/api/mcp, port from kopusha.conf)")
	port := fs.Int("port", 0, "Port of the running kopusha (overrides kopusha.conf)")
	token := fs.String("token", "", "Bearer token (default: [mcp] token from kopusha_mcp.conf)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: kopusha mcp [--port N | --url URL] [--token T]\n\n"+
			"Speaks MCP over standard input/output and forwards to a running kopusha\n"+
			"with the mcp module enabled. See docs/MCP.md.\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	// Same config files the viewer reads, so the defaults match the
	// instance an operator started from the same directory.
	var cfg *config.Config
	if exe, err := os.Executable(); err == nil {
		paths, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), "kopusha*.conf"))
		sort.Strings(paths)
		if c, err := config.LoadAll(paths); err == nil {
			cfg = c
		} else {
			fmt.Fprintf(os.Stderr, "kopusha mcp: config: %v\n", err)
		}
	}

	endpoint := strings.TrimSpace(*url)
	if endpoint == "" {
		p := *port
		if p == 0 && cfg != nil {
			p = cfg.GetInt("port", 9200)
		}
		if p == 0 {
			p = 9200
		}
		endpoint = fmt.Sprintf("http://127.0.0.1:%d%s", p, mcp.Endpoint)
	}
	tok := *token
	if tok == "" && cfg != nil {
		if s, ok := cfg.Section(mcp.Name); ok {
			tok = strings.TrimSpace(s["token"])
		}
	}

	// No signal handling: Run blocks reading standard input, so the
	// default behaviour (an interrupt ends the process) is the right one.
	b := &mcp.Bridge{URL: endpoint, Token: tok, Log: os.Stderr}
	fmt.Fprintf(os.Stderr, "kopusha mcp: forwarding standard input/output to %s\n", endpoint)
	if err := b.Run(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "kopusha mcp: %v\n", err)
		return 1
	}
	return 0
}
