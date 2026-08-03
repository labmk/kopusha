// Package module defines the interface optional kopusha sub-features
// implement to plug into the core viewer. No modules ship by default;
// see docs/MODULES.md for the contract and a worked example.
//
// Boot flow:
//
//  1. main.go loads kopusha.conf into a *config.Config.
//  2. main.go creates a Registry with the runtime Deps, calls Add(m) for
//     each module the build ships with, and calls Boot(mux).
//  3. Boot iterates modules in Add() order and consults Enabled(cfg) on
//     each. False means the module is skipped entirely: no routes, no
//     manifest entry, no mention to the SPA.
//  4. For each enabled module, Register(ctx) is called once. The module
//     mounts its backend routes on ctx.Mux and optionally fills
//     ctx.Manifest so the SPA can mount its frontend half (tab,
//     component, style, config).
//  5. Boot registers GET /api/modules, which returns the manifests of
//     enabled modules in Add() order. The SPA reads this on startup.
package module

import (
	"net/http"

	"github.com/labmk/kopusha/internal/config"
	"github.com/labmk/kopusha/internal/engine"
	"github.com/labmk/kopusha/internal/settings"
)

// Module is implemented by every optional sub-feature.
type Module interface {
	// Name is the registry ID. Used as the default Manifest.ID and shown
	// in startup logs. Must be unique within a build. Convention:
	// kebab-case ("my-module").
	Name() string

	// Enabled inspects the loaded config and returns whether this module
	// should be mounted. Typical implementation:
	//   return cfg.ModuleEnabled("my-module")
	Enabled(cfg *config.Config) bool

	// Register is called once at boot when Enabled returned true. The
	// module mounts its HTTP routes on ctx.Mux and optionally fills in
	// ctx.Manifest fields it wants exposed to the SPA.
	Register(ctx *RegisterContext) error
}

// Deps is the runtime wiring the registry exposes to every enabled
// module via RegisterContext. Fields are append-only.
//
// Concrete types are used (not interfaces) for clarity at this stage —
// the kopusha code base is small enough that the indirection cost of
// abstract interfaces would outweigh the testability win.
type Deps struct {
	// Engine is the DuckDB engine the viewer uses. May be nil in tests;
	// modules that need it should fail fast in Register.
	Engine *engine.Engine

	// Settings is the persistent settings store (saved queries, last
	// directory, per-module state, …). May be nil in tests.
	Settings *settings.Store

	// APIHandler wraps a handler with cross-cutting concerns (currently:
	// touch activity timer). Modules SHOULD wrap their handlers with
	// this when registering routes, mirroring core viewer routes.
	APIHandler func(http.HandlerFunc) http.HandlerFunc

	// TouchActivity records "the viewer is doing something" — modules
	// MUST call this from any long-running handler (SSE loops, fetches)
	// so the inactivity-timeout auto-shutdown doesn't kill them.
	TouchActivity func()

	// AddBusyCheck registers a predicate the inactivity-timeout loop
	// consults before shutting down. Return true while your module is
	// in the middle of work the operator would lose (an active SSH
	// session, an in-flight upload, …).
	AddBusyCheck func(func() bool)
}

// RegisterContext carries everything a module needs at Register time.
// Embeds Deps so module code can write ctx.Engine, ctx.Settings, … as
// if they were fields on the context.
type RegisterContext struct {
	Deps

	// Mux is the same ServeMux the core viewer uses. Module routes
	// should live under /api/<name>/* and /m/<name>/* to avoid
	// collisions with core paths.
	Mux *http.ServeMux

	// Manifest is pre-allocated with ID = module.Name(). The module
	// fills in optional fields (Tab, Bundle, Style, Config) it wants
	// the SPA to see via /api/modules.
	Manifest *Manifest

	// Config is the parsed kopusha.conf. Modules can read their
	// own section via cfg.Section("<name>").
	Config *config.Config
}

// Manifest is the per-module payload returned by /api/modules. JSON
// marshaling uses omitempty so only relevant fields appear per module.
type Manifest struct {
	// ID is the module's unique identifier, defaults to Module.Name().
	ID string `json:"id"`

	// Tab describes a top-level tab the SPA should show for this module.
	// Omit for modules that don't surface a tab (e.g. a theme module).
	Tab *TabEntry `json:"tab,omitempty"`

	// Bundle is the URL of the module's frontend JS bundle, served by
	// the module itself (e.g. "/m/my-module/index.js"). Omit for
	// modules with no JS (e.g. one contributing CSS + data only).
	Bundle string `json:"bundle,omitempty"`

	// Style is the URL of a CSS stylesheet the SPA should inject
	// (e.g. "/m/branding/brand.css"). Omit if not needed.
	Style string `json:"style,omitempty"`

	// Config is arbitrary module-specific configuration the SPA reads
	// directly. Used by branding for company name, link, logo URL, etc.
	Config map[string]any `json:"config,omitempty"`
}

// TabEntry describes a tab a module wants the SPA to mount.
type TabEntry struct {
	Label string `json:"label"`
	Route string `json:"route"`
}
