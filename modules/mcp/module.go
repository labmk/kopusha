// Package mcp exposes the loaded log set to AI agents over the Model
// Context Protocol. It is the first module built on internal/module and
// adds no frontend: an agent needs the parser and the query engine, not
// the UI.
//
// Transport is MCP "Streamable HTTP" in its simplest form — one POST
// endpoint answering each JSON-RPC request with a JSON body, no SSE
// stream, no session. It is mounted on the viewer's own listener at
// /api/mcp and refuses anything that is not from this machine: the
// remote address must be loopback, and the Host and Origin headers must
// name loopback too, which is what defeats DNS rebinding from a web page
// open in a local browser. Nothing in this package makes an outbound
// connection.
//
// The tool set is read-only with respect to disk. An agent can load
// files into the session and query them, but cannot export, write
// parser rules, or change settings. Zip archives are refused because
// loading one extracts it next to the archive.
package mcp

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/labmk/kopusha/internal/config"
	"github.com/labmk/kopusha/internal/engine"
	"github.com/labmk/kopusha/internal/module"
	"github.com/labmk/kopusha/internal/parsers"
)

// Name is the registry ID and the config section that enables the module.
const Name = "mcp"

// Endpoint is the single MCP route.
const Endpoint = "/api/mcp"

const (
	defaultMaxRows   = 500
	defaultQueryRows = 50
	defaultSampleCap = 30
	maxSampleCap     = 500
)

// Module implements module.Module.
type Module struct {
	version string
}

// New returns the module for registration in main.go. version is the
// kopusha version, reported to clients in the initialize reply.
func New(version string) *Module { return &Module{version: version} }

// Name implements module.Module.
func (m *Module) Name() string { return Name }

// Enabled implements module.Module: on when [mcp] is present.
func (m *Module) Enabled(cfg *config.Config) bool { return cfg.ModuleEnabled(Name) }

// Register mounts /api/mcp.
func (m *Module) Register(ctx *module.RegisterContext) error {
	if ctx.Engine == nil {
		return fmt.Errorf("mcp: engine is required")
	}
	section, _ := ctx.Config.Section(Name)
	opts, err := parseOptions(section)
	if err != nil {
		return err
	}
	h := newHandler(ctx.Engine, ctx.Rules, opts, m.version)
	handle := h.ServeHTTP
	if ctx.APIHandler != nil {
		// Agent traffic counts as activity, so the inactivity timeout
		// does not shut the server down mid-conversation.
		handle = ctx.APIHandler(handle)
	}
	ctx.Mux.HandleFunc(Endpoint, handle)
	ctx.Manifest.Config = map[string]any{"endpoint": Endpoint}
	return nil
}

// Options is the parsed [mcp] section.
type Options struct {
	// Token, when set, must arrive as "Authorization: Bearer <token>".
	// Loopback-only already keeps other machines out; the token keeps
	// other local users out on a shared host.
	Token string
	// MaxRows caps the limit a query tool call may request.
	MaxRows int
	// AllowLoad exposes load_directory. Off means the agent can only
	// query what the operator loaded.
	AllowLoad bool
}

func parseOptions(s map[string]string) (Options, error) {
	o := Options{MaxRows: defaultMaxRows, AllowLoad: true}
	o.Token = strings.TrimSpace(s["token"])
	if v := strings.TrimSpace(s["max_rows"]); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return o, fmt.Errorf("mcp: max_rows must be a positive integer, got %q", v)
		}
		o.MaxRows = n
	}
	if v, ok := s["allow_load"]; ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "0", "false", "no", "off":
			o.AllowLoad = false
		case "1", "true", "yes", "on", "":
		default:
			return o, fmt.Errorf("mcp: allow_load must be true or false, got %q", v)
		}
	}
	return o, nil
}

// handler holds what the tools need. rules may be nil in a build
// without parsers.d; list_formats and explain_detection then report
// that rather than failing the whole server.
type handler struct {
	eng     *engine.Engine
	rules   *parsers.Manager
	opts    Options
	version string
	tools   []tool
}

func newHandler(eng *engine.Engine, rules *parsers.Manager, opts Options, version string) *handler {
	h := &handler{eng: eng, rules: rules, opts: opts, version: version}
	h.tools = h.buildTools()
	return h
}
