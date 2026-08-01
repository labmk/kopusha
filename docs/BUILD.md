# Building obs-viewer

## Requirements

- **Go 1.25.6+** with CGO enabled. `go.mod` pins `toolchain go1.26.2`;
  the `go 1.25.6` line is the language-compat floor, so any 1.25.6+
  toolchain can consume the module.
- **Node 18+** with npm (Vite frontend build).
- **A C compiler.** DuckDB is a CGO dependency — there is no pure-Go
  fallback.

Optional:

- `swag` — regenerates the OpenAPI spec from handler annotations.
  Without it the build uses the committed `internal/server/docs/swagger.json`.
  `go install github.com/swaggo/swag/cmd/swag@latest`
- `govulncheck` — run automatically at the end of the build unless
  `SKIP_VULNCHECK=1`. `go install golang.org/x/vuln/cmd/govulncheck@latest`

## Quick start

```bash
chmod +x build.sh
./build.sh
```

Defaults to the host platform. Output goes to `dist/` together with
`parsers.d/` and `obs_viewer.conf`.

Environment overrides:

| Variable | Default | Effect |
|----------|---------|--------|
| `VERSION` | current release | Stamped into `main.version` via ldflags |
| `GOOS` / `GOARCH` | host | Target platform |
| `PRODUCT_NAME` | `obs_viewer` | Shown in build output, passed to the sign hook |
| `MANUFACTURER` | `obs-viewer contributors` | Passed to the sign hook |
| `COPYRIGHT` | derived from `MANUFACTURER` | Passed to the sign hook |
| `CC` | auto-detected | C compiler override |
| `SKIP_VULNCHECK` | `0` | Set to `1` on air-gapped hosts (vuln.go.dev unreachable) |
| `MACOSX_DEPLOYMENT_TARGET` | `11.0` | macOS minimum version |

## Supported targets

DuckDB ships prebuilt static libraries for exactly these; `build.sh`
rejects anything else up front rather than failing at link time.

| Target | Build host | C toolchain |
|--------|-----------|-------------|
| `windows/amd64` | Windows | MSYS2 **ucrt64** GCC |
| `linux/amd64` | Linux | system GCC |
| `linux/arm64` | Linux arm64 | system GCC |
| `darwin/arm64` | **macOS only** | Xcode Command Line Tools (clang) |
| `darwin/amd64` | **macOS only** | Xcode Command Line Tools (clang) |

## Platform notes

### Windows

`build.sh` auto-detects MSYS2 ucrt64 GCC at
`/c/msys64/ucrt64/bin/gcc.exe`. Two toolchains that look like they
should work but don't:

- **TDM-GCC 10.x** — too old; missing `__throw_bad_array_new_length`.
- **MSYS2 mingw64 GCC 15.x** — `_Mbstatet` ABI mismatch against the
  prebuilt `libduckdb_static.a`.

Both fail at link time with symbol errors that don't obviously point at
the compiler choice. If you see either symbol, you're on the wrong GCC.

Install the right one:

```bash
pacman -S mingw-w64-ucrt-x86_64-gcc
```

The binary is built for the console subsystem, so launching it from
Explorer opens a console window. That's deliberate — closing the window
kills the process, which is the clearest possible "I'm done" gesture.

### macOS

**A macOS binary must be built on macOS.** CGO needs a C toolchain
targeting Darwin, and that requires the Apple SDK, which Apple does not
permit redistributing. There is no supported cross-compile path from
Linux or Windows. `build.sh` detects the attempt and fails early with an
explanation rather than surfacing a confusing
`gcc: unrecognized command-line option '-arch'` from `runtime/cgo`.

If you have no Mac, use CI — `.github/workflows/build.yml` builds both
macOS targets on the `macos-14` (Apple Silicon) runner and uploads them
as artifacts. `darwin/amd64` is cross-built there with
`CC="clang -arch x86_64"`: the macOS SDK is universal, so an arm64 host
can target Intel even though no *other* host can target Darwin at all.
The same trick works locally on an Apple Silicon Mac.

On a Mac:

```bash
xcode-select --install          # once
GOOS=darwin GOARCH=arm64 ./build.sh
```

`MACOSX_DEPLOYMENT_TARGET` defaults to `11.0` so the binary runs on
releases older than the build machine.

`build.sh` applies an **ad-hoc** code signature (`codesign --sign -`),
which is enough for a binary you built yourself or copied over manually.
It is *not* enough for distribution: a binary downloaded through a
browser carries the quarantine attribute, and Gatekeeper will refuse an
ad-hoc signature. Distribution requires a Developer ID certificate plus
notarization — wire that into `hooks/sign.sh`, which receives
`$1=binary $2=product $3=version $4=manufacturer`.

Recipients of an unnotarized build can clear quarantine manually:

```bash
xattr -d com.apple.quarantine ./obs_viewer
```

### Linux

Nothing special — system GCC works. Building `linux/arm64` on an amd64
host needs `aarch64-linux-gnu-gcc` and `CC=aarch64-linux-gnu-gcc`; the
prebuilt DuckDB archive for that target is glibc-based, so musl hosts
(Alpine) need a glibc toolchain or a compatible container.

## Build hooks

Three optional executables, all skipped if absent:

| Hook | When | Arguments |
|------|------|-----------|
| `hooks/pre-build.sh` | before compilation | `version os arch` |
| `hooks/sign.sh` | after compilation | `binary product version manufacturer` |
| `hooks/post-build.sh` | last | `binary version os arch` |

`hooks/sign.sh.example` sketches a Windows `signtool` invocation.

## What the build does

1. `swag init` — regenerate the OpenAPI spec (skipped if `swag` absent).
2. `npm ci && npm run build` — the `prebuild` script regenerates the
   typed TS client from `swagger.json`, then Vite writes to `static/`.
3. Verify `static/index.html` exists (guards against a silent frontend
   failure producing a binary with an empty UI).
4. `go build` with `-s -w` and the version stamped in.
5. `govulncheck` (unless skipped) — reported, non-fatal by default.
6. Copy `parsers.d/` and `obs_viewer.conf` into `dist/`.
7. Code-sign hook.

## Troubleshooting

**`static/index.html not found`** — the frontend build failed. Run
`cd frontend && npm run build` directly to see the real error.

**Build hangs for ~4 minutes on first query** — something called
`INSTALL json`, which tries to reach `extensions.duckdb.org`. All
extensions are statically linked; that call should never be made.

**`go: cannot find GOROOT`** in a hook — hooks inherit the build
environment but not the MSYS2 PATH prepend. Set it explicitly.
