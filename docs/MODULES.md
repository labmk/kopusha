# Writing an obs-viewer module

A module is an optional sub-feature that plugs into the core viewer
without the core knowing anything about it. A module can contribute any
combination of:

- **HTTP handlers** under `/api/<name>/*`
- **Static assets** under `/m/<name>/*` (CSS, images, JS bundles)
- **A React tab** rendered alongside the built-in viewer tab
- **Config data** passed to the SPA at boot (a branding module, say,
  supplying a logo URL and company name)

No modules ship with obs-viewer. This document is the contract for
adding one.

## Lifecycle

1. `main.go` loads all `obs_viewer*.conf` files and merges them into one
   `config.Config`.
2. `module.NewRegistry(cfg, deps)` is constructed, then each module is
   added with `modreg.Add(yourmodule.New())`.
3. `modreg.Boot(mux)` calls `Enabled(cfg)` on each. Modules that return
   `false` are skipped entirely — no routes, no manifest entry.
4. For each enabled module, `Register(ctx)` runs once. It mounts routes
   on `ctx.Mux` and fills `ctx.Manifest`.
5. The SPA fetches `/api/modules` at boot and uses the manifest to
   decide which tabs to render, which stylesheets to inject, and what
   config values to apply.

A module is enabled **iff** its config section exists. That is the only
on/off switch — there is no build flag.

## The interface

```go
type Module interface {
    Name() string                      // registry ID, kebab-case
    Enabled(cfg *config.Config) bool    // usually cfg.ModuleEnabled(Name())
    Register(ctx *RegisterContext) error
}
```

`RegisterContext` embeds `Deps`, so `Register` can reach:

| Field | Use |
|-------|-----|
| `Mux` | Mount your handlers |
| `Config` | Read your `[section]` via `ctx.Config.Section(name)` |
| `Manifest` | Fill `Tab`, `Style`, `Bundle`, `Config` |
| `Engine` | Query loaded files |
| `Settings` | Persist state (add a field to `settings.Settings`) |
| `APIHandler` | Wrap handlers so they refresh the inactivity timer |
| `TouchActivity` | Call from long-running handlers to keep the timer fresh |
| `AddBusyCheck` | Register a `func() bool` so auto-shutdown pauses during your work |

## Directory layout

```
modules/my-module/
  backend/            # Go — only if the module has both halves
    module.go         # package mymodule (Go-valid identifier, no dashes)
    handlers.go
  frontend/
    MyModuleTab.jsx
```

Data-only modules (assets and CSS, no handlers) flatten into
`modules/my-module/` directly. Go's `embed` cannot escape upward with
`..`, so any embedded file must live beside the source that embeds it.

## Minimal example

```go
// modules/hello/module.go
package hello

import (
    "embed"
    "net/http"

    "github.com/labmk/obs-viewer/internal/config"
    "github.com/labmk/obs-viewer/internal/module"
)

//go:embed all:assets
var assetFS embed.FS

type Module struct{}

func New() *Module { return &Module{} }

func (m *Module) Name() string { return "hello" }

func (m *Module) Enabled(cfg *config.Config) bool {
    return cfg.ModuleEnabled("hello")
}

func (m *Module) Register(ctx *module.RegisterContext) error {
    ctx.Mux.Handle("/m/hello/", http.StripPrefix("/m/hello/",
        http.FileServer(http.FS(assetFS))))

    ctx.Mux.HandleFunc("/api/hello/ping", ctx.APIHandler(
        func(w http.ResponseWriter, r *http.Request) {
            w.Header().Set("Content-Type", "application/json")
            _, _ = w.Write([]byte(`{"pong":true}`))
        }))

    section, _ := ctx.Config.Section("hello")
    ctx.Manifest.Tab = &module.TabEntry{Label: "Hello", Route: "hello"}
    ctx.Manifest.Config = map[string]any{"greeting": section["greeting"]}
    return nil
}
```

## Wiring it up

Four edits, all one-liners:

1. **`main.go`** — import the package and add
   `modreg.Add(hello.New())` before `modreg.Boot(...)`.

2. **`frontend/src/moduleRegistry.js`** — import the tab component and
   map the module ID to it:

   ```js
   import HelloTab from '../../modules/hello/frontend/HelloTab';
   export const moduleComponents = { hello: HelloTab };
   ```

3. **`frontend/vite.config.js`** — add a `resolve.alias` entry for
   every external package your JSX imports. Module files live outside
   the Vite project root, so Rollup's `node_modules` walk-up never
   reaches `frontend/node_modules` and the import will fail to resolve
   even though the package is installed.

4. **`obs_viewer_hello.conf`** — ship the config section. If it needs
   per-site values, ship it as `obs_viewer_hello.conf.example` instead;
   the `.example` suffix is deliberately excluded from the startup glob,
   so the operator opts in by renaming.

   ```ini
   [hello]
   enabled = true
   greeting = hi
   ```

## Removing a module

Delete `modules/<name>/`, drop the import and `modreg.Add(...)` line in
`main.go`, drop the `moduleRegistry.js` entry, delete the conf file, and
rebuild. Nothing in the core references a module by name.

## Conventions

- Route prefixes `/api/<name>/` and `/m/<name>/` are disjoint from every
  core path, so the SPA's `/` catch-all never shadows them. Stay inside
  your prefix.
- Wrap handlers in `ctx.APIHandler` unless you have a reason not to —
  otherwise requests to your module won't refresh the inactivity timer
  and the server may shut down under an active user.
- If your module does work that outlasts a request (a long transfer, a
  subprocess), register an `AddBusyCheck` predicate so auto-shutdown
  doesn't fire mid-operation.
- Manifest fields are all optional. A tab-only module fills `Tab`; a
  theme-only module fills `Style` + `Config`; both patterns compose.
