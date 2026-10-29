# Security

## Reporting a vulnerability

Report security issues privately through GitHub's
[private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)
on [github.com/labmk/kopusha](https://github.com/labmk/kopusha). Do not
open a public issue.

Include the affected version, steps to reproduce, and the impact you
expect. You will get an acknowledgement within a few days.

Only the latest release is supported. Fixes go to `main` and ship in
the next release.

## Threat model

kopusha is a local tool for one user. It reads files the user can
already read and serves its UI on `127.0.0.1`. It is not a multi-user
service.

**There is no authentication or authorisation.** Anyone who can reach
the HTTP port can read every loaded file, list the file system through
`/api/browse` with the process's permissions, export data to any
writable path, and stop the server. Binding to loopback is the only
access control.

Two rules protect that:

- The server binds `127.0.0.1` unless `listen` is set in
  `kopusha.conf` **and** a TLS certificate is given with `--cert` and
  `--key`. `listen` without a certificate is ignored with a warning.
- TLS is off by default, because loopback traffic does not need it.

If you expose kopusha to a network, put an authenticating reverse proxy
in front of it. Exposing it without one is a misconfiguration, not a
vulnerability in kopusha.

The MCP module has its own checks (loopback client, `Host` and
`Origin` headers, optional token); see [docs/MCP.md](./docs/MCP.md#security-model).

### In scope

- A crafted input file (NDJSON, Parquet, EVTX, XML, text log) that
  causes memory corruption, code execution, path traversal on load or
  export, or a hang out of proportion to its size. Parsers handle
  untrusted input; this area matters most.
- Path traversal or symlink following in `/api/browse`, `/api/export`,
  `/api/export/self-copy` or zip extraction that reaches outside the
  intended folder.
- A `parsers.d/` rule that does more than match regular expressions and
  map fields.
- Anything that weakens the loopback and TLS rule above.
- Vulnerable dependencies reachable from kopusha's code. `govulncheck`
  and `npm audit` run in CI.

### Out of scope

- Lack of authentication on the local HTTP port (by design, see above).
- Reading files the user can already read (that is the purpose of the
  tool).
- Running out of memory on a very large input. Loading a 100 GB file
  on a machine with 16 GB of memory will fail.
- Attacks that require control of the machine, the binary or the
  `parsers.d/` folder.

## Network behaviour

kopusha sends no telemetry and the UI loads nothing from the internet.
There are two outbound requests, both to GitHub:

1. **Release check**, at startup, in the background. A GET to the
   GitHub releases API for the latest version number. Nothing about the
   machine is sent beyond what any HTTP request reveals (IP address and
   User-Agent). It never downloads a release. Its result is shown in
   the status bar. It fails silently when there is no network.
2. **Self-update**, only when the user presses the update button. See
   [Self-update](#self-update).

`update_check = false` in `kopusha.conf` turns off both.
`--no-update-check` turns off both for one run. With either, kopusha
makes no outbound connections.

DuckDB extensions are linked into the binary. kopusha never downloads
an extension at runtime.

## Verifying a download

Release binaries are built by `.github/workflows/build.yml` on
GitHub-hosted runners. Every release archive has a signed build
attestation that records that those exact bytes were built by that
workflow, from this repository, at a named commit. Verify it with the
GitHub CLI:

```bash
gh attestation verify kopusha-<version>-<platform>.zip --repo labmk/kopusha
```

The signature uses a short-lived certificate issued to the workflow run
through Sigstore. There is no long-lived signing key to steal or renew.

`SHA256SUMS`, published beside the archives, only shows that a download
is intact compared with a list from the same place. Someone who
replaced an archive could replace the list too. The attestation is tied
to the build, not to whoever uploaded the file.

### Attestation is not code signing

An attestation shows the binary was built from this source. It does not
identify a publisher to the operating system, so it does not stop OS
warnings:

- macOS binaries have only an ad-hoc signature and are not notarized.
  A binary downloaded with a browser is blocked by Gatekeeper until you
  run `xattr -d com.apple.quarantine ./kopusha`. See
  [docs/BUILD.md](./docs/BUILD.md#macos).
- Windows binaries are unsigned unless a `hooks/sign.sh` is provided at
  build time, so SmartScreen warns.

Certificate-based signing is tracked in
[#24](https://github.com/labmk/kopusha/issues/24).

## Self-update

When a newer release exists, the UI offers to install it. Nothing
happens until the user presses the button. It never runs on a timer or
at startup, and it is unavailable when `update_check = false`.

Steps:

1. The release archive for the running platform is downloaded into
   memory.
2. Its SHA-256 digest is looked up in GitHub's attestations API. An
   archive that no release workflow produced has no attestation and is
   refused. The attestation must also name this repository, the release
   workflow, the tag being installed and a GitHub-hosted runner.
3. The UI shows the plan: versions, archive, verification result, and
   which parser rules and samples will change. Nothing has been written
   yet.
4. On confirmation, the new binary replaces the old one, `parsers.d/`
   and `samples/` are merged as described below, and kopusha restarts
   (on Windows, or if restarting fails, you start it again). The old
   binary is renamed to `<name>.old` during the swap and deleted the
   next time kopusha starts.

What the check proves: the archive matches GitHub's attestation record,
fetched over TLS. It does not verify the Sigstore signature inside the
attestation, so it does not protect against someone who can forge
responses from `api.github.com` (they could forge the download too). It
does protect against the realistic case: an archive uploaded by hand or
replaced after publication, which no workflow attested. For the full
cryptographic check, run `gh attestation verify` as shown above.

### What an update changes in your folder

- **Config files** (`kopusha*.conf`, `kopusha_settings.json`) are never
  touched.
- **Parser rules:** a rule is replaced or removed only if it is
  byte-identical to the rule the running binary shipped with (checked
  against the manifest compiled into the binary). Rules you edited,
  added or deleted are left alone, and the update reports what it
  skipped. If the running binary has no manifest, `parsers.d/` is not
  touched at all.
- **`samples/`:** shipped sample files are overwritten with the new
  release's versions, and the folder is created if missing. Other files
  in it are left alone. The binary never reads this folder.

The design is explained in
[docs/SELF_UPDATE_PROPOSAL.md](./docs/SELF_UPDATE_PROPOSAL.md).
