# Building kopusha

## Requirements

- **Go 1.25.6+** with CGO enabled. `go.mod` pins `toolchain go1.26.5`;
  the `go 1.25.6` line is the language-compat floor, so any 1.25.6+
  toolchain can consume the module.
- **Node 20+** with npm (Vite frontend build). CI builds on 24 LTS.
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
./build.sh
```

Defaults to the host platform. Output goes to `dist/` together with
`parsers.d/`, `kopusha.conf`, and `samples/` — the synthetic log
fixtures, minus the vendored `.evtx`.

Environment overrides:

| Variable | Default | Effect |
|----------|---------|--------|
| `VERSION` | current release | Stamped into `main.version` via ldflags |
| `GOOS` / `GOARCH` | host | Target platform |
| `PRODUCT_NAME` | `kopusha` | Shown in build output, passed to the sign hook |
| `MANUFACTURER` | `labmk` | Passed to the sign hook |
| `COPYRIGHT` | derived from `MANUFACTURER` | Passed to the sign hook |
| `CC` | auto-detected | C compiler override |
| `SKIP_VULNCHECK` | `0` | Set to `1` on air-gapped hosts (vuln.go.dev unreachable) |
| `MACOSX_DEPLOYMENT_TARGET` | build SDK major version | macOS minimum version |

## Supported targets

DuckDB ships prebuilt static libraries for exactly these; `build.sh`
rejects anything else up front rather than failing at link time.

| Target | Build host | C toolchain |
|--------|-----------|-------------|
| `windows/amd64` | Windows (CI) | MinGW-w64 GCC — see below |
| `linux/amd64` | Linux | system GCC |
| `linux/arm64` | Linux arm64 | system GCC |
| `darwin/arm64` | **macOS only** | Xcode Command Line Tools (clang) |

macOS support is **Apple Silicon only**. Intel Macs are not a target:
`build.sh` rejects `darwin/amd64` up front and CI does not build it.
The DuckDB bindings do ship an Intel archive, so the target is
reachable if you ever need it — add the case back to `build.sh` and
build on an arm64 Mac with `CC="clang -arch x86_64"`, since the macOS
SDK is universal. Nothing in the source is architecture-specific.

## Platform notes

### Windows

**Nobody develops this project on Windows.** The Windows binary is
produced by CI, and that is the supported path. What follows is for the
case where you need a local Windows build anyway.

The C toolchain is the only real difficulty, because you are linking
against a **prebuilt** `libduckdb_static.a` and your compiler's C++ ABI
has to match whatever built it. `build.sh` probes for a compiler, prints
which one it selected and its version, and falls through to `gcc` on
`PATH`.

What is known, as of 2026-08-01 against `duckdb-go-bindings v0.10505.0`:

| Toolchain | Result |
|-----------|--------|
| MinGW-Builds GCC 15.2.0 at `C:\mingw64` (msvcrt) | **Works.** This is the stock GitHub `windows-latest` toolchain; CI builds and passes e2e with it. |
| MSYS2 ucrt64 GCC 16.1.0 | **Fails.** `ld` crashes linking `libduckdb_static.a` — `collect2.exe: error: ld returned 5 exit status`, no symbol diagnostics. |
| TDM-GCC 10.x | Fails — too old, missing `__throw_bad_array_new_length`. |

Earlier revisions of this document asserted the reverse: that MSYS2
**ucrt64** was required and mingw64 GCC 15.x was broken by an
`_Mbstatet` ABI mismatch. That was accurate against an older bindings
release and is no longer true. Treat the table as a record of what was
last observed, not a rule — if the link step starts failing after a
`duckdb-go-bindings` bump, the working compiler may well move again.

The practical advice: install the [winlibs](https://winlibs.com/) or
MinGW-Builds GCC to `C:\mingw64`, or simply read the `C compiler:` line
`build.sh` prints and compare it against the table.

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

If you have no Mac, use CI — `.github/workflows/build.yml` builds
`darwin/arm64` on the `macos-26` (Apple Silicon) runner and uploads it
as an artifact. That leg runs only on tags and manual dispatch, because
macOS runners bill at 10x while the repository is private.

On a Mac:

```bash
xcode-select --install          # once
GOOS=darwin GOARCH=arm64 ./build.sh
```

`MACOSX_DEPLOYMENT_TARGET` defaults to the **major version of the build
SDK**, so the supported minimum is whatever macOS the build machine
targets. There is deliberately no older floor: DuckDB's prebuilt archive
and the Go runtime objects are compiled against the installed SDK, so
pinning an older target only produced a linker warning per object and a
binary that had never been verified on that older release. Set the
variable explicitly if you need a deliberate older target and are
prepared to test it.

`build.sh` applies an **ad-hoc** code signature (`codesign --sign -`),
which is enough for a binary you built yourself or copied over manually.
It is *not* enough for distribution: a binary downloaded through a
browser carries the quarantine attribute, and Gatekeeper will refuse an
ad-hoc signature. Distribution requires a Developer ID certificate plus
notarization — wire that into `hooks/sign.sh`, which receives
`$1=binary $2=product $3=version $4=manufacturer`.

Recipients of an unnotarized build can clear quarantine manually:

```bash
xattr -d com.apple.quarantine ./kopusha
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
2. `go run ./cmd/genmanifest` — rewrite `parsers.d.sha256`, the record of
   which parser rules this binary ships with. `go:embed` compiles it in,
   so the manifest inside the binary always describes the `parsers.d/`
   beside it.
3. `npm ci && npm run build` — the `prebuild` script regenerates the
   typed TS client from `swagger.json`, then Vite writes to `static/`.
4. Verify `static/index.html` exists (guards against a silent frontend
   failure producing a binary with an empty UI).
5. `go build` with `-s -w` and the version stamped in.
6. `govulncheck` (unless skipped) — reported, non-fatal by default.
7. Copy `parsers.d/` and `kopusha.conf` into `dist/`.
8. Copy `test-fixtures/formats/` into `dist/samples/` (with
   `test-fixtures/SAMPLES.md` as its `README.md`), skipping
   `sample.evtx` — the one fixture that is vendored rather than
   generated, and so is not redistributed. The release workflow puts
   the folder in every archive.
9. Code-sign hook.

## Troubleshooting

**`static/index.html not found`** — the frontend build failed. Run
`cd frontend && npm run build` directly to see the real error.

**Build hangs for ~4 minutes on first query** — something called
`INSTALL json`, which tries to reach `extensions.duckdb.org`. All
extensions are statically linked; that call should never be made.

**`go: cannot find GOROOT`** in a hook — hooks inherit the build
environment but not the MSYS2 PATH prepend. Set it explicitly.
