#!/usr/bin/env python3
"""Generate sbom.cdx.json and THIRD-PARTY-NOTICES.md.

obs-viewer ships as one statically linked executable, so everything below
is *inside* the binary you distribute. That is what makes the notices
file a licence obligation rather than a courtesy: MIT and BSD both
require their copyright notice to travel with copies, and Apache-2.0 §4
requires attribution.

Three sources feed the SBOM, because no single tool can see all of it:

  1. Go modules      cyclonedx-gomod, in `app` mode — only packages
                     actually reachable from main, not the whole module
                     graph. That distinction matters: golang-lru
                     (MPL-2.0) is in go.mod but never linked.
  2. npm packages    cyclonedx-npm with --omit dev. The Vite bundle is
                     embedded via go:embed, so production dependencies
                     ship; dev tooling does not.
  3. DuckDB bundle   Hand-maintained below. libduckdb_static.a arrives
                     prebuilt from duckdb-go-bindings, so no scanner can
                     see inside it. Omitting it would make the SBOM
                     quietly wrong about the largest single chunk of
                     third-party code in the binary.

Usage:  python3 scripts/generate-sbom.py

Requires: cyclonedx-gomod (go install github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@latest)
          npx (for @cyclonedx/cyclonedx-npm)
"""

import json
import os
import re
import subprocess
import sys
import tempfile
from datetime import datetime, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

# ---------------------------------------------------------------------------
# DuckDB's vendored third-party components.
#
# Derived from the static archives the linker actually consumes —
# `ls $(go env GOMODCACHE)/github.com/duckdb/duckdb-go-bindings/lib/*/lib*.a`
# — rather than from DuckDB's source tree, so this list reflects what is
# linked and not merely what upstream vendors. Licences were read from
# github.com/duckdb/duckdb third_party/<name>/LICENSE.
#
# To refresh after a duckdb-go-bindings bump: re-run that ls, diff the
# names against this table, and look up any newcomer.
# ---------------------------------------------------------------------------
DUCKDB_BUNDLED = [
    ("fastpforlib", "Apache-2.0", "Integer compression (FastPFor)"),
    ("fmt", "MIT", "Formatting library"),
    ("fsst", "MIT", "Fast static symbol table string compression"),
    ("hyperloglog", "MIT", "Cardinality estimation"),
    ("mbedtls", "Apache-2.0", "Crypto primitives"),
    ("miniz", "MIT", "Deflate/inflate"),
    ("pg_query", "PostgreSQL", "PostgreSQL-derived SQL parser (libpg_query)"),
    ("re2", "BSD-3-Clause", "Regular expressions"),
    ("skiplistlib", "MIT", "Skip list"),
    ("utf8proc", "MIT", "Unicode normalisation"),
    ("yyjson", "MIT", "JSON parsing"),
    ("zstd", "BSD-3-Clause", "Zstandard compression (dual BSD-3/GPL-2; BSD taken)"),
]

# DuckDB itself plus the extensions compiled in. All MIT, from the DuckDB
# project — statically linked, which is what makes air-gapped use work:
# obs-viewer never fetches an extension at runtime.
DUCKDB_CORE = [
    ("duckdb", "MIT", "Embedded analytical database engine"),
    ("duckdb-extension-json", "MIT", "JSON extension"),
    ("duckdb-extension-icu", "MIT", "ICU / timezone extension"),
    ("duckdb-extension-parquet", "MIT", "Parquet extension"),
    ("duckdb-extension-core-functions", "MIT", "Core scalar functions"),
    ("duckdb-extension-autocomplete", "MIT", "SQL autocomplete"),
]


# ---------------------------------------------------------------------------
# Licence corrections.
#
# cyclonedx-gomod classifies by matching licence *text*, which misfires on
# two families whose wording is close to a rarer licence. Both were checked
# against the upstream LICENSE file and GitHub's own classification before
# being overridden here — do not add an entry without doing the same.
# ---------------------------------------------------------------------------
LICENCE_OVERRIDES = {
    # Detected as BSD-Source-Code. These carry Go's standard three-clause
    # BSD text; golang/go is BSD-3-Clause and x/* repositories reuse it
    # verbatim.
    "golang.org/x/mod": "BSD-3-Clause",
    "golang.org/x/sync": "BSD-3-Clause",
    "golang.org/x/tools": "BSD-3-Clause",
    "golang.org/x/sys": "BSD-3-Clause",
    "golang.org/x/exp": "BSD-3-Clause",
    "golang.org/x/text": "BSD-3-Clause",
    "golang.org/x/xerrors": "BSD-3-Clause",
    # Detected as 0BSD. The file's first line reads "ISC License"; the
    # permission grant is ISC's "with or without fee" wording, which 0BSD
    # closely resembles.
    "github.com/davecgh/go-spew": "ISC",
}


def apply_overrides(component):
    spdx = LICENCE_OVERRIDES.get(component.get("name"))
    if spdx:
        component["licenses"] = [{"license": {"id": spdx}}]
        component.setdefault("properties", []).append(
            {
                "name": "obs-viewer:licence-corrected",
                "value": f"scanner output overridden to {spdx}; verified "
                         f"against the upstream LICENSE file",
            }
        )
    return component


def run(cmd, **kw):
    return subprocess.run(cmd, check=True, capture_output=True, text=True, **kw)


def project_version() -> str:
    m = re.search(r'var version = "([^"]+)"', (ROOT / "main.go").read_text())
    return m.group(1) if m else "0.0.0"


def gen_go(out: Path):
    env = dict(os.environ, CGO_ENABLED="1")
    run(
        ["cyclonedx-gomod", "app", "-json", "-licenses", "-assert-licenses",
         "-main", ".", "-output", str(out)],
        cwd=ROOT, env=env,
    )
    return json.loads(out.read_text())


def gen_npm(out: Path):
    run(
        ["npx", "--yes", "@cyclonedx/cyclonedx-npm@latest", "--omit", "dev",
         "--output-format", "JSON", "--output-file", str(out),
         "--spec-version", "1.6"],
        cwd=ROOT / "frontend",
    )
    return json.loads(out.read_text())


def licence_ids(component) -> list:
    """CycloneDX allows id, name or expression — normalise to a list."""
    out = []
    for entry in component.get("licenses", []):
        if "expression" in entry:
            out.append(entry["expression"])
            continue
        lic = entry.get("license", {})
        val = lic.get("id") or lic.get("name")
        if val:
            out.append(val)
    return out or ["UNKNOWN"]


def tag(component, origin):
    component.setdefault("properties", []).append(
        {"name": "obs-viewer:origin", "value": origin}
    )
    return component


def synthetic(name, spdx, description, group):
    return {
        "type": "library",
        "name": name,
        "group": group,
        "description": description,
        "licenses": [{"license": {"id": spdx}}],
        "properties": [
            {"name": "obs-viewer:origin", "value": "duckdb-static"},
            {
                "name": "obs-viewer:evidence",
                "value": "statically linked inside libduckdb_static.a; "
                         "not discoverable by SBOM scanners",
            },
        ],
    }


def main():
    version = project_version()
    with tempfile.TemporaryDirectory() as td:
        td = Path(td)
        print("Scanning Go modules (reachable from main only)...", file=sys.stderr)
        go = gen_go(td / "go.json")
        print("Scanning npm production dependencies...", file=sys.stderr)
        npm = gen_npm(td / "npm.json")

    components = []
    for c in go.get("components", []):
        components.append(apply_overrides(tag(c, "go")))
    for c in npm.get("components", []):
        components.append(apply_overrides(tag(c, "npm")))
    for name, spdx, desc in DUCKDB_CORE:
        components.append(synthetic(name, spdx, desc, "org.duckdb"))
    for name, spdx, desc in DUCKDB_BUNDLED:
        components.append(synthetic(name, spdx, desc, "org.duckdb.third_party"))

    bom = {
        "bomFormat": "CycloneDX",
        "specVersion": "1.6",
        "version": 1,
        "metadata": {
            "timestamp": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "tools": {
                "components": [
                    {"type": "application", "name": "cyclonedx-gomod"},
                    {"type": "application", "name": "cyclonedx-npm"},
                    {"type": "application", "name": "scripts/generate-sbom.py"},
                ]
            },
            "component": {
                "type": "application",
                "name": "obs_viewer",
                "version": version,
                "description": "Single-binary local-first viewer for log, "
                               "metric and trace files",
                "licenses": [{"license": {"id": "MIT"}}],
                "externalReferences": [
                    {"type": "vcs", "url": "https://github.com/labmk/obs-viewer"}
                ],
            },
        },
        "components": components,
        "dependencies": go.get("dependencies", []) + npm.get("dependencies", []),
    }

    (ROOT / "sbom.cdx.json").write_text(json.dumps(bom, indent=2) + "\n")
    print(f"sbom.cdx.json: {len(components)} components", file=sys.stderr)

    write_notices(components, version)


def write_notices(components, version):
    by_licence = {}
    for c in components:
        for lic in licence_ids(c):
            name = c["name"]
            if c.get("group") and not name.startswith(c["group"]):
                name = f"{c['group']}/{name}"
            entry = (name, c.get("version", ""), c.get("description", ""))
            by_licence.setdefault(lic, set()).add(entry)

    lines = [
        "# Third-party notices",
        "",
        f"obs-viewer {version} is distributed as a single statically linked",
        "executable. Every component listed here is compiled into that binary,",
        "which is why their notices ship with it: MIT and BSD require the",
        "copyright notice to travel with copies, and Apache-2.0 §4 requires",
        "attribution.",
        "",
        "obs-viewer's own code is MIT — see [LICENSE](./LICENSE). This file",
        "covers everything else.",
        "",
        "**Generated** by `scripts/generate-sbom.py`; do not edit by hand.",
        "A machine-readable CycloneDX equivalent is in",
        "[sbom.cdx.json](./sbom.cdx.json).",
        "",
        "## Summary",
        "",
        "| Licence | Components |",
        "|---------|-----------|",
    ]
    for lic in sorted(by_licence):
        lines.append(f"| {lic} | {len(by_licence[lic])} |")

    lines += [
        "",
        "Every licence above is permissive. No component is under GPL, AGPL,",
        "or any other reciprocal licence — a constraint enforced by policy in",
        "[CONTRIBUTING.md](./CONTRIBUTING.md), because a copyleft dependency",
        "would make the distributed binary undistributable under MIT.",
        "",
    ]

    for lic in sorted(by_licence):
        lines += [f"## {lic}", ""]
        for name, ver, desc in sorted(by_licence[lic]):
            suffix = f" — {desc}" if desc else ""
            lines.append(f"- `{name}`{' ' + ver if ver else ''}{suffix}")
        lines.append("")

    lines += [
        "## A note on the DuckDB components",
        "",
        "`libduckdb_static.a` arrives prebuilt from `duckdb-go-bindings`, so no",
        "scanner can see inside it. The DuckDB entries above were derived from",
        "the static archives the linker actually consumes and their licences",
        "read from the DuckDB source tree. They are marked in the SBOM with an",
        "`obs-viewer:evidence` property recording that provenance, rather than",
        "being presented as scanner output.",
        "",
        "Full licence texts are available from each project's repository. The",
        "DuckDB bundle's texts ship inside the `duckdb-go-bindings` module.",
        "",
    ]

    (ROOT / "THIRD-PARTY-NOTICES.md").write_text("\n".join(lines))
    print(f"THIRD-PARTY-NOTICES.md: {len(by_licence)} distinct licences",
          file=sys.stderr)


if __name__ == "__main__":
    main()
