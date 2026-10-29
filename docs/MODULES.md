# Writing a kopusha module

A module is an optional feature added to kopusha without the core code
knowing about it. A module can provide any of:

- HTTP handlers under `/api/<name>/`;
- static files (CSS, images, JavaScript) under `/m/<name>/`;
- a React tab shown next to the main viewer tab;
- configuration values passed to the UI at startup (for example a logo
  URL for a branding module).

One module ships with kopusha: `mcp` in `modules/mcp/`, which serves
the MCP endpoint for AI agents ([MCP.md](./MCP.md)). It has handlers and
no tab, and is the reference for that kind of module.

## On and off

A module is enabled when its `[<name>]` section exists in one of the
`kopusha*.conf` files next to the binary, and the section does not set
`enabled` to `false`, `0`, `no`, `off` or an empty value. There is no
build flag.

## Lifecycle

1. `main.go` reads all `kopusha*.conf` files into one `config.Config`.
2. `module.NewRegistry(cfg, deps)` creates the registry, and each module
   is added with `modreg.Add(yourmodule.New())`.
3. `modreg.Boot(mux)` calls `Enabled(cfg)` on each module. A module that
   returns `false` gets no routes and no manifest entry.
4. For each enabled module, `Register(ctx)` runs once. It mounts routes
   on `ctx.Mux` and fills `ctx.Manifest`.
5. The UI fetches `/api/modules` at startup and uses the manifests to
   decide which tabs to show, which stylesheets to load and which
   config values to apply.

## Interface

```go
type Module interface {
    Name() string                       // registry ID, kebab-case
    Enabled(cfg *config.Config) bool    // usually cfg.ModuleEnabled(Name())
    Register(ctx *RegisterContext) error
}
```

`RegisterContext` embeds `Deps`, so `Register` can use:

| Field | Use |
|-------|-----|
| `Mux` | Mount handlers |
| `Config` | Read your section with `ctx.Config.Section(name)` |
| `Manifest` | Set `Tab`, `Style`, `Bundle`, `Config` |
| `Engine` | Query loaded files |
| `Rules` | The `parsers.d/` manager: current adapter registry and rule list |
| `Settings` | Store state (add a field to `settings.Settings`) |
| `APIHandler` | Wrap a handler so requests reset the inactivity timer |
| `TouchActivity` | Reset the inactivity timer from long-running work |
| `AddBusyCheck` | Register a `func() bool`; auto-shutdown waits while it returns true |

Modules reach the core only through these fields. They do not import
`internal/server`.

## Folder layout

```
modules/my-module/
  backend/            # Go, only when the module has both a backend and a UI part
    module.go         # package mymodule (a valid Go name, no dashes)
    handlers.go
  frontend/
    MyModuleTab.jsx
```

A module with only data (assets and CSS) or only a backend (like
`modules/mcp/`) puts its files directly in `modules/my-module/`. Go's
`embed` cannot reach parent folders with `..`, so embedded files must
sit next to the Go file that embeds them.

## Example

```go
// modules/hello/module.go
package hello

import (
    "embed"
    "net/http"

    "github.com/labmk/kopusha/internal/config"
    "github.com/labmk/kopusha/internal/module"
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

1. **`main.go`:** import the package and call `modreg.Add(hello.New())`
   before `modreg.Boot(...)`. Pass any values the module needs from
   `main`, as in `mcp.New(version)`.
2. **`frontend/src/moduleRegistry.js`** (modules with a tab only):
   import the tab component and map the module ID to it.

   ```js
   import HelloTab from '../../modules/hello/frontend/HelloTab';
   export const moduleComponents = { hello: HelloTab };
   ```

3. **`frontend/vite.config.js`** (modules with a tab only): add a
   `resolve.alias` entry for every npm package the module's JSX imports.
   Module files are outside the Vite project folder, so the bundler does
   not find `frontend/node_modules` on its own and the import fails even
   though the package is installed.
4. **`kopusha_hello.conf`:** ship the config section. If it needs
   site-specific values, ship `kopusha_hello.conf.example` instead.
   Files ending in `.example` are not read, so the operator enables the
   module by renaming the file.

   ```ini
   [hello]
   greeting = hi
   ```

   To ship the section turned off, add `enabled = false`.

## Removing a module

Delete `modules/<name>/`, remove its import and `modreg.Add(...)` line
from `main.go`, remove its entry in `moduleRegistry.js`, delete its
config file, and rebuild. The core does not refer to modules by name.

## Rules

- Keep routes under `/api/<name>/` and `/m/<name>/`. These prefixes do
  not overlap with core routes, so the UI's catch-all route `/` never
  hides them.
- Wrap handlers in `ctx.APIHandler`. Otherwise requests to your module
  do not count as activity, and the server can shut down while someone
  is using it.
- If the module does work that outlasts a request (a long transfer, a
  subprocess), register an `AddBusyCheck` function so auto-shutdown
  waits.
- All manifest fields are optional. A tab-only module sets `Tab`; a
  theme module sets `Style` and `Config`; both can be combined.
- Module UI code is bundled with the main UI for now. The `Bundle`
  manifest field is reserved for separately built bundles.
