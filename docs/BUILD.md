# Building kopusha

## Requirements

- **Go 1.26.6 or later** with CGO enabled. `go.mod` sets `go 1.26.6`
  as the minimum and pins `toolchain go1.26.10` for builds.
- **Node 22.12 or later** with npm, for the Vite 8 UI build. CI uses
  Node 24.
- **A C compiler.** DuckDB is linked through CGO; there is no pure-Go
  build.

Optional:

- `swag` regenerates the OpenAPI spec from handler annotations. Without
  it, the build uses the committed `internal/server/docs/swagger.json`.
  Install: `go install github.com/swaggo/swag/cmd/swag@latest`
- `govulncheck` runs at the end of the build unless
  `SKIP_VULNCHECK=1`. Install:
  `go install golang.org/x/vuln/cmd/govulncheck@latest`

## Build

```bash
./build.sh                             # host platform
GOOS=linux GOARCH=arm64 ./build.sh     # another target (needs a matching C compiler)
GOOS=darwin GOARCH=arm64 ./build.sh    # macOS, on a Mac only
VERSION=1.0.0 ./build.sh               # custom version string
MANUFACTURER="Acme Corp" ./build.sh    # name passed to the signing hook
```

Output goes to `dist/`: the binary, `parsers.d/`, `kopusha.conf`,
`kopusha_mcp.conf.example` and `samples/`.

Environment variables:

| Variable | Default | Effect |
|----------|---------|--------|
| `VERSION` | current release | Written into `main.version` with `-ldflags` |
| `GOOS` / `GOARCH` | host | Target platform |
| `PRODUCT_NAME` | `kopusha` | Shown in build output, passed to the signing hook |
| `MANUFACTURER` | `labmk` | Passed to the signing hook |
| `COPYRIGHT` | built from `MANUFACTURER` | Passed to the signing hook |
| `CC` | detected | C compiler |
| `SKIP_VULNCHECK` | `0` | `1` skips `govulncheck` (for hosts that cannot reach vuln.go.dev) |
| `MACOSX_DEPLOYMENT_TARGET` | major version of the build SDK | Minimum macOS version |

## Supported targets

DuckDB provides prebuilt static libraries for these targets. `build.sh`
rejects any other target before compiling.

| Target | Build host | C compiler |
|--------|------------|------------|
| `windows/amd64` | Windows (CI) | MinGW-w64 GCC, see [Windows](#windows) |
| `linux/amd64` | Linux | system GCC |
| `linux/arm64` | Linux arm64 | system GCC |
| `darwin/arm64` | macOS only | Xcode Command Line Tools (clang) |

Intel Macs (`darwin/amd64`) are not a target: `build.sh` rejects it and
CI does not build it. The DuckDB bindings do include an Intel macOS
library and nothing in the source depends on the architecture. To build
one anyway, add the case back to `build.sh` and build on an Apple
Silicon Mac with `CC="clang -arch x86_64"`.

## Windows

Windows binaries are built by CI, and that is the supported way to get
one. The project is not developed on Windows. This section is for a
local build.

The only difficulty is the C compiler. The build links a prebuilt
`libduckdb_static.a`, so the compiler's C++ ABI must match the one that
built it. `build.sh` searches for a compiler, prints the one it chose
and its version, and otherwise uses `gcc` from `PATH`.

Last observed results, on 2026-08-01 with `duckdb-go-bindings v0.10505.0`:

| Compiler | Result |
|----------|--------|
| MinGW-Builds GCC 15.2.0 in `C:\mingw64` (msvcrt) | Works. This is the stock GitHub `windows-latest` toolchain used by CI. |
| MSYS2 ucrt64 GCC 16.1.0 | Fails: `ld` crashes linking `libduckdb_static.a` (`collect2.exe: error: ld returned 5 exit status`, no symbol errors). |
| TDM-GCC 10.x | Fails: too old, `__throw_bad_array_new_length` is missing. |

Earlier versions of this document said the opposite (ucrt64 required,
mingw64 GCC 15 broken). That was true for an older bindings release.
Treat the table as a record, not a rule: after an upgrade of the DuckDB
library modules (`go.duckdb.org/libduckdb/*`, formerly
`duckdb-go-bindings`) the working compiler can change.

In practice: install [winlibs](https://winlibs.com/) or MinGW-Builds
GCC to `C:\mingw64`, and check the `C compiler:` line that `build.sh`
prints against the table.

The binary uses the console subsystem, so starting it from Explorer
opens a console window. This is deliberate: closing the window stops
the process.

## macOS

A macOS binary must be built on macOS. CGO needs the Apple SDK, which
may not be redistributed, so there is no cross-compile from Linux or
Windows. `build.sh` detects the attempt and stops with an explanation.

Without a Mac, use CI: `.github/workflows/build.yml` builds
`darwin/arm64` on the `macos-26` runner and uploads it as an artifact.
That job runs only for tags and manual runs, because macOS runners are
billed at 10 times the Linux rate while the repository is private.

On a Mac:

```bash
xcode-select --install          # once
GOOS=darwin GOARCH=arm64 ./build.sh
```

`MACOSX_DEPLOYMENT_TARGET` defaults to the major version of the build
SDK, so the minimum supported macOS is the one the build machine
targets. There is no older default: DuckDB's library and the Go runtime
are compiled against the installed SDK, and an older target produced a
linker warning per object and a binary never tested on that release.
Set the variable yourself only if you will test on the older version.

`build.sh` signs the binary ad hoc (`codesign --sign -`). That is
enough for a binary you built or copied yourself. A binary downloaded
through a browser carries the quarantine attribute, and Gatekeeper
refuses ad-hoc signatures. Distribution needs a Developer ID
certificate and notarization, which you can add in `hooks/sign.sh`.

To run an unnotarized download:

```bash
xattr -d com.apple.quarantine ./kopusha
```

## Linux

The system GCC works. To build `linux/arm64` on an amd64 host, install
`aarch64-linux-gnu-gcc` and set `CC=aarch64-linux-gnu-gcc`. The prebuilt
DuckDB library is built against glibc, so musl systems such as Alpine
need a glibc toolchain or a glibc container.

## Build hooks

Optional executables, skipped when absent:

| Hook | When | Arguments |
|------|------|-----------|
| `hooks/pre-build.sh` | before compiling | `version os arch` |
| `hooks/sign.sh` | after compiling | `binary product version manufacturer` |
| `hooks/post-build.sh` | at the end | `binary version os arch` |

`hooks/sign.sh.example` shows a Windows `signtool` call.

## What `build.sh` does

1. `swag init`: regenerates the OpenAPI spec (skipped without `swag`).
2. `go run ./cmd/genmanifest`: rewrites `parsers.d.sha256`, the list of
   rules this binary ships with. It is compiled in with `go:embed`, so
   the manifest in the binary always matches the `parsers.d/` next to
   it.
3. `npm ci && npm run build` in `frontend/`. The `prebuild` script
   regenerates the TypeScript client from `swagger.json`; Vite writes
   to `static/`.
4. Checks that `static/index.html` exists, so a failed UI build cannot
   produce a binary with an empty UI.
5. `go build` with `-s -w` and the version.
6. Ad-hoc code signature on macOS.
7. `govulncheck` (unless skipped). Findings are printed; they do not
   stop the build.
8. Copies `parsers.d/`, `kopusha.conf` and `kopusha_mcp.conf.example`
   into `dist/`.
9. Copies `sample.ndjson`, `sample.parquet`, `xml-row-element.txt` and
   `unmatched.log` from `test-fixtures/formats/` into `dist/samples/`,
   with `test-fixtures/SAMPLES.md` as `README.md`. The CI release job
   puts this folder in every archive.
10. Runs `hooks/sign.sh`, then `hooks/post-build.sh`.

## CI and releases

`.github/workflows/build.yml` runs on pushes and pull requests to
`main`, on `v*` tags, and manually. It runs `gofmt` and `go vet`, builds
each target on a native runner, runs the Go tests on every target and
the e2e tests on Linux amd64 and Windows, and runs `govulncheck` and
`npm audit`. Ordinary pushes build Linux and Windows only; tags and
manual runs also build macOS.

For a `v*` tag, the release job packs each platform into
`kopusha-<version>-<os>-<arch>.zip` (binary, `parsers.d/`, config
files, `samples/`, `LICENSE`, `README.md`, `THIRD-PARTY-NOTICES.md`,
`sbom.cdx.json`), writes `SHA256SUMS`, creates a build attestation for
each zip and publishes the GitHub release. See
[SECURITY.md](../SECURITY.md#verifying-a-download).

## Troubleshooting

**`static/index.html not found`**: the UI build failed. Run
`cd frontend && npm run build` to see the error.

**The first query hangs for about 4 minutes**: something ran DuckDB
`INSTALL json`, which tries to reach `extensions.duckdb.org`. All
extensions are linked in; that statement must never run.

**`go: cannot find GOROOT` in a hook**: hooks inherit the build
environment but not changes made to `PATH` inside the MSYS2 shell. Set
it in the hook.
