#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

VERSION="${VERSION:-0.1.0}"
# Default to the host platform. Set GOOS/GOARCH to cross-compile —
# see docs/BUILD.md for which combinations actually work (CGO makes
# most of them require a matching C cross-toolchain).
TARGET_OS="${GOOS:-$(go env GOHOSTOS)}"
TARGET_ARCH="${GOARCH:-$(go env GOHOSTARCH)}"
HOST_OS="$(go env GOHOSTOS)"
OUTPUT_NAME="obs_viewer"

if [ "$TARGET_OS" = "windows" ]; then
    OUTPUT_NAME="obs_viewer.exe"
fi

# Supported targets. DuckDB ships prebuilt static libs for more than
# these — darwin/amd64 among them — but Intel macOS is deliberately not
# a supported target for this project, so it is rejected here rather
# than half-working. Anything else fails at link time with a
# missing-archive error rather than something diagnosable.
case "${TARGET_OS}/${TARGET_ARCH}" in
    windows/amd64|linux/amd64|linux/arm64|darwin/arm64) ;;
    darwin/amd64)
        echo "ERROR: darwin/amd64 (Intel macOS) is not a supported target." >&2
        echo "       macOS support is Apple Silicon only. The DuckDB" >&2
        echo "       bindings do ship an Intel archive, so if you need it," >&2
        echo "       add darwin/amd64 to this case and build with" >&2
        echo "       CC=\"clang -arch x86_64\" on an arm64 Mac." >&2
        exit 1
        ;;
    *)
        echo "ERROR: unsupported target ${TARGET_OS}/${TARGET_ARCH}." >&2
        echo "       Supported: windows/amd64, linux/amd64, linux/arm64," >&2
        echo "       darwin/arm64." >&2
        exit 1
        ;;
esac

# CGO cross-compilation needs a C toolchain that targets the host's
# *target*, not the host itself. Building for macOS requires the Apple
# SDK, which cannot be redistributed — so darwin builds must run on
# macOS. Fail early with a useful message instead of a confusing
# "gcc: unrecognized command-line option '-arch'" from the Go runtime.
if [ "$TARGET_OS" = "darwin" ] && [ "$HOST_OS" != "darwin" ]; then
    echo "ERROR: darwin/${TARGET_ARCH} must be built on macOS." >&2
    echo "       CGO is required (DuckDB) and the Apple SDK is not" >&2
    echo "       redistributable, so there is no supported cross path" >&2
    echo "       from ${HOST_OS}. Use a macOS machine or the" >&2
    echo "       macos-26 runner in .github/workflows/build.yml." >&2
    exit 1
fi

# ---------------------------------------------------------------------------
# Branding
# ---------------------------------------------------------------------------
# Override any of these from the environment to brand a fork, e.g.
#   MANUFACTURER="Acme Corp" ./build.sh
PRODUCT_NAME="${PRODUCT_NAME:-obs_viewer}"
MANUFACTURER="${MANUFACTURER:-labmk}"
COPYRIGHT="${COPYRIGHT:-Copyright (c) $(date +%Y) ${MANUFACTURER}. MIT licensed.}"

echo "=== ${PRODUCT_NAME} build ==="
echo "Version:      $VERSION"
echo "Manufacturer: $MANUFACTURER"
echo "Target:       ${TARGET_OS}/${TARGET_ARCH}"
echo ""

# ---------------------------------------------------------------------------
# Hook: pre-build (custom actions before compilation)
# ---------------------------------------------------------------------------
if [ -x "${SCRIPT_DIR}/hooks/pre-build.sh" ]; then
    echo "[hook] Running pre-build..."
    "${SCRIPT_DIR}/hooks/pre-build.sh" "$VERSION" "$TARGET_OS" "$TARGET_ARCH"
fi

# Step 0: Regenerate OpenAPI spec from swag annotations so the TS client
# the frontend `prebuild` script generates is up to date with the Go
# handlers. swag CLI lives at ~/go/bin/swag after `go install
# github.com/swaggo/swag/cmd/swag@latest`. Skipped (with a notice) when
# the binary isn't on PATH — useful for CI images that prebuild docs/.
if command -v swag &>/dev/null; then
    echo "[0/4] Regenerating OpenAPI spec (swag init)..."
    swag init -g main.go --output internal/server/docs --parseInternal --parseDependency >/dev/null 2>&1 \
        && echo "  Spec written to internal/server/docs/" \
        || echo "  swag init reported issues — see internal/server/docs/"
else
    echo "[0/4] swag not found on PATH; skipping spec regeneration."
    echo "       Install: go install github.com/swaggo/swag/cmd/swag@latest"
fi

# Step 1: Build frontend
echo "[1/4] Building frontend..."
cd frontend
echo "  Installing dependencies (locked via package-lock.json)..."
# npm ci (not install) — refuses to drift from the lockfile, which
# prevents silent upgrades to transitive packages with fresh CVEs
# between developer machines and CI.
#
# Run unconditionally. Gating this on `[ ! -d node_modules ]` looked
# like a speed-up but meant a stale tree was never repaired: several
# dependencies (rollup, esbuild) resolve to per-platform native
# packages, so a node_modules copied between machines — or carried
# along in a directory copy — fails at build time with a
# "Cannot find module @rollup/rollup-<os>-<arch>" that points nowhere
# useful. npm ci wipes and reinstalls, which is the behaviour that
# makes the lockfile meaningful. CI never hit this because a fresh
# checkout has no node_modules at all.
npm ci --silent
npm run build --silent
cd ..

# Vite builds with emptyOutDir, which wipes static/ — including the
# committed .gitkeep. That file is load-bearing: `//go:embed static/*`
# fails to resolve on a fresh clone if the directory has no tracked
# contents, so `go vet`, `go test` and `go run .` all break before the
# frontend has ever been built. Put it back so a build never leaves the
# tree in a state that a fresh clone could not reproduce.
touch static/.gitkeep
echo "  Frontend built -> static/"

# Step 2: Verify static output
if [ ! -f static/index.html ]; then
    echo "ERROR: static/index.html not found after frontend build"
    exit 1
fi
echo "[2/4] Static assets verified"

# Step 3: Build Go binary
echo "[3/4] Compiling Go binary..."

# Windows C compiler selection. What is actually known, as of
# 2026-08-01 against duckdb-go-bindings v0.10505.0:
#
#   works   MinGW-Builds GCC 15.2.0 at C:\mingw64 (msvcrt) — the stock
#           toolchain on the GitHub windows-latest runner. CI builds and
#           passes its e2e suite with this.
#   fails   MSYS2 ucrt64 GCC 16.1.0 — ld crashes linking
#           libduckdb_static.a ("ld returned 5 exit status", no symbol
#           diagnostics).
#
# Earlier notes in this project asserted the opposite — that ucrt64 was
# required and mingw64 GCC 15.x was broken by an _Mbstatet ABI mismatch.
# That was true of an older bindings release; it is not true now, so
# treat the ordering below as evidence rather than doctrine and re-check
# it if the link step starts failing. Probe a list because MSYS2 lives
# in a different place on every machine, then fall through to whatever
# gcc is on PATH — which is what upstream duckdb-go relies on.
if [ "$TARGET_OS" = "windows" ] && [ -z "${CC:-}" ]; then
    for candidate in \
        /c/mingw64/bin/gcc.exe \
        "${MSYS2_ROOT:-}/mingw64/bin/gcc.exe" \
        /c/msys64/mingw64/bin/gcc.exe \
        "${MSYS2_ROOT:-}/ucrt64/bin/gcc.exe" \
        /c/msys64/ucrt64/bin/gcc.exe
    do
        if [ -x "$candidate" ]; then
            export CC="$candidate"
            export PATH="$(dirname "$candidate"):$PATH"
            break
        fi
    done
fi

if [ "$TARGET_OS" = "windows" ]; then
    resolved_cc="${CC:-$(command -v gcc 2>/dev/null || echo '')}"
    if [ -z "$resolved_cc" ]; then
        echo "ERROR: no C compiler found for the windows/amd64 target." >&2
        echo "       Install MinGW-w64 GCC (the winlibs or MinGW-Builds" >&2
        echo "       distribution at C:\\mingw64 is what CI uses) or set" >&2
        echo "       CC explicitly. See docs/BUILD.md." >&2
        exit 1
    fi
    echo "  C compiler: $resolved_cc"
    "$resolved_cc" --version 2>/dev/null | head -1 | sed 's/^/  /'
fi

LDFLAGS="-s -w -X main.version=$VERSION"

# Windows: console subsystem (default). `-H windowsgui` was tried and
# reverted — hiding the console broke the unambiguous "close the window
# → the process dies" contract, and the auto-shutdown paths that were
# supposed to compensate proved less reliable than the console itself.
# The trade is a console window on Explorer launches.

# macOS: the supported minimum is whatever the build SDK targets, so
# derive it rather than pinning a floor. The previous 11.0 pin was
# aspirational — DuckDB's prebuilt archive and the Go runtime objects
# are compiled against the installed SDK, so the linker emitted a
# warning per object ("built for newer 'macOS' version than being
# linked") and the resulting binary would not have been trustworthy on
# an 11.0 machine anyway. Export it explicitly so the value is visible
# in the build log and overridable for a deliberate older target.
if [ "$TARGET_OS" = "darwin" ]; then
    sdk_version="$(xcrun --sdk macosx --show-sdk-version 2>/dev/null || echo "")"
    export MACOSX_DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-${sdk_version%%.*}.0}"
    echo "  macOS deployment target: $MACOSX_DEPLOYMENT_TARGET (from SDK ${sdk_version:-unknown})"
fi

CGO_ENABLED=1 GOOS="$TARGET_OS" GOARCH="$TARGET_ARCH" \
    go build -ldflags="$LDFLAGS" -o "dist/$OUTPUT_NAME" .

# macOS refuses to run unsigned binaries downloaded from the internet
# (Gatekeeper). An ad-hoc signature is enough for locally-built and
# manually-transferred binaries; distribution needs a Developer ID and
# notarization — wire that into hooks/sign.sh.
if [ "$TARGET_OS" = "darwin" ] && [ "$HOST_OS" = "darwin" ]; then
    if command -v codesign &>/dev/null; then
        codesign --force --sign - "dist/$OUTPUT_NAME" 2>/dev/null \
            && echo "  Ad-hoc signed (not notarized — see docs/BUILD.md)" \
            || echo "  codesign failed; binary is unsigned"
    fi
fi

echo "  Binary: dist/$OUTPUT_NAME"

# Vulnerability scan — symbol-reachable advisories only, low false-positive
# rate. SKIP_VULNCHECK=1 bypasses (handy for air-gapped builds where the
# Go vuln DB at vuln.go.dev is unreachable). Failures are reported but do
# not fail the build by default; flip the `|| true` to make it fatal.
if [ "${SKIP_VULNCHECK:-0}" != "1" ]; then
    if command -v govulncheck &>/dev/null; then
        echo "  Running govulncheck..."
        govulncheck ./... 2>&1 | tail -40 || echo "  (govulncheck reported issues — review above)"
    else
        echo "  govulncheck not found; install with: go install golang.org/x/vuln/cmd/govulncheck@latest"
        echo "  (set SKIP_VULNCHECK=1 to silence this notice)"
    fi
fi

# Ship parsers.d/ alongside the binary. Each YAML file inside drives
# autodetect for one block/line/xml format; users can add their own
# without recompiling.
if [ -d "${SCRIPT_DIR}/parsers.d" ]; then
    rm -rf "dist/parsers.d"
    cp -r "${SCRIPT_DIR}/parsers.d" "dist/parsers.d"
    echo "  Parser rules: dist/parsers.d/ ($(ls -1 dist/parsers.d/*.yaml 2>/dev/null | wc -l) files)"
fi

# Ship the config files. Core settings live in obs_viewer.conf (active,
# all values commented = defaults). Each optional module adds its own
# obs_viewer_<name>.conf sibling; ship one as `.example` when it needs
# per-site values, and the operator renames it to `.conf` to enable.
for conf in obs_viewer.conf; do
    if [ -f "${SCRIPT_DIR}/${conf}" ]; then
        cp "${SCRIPT_DIR}/${conf}" "dist/${conf}"
        echo "  Config: dist/${conf}"
    fi
done

# ---------------------------------------------------------------------------
# Step 4: Code signing hook
# ---------------------------------------------------------------------------
echo "[4/4] Post-build..."

if [ -x "${SCRIPT_DIR}/hooks/sign.sh" ]; then
    echo "  [hook] Running code signing..."
    "${SCRIPT_DIR}/hooks/sign.sh" "dist/$OUTPUT_NAME" "$PRODUCT_NAME" "$VERSION" "$MANUFACTURER"
elif [ "$TARGET_OS" = "windows" ] && command -v signtool.exe &>/dev/null; then
    echo "  [hook] signtool detected — sign manually or create hooks/sign.sh"
    echo "  Example: signtool sign /fd SHA256 /tr http://timestamp.digicert.com /td SHA256 dist/$OUTPUT_NAME"
else
    echo "  Code signing: skipped (no hooks/sign.sh and no signtool)"
fi

# ---------------------------------------------------------------------------
# Hook: post-build (custom actions after compilation)
# ---------------------------------------------------------------------------
if [ -x "${SCRIPT_DIR}/hooks/post-build.sh" ]; then
    echo "[hook] Running post-build..."
    "${SCRIPT_DIR}/hooks/post-build.sh" "dist/$OUTPUT_NAME" "$VERSION" "$TARGET_OS" "$TARGET_ARCH"
fi

echo ""
echo "=== Build complete ==="
echo "Product: ${PRODUCT_NAME} v${VERSION} (${MANUFACTURER})"
echo "Output:  dist/$OUTPUT_NAME"
ls -lh "dist/$OUTPUT_NAME"
