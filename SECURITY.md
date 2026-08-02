# Security Policy

## Reporting a vulnerability

Please report security issues privately via GitHub's
[private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)
on [github.com/labmk/obs-viewer](https://github.com/labmk/obs-viewer),
rather than opening a public issue.

Include what you have: affected version, a reproduction, and the impact
you believe it has. You'll get an acknowledgement within a few days.

## Threat model

obs-viewer is a **local, single-user tool**. It reads files you already
have access to and serves a UI to `127.0.0.1`. It is not a multi-tenant
service and does not attempt to be one.

**There is no authentication or authorization.** Anyone who can reach
the HTTP port has full access to every loaded file, can browse the
filesystem through `/api/browse` with the privileges of the process, can
export data to any writable path, and can shut the server down. The
loopback bind is the entire access control story.

Two guardrails enforce that:

- The server binds `127.0.0.1` unless `listen` is set in
  `obs_viewer.conf` **and** a TLS certificate is supplied via
  `--cert`/`--key`. Setting `listen` alone is ignored, with a warning.
- TLS is off by default because loopback traffic doesn't need it.

If you expose obs-viewer beyond loopback, put an authenticating reverse
proxy in front of it. Treat "reachable on the network without a proxy"
as a misconfiguration, not a vulnerability in obs-viewer.

### In scope

- Anything that lets a **file** escalate: a crafted NDJSON, EVTX, XML,
  or text log that causes memory corruption, code execution, path
  traversal on load or export, or a hang that isn't proportionate to its
  size. Parsers process untrusted input by design — this is the area
  that matters most.
- Path traversal or symlink-following in `/api/browse`, `/api/export`,
  `/api/export/self-copy`, or zip extraction that reaches outside the
  intended directory.
- A crafted `parsers.d/` rule that escapes its stated capabilities
  (regex matching and field mapping).
- Anything that weakens the loopback/TLS guardrail above.
- Dependency vulnerabilities reachable from obs-viewer's own code
  paths — `govulncheck` and `npm audit` both gate CI.

### Out of scope

- Lack of authentication on the local HTTP interface. That's the
  documented design; see above.
- Reading files the invoking user can already read. That is the tool's
  purpose.
- Denial of service through a legitimately enormous input file. Loading
  a 100 GB file on a 16 GB machine will fail, and that's expected.
- Findings that require an attacker to already control the machine, the
  binary, or the `parsers.d/` directory.

## Supported versions

The latest released version. Fixes land on `main` and ship in the next
release.

## Build integrity

- Release binaries are built by `.github/workflows/build.yml` on
  GitHub-hosted runners, one per platform.
- **Every release archive carries a signed build attestation.** It
  records that those exact bytes were produced by that workflow, from
  this repository, at a named commit. Verify before running:

  ```bash
  gh attestation verify obs_viewer-<version>-<platform>.zip --repo labmk/obs-viewer
  ```

  The signature is made with a short-lived certificate issued to the
  workflow run through Sigstore. No signing key exists to be stolen,
  and nothing has to be renewed.

  This is what `SHA256SUMS` cannot do. A checksum shows a download is
  intact against a list published in the same place by whoever
  published the file — an attacker who replaced the archive would
  replace the list with it. The attestation is tied to the build, not
  to the publisher.

- **Attestation is not code signing.** It answers "was this built from
  the source it claims?", not "who is the publisher?" — and only the
  second silences the operating system. Concretely:

  - macOS binaries are **ad-hoc signed only**, not notarized. A build
    downloaded through a browser is blocked by Gatekeeper until you
    clear the quarantine attribute. See
    [docs/BUILD.md](./docs/BUILD.md).
  - Windows binaries are unsigned unless you supply `hooks/sign.sh`,
    so SmartScreen will warn.

  Certificate-based signing for both platforms is tracked in
  [#24](https://github.com/labmk/obs-viewer/issues/24).
- DuckDB extensions are statically linked. obs-viewer never downloads an
  extension at runtime, which is what makes air-gapped use safe.
- **obs-viewer never downloads or executes code.** The one outbound
  request is the startup release check, which reads the GitHub releases
  API and renders a version string. There is no self-update path: a
  release cannot replace the running binary, so a compromised release
  channel cannot execute code on an existing install. User-initiated
  updates are a possible future feature, tracked in
  docs/SELF_UPDATE_PROPOSAL.md; they will not ship without verifying
  the attestation of the downloaded artifact first.
