# Self-update — design + tradeoffs

Status: **stage 1 shipped** (release notification).
Stage 2 (downloading and replacing the binary) is **deferred**.
Trigger to revisit: release signing exists, and someone actually asks.

## What shipped

A startup check against the GitHub releases API. It compares the running
version to the newest published release and, when the release is newer,
the status bar shows a link to the release page.

- On by default; `update_check = false` in `obs_viewer.conf`, or
  `--no-update-check` for a single run.
- Runs in the background. Startup never waits on it.
- Fails silently at normal verbosity — on an air-gapped host, failure is
  the expected outcome of every launch and must not accumulate warnings.
- Transmits nothing about the host beyond the IP address and User-Agent
  that any HTTP request reveals. No identifier, no telemetry.
- `GET /api/update` returns the cached result. `enabled: false` and
  `checked: false` are distinct from "up to date", so the UI never
  implies a state it has no basis for.

Nothing is downloaded. Nothing is executed. The running binary is never
modified.

## What is deliberately not built

Downloading a release and replacing the running executable. Four reasons,
in descending order of importance.

### 1. Releases are not signed

`SHA256SUMS` authenticates against corruption, not substitution. Anyone
who compromises the GitHub account, or presents a certificate the client
trusts, can serve a different binary with a matching checksum file.

Auto-replacement turns that from "a bad download" into "code execution on
every install that updates". The minimum bar is an embedded public key
(minisign or cosign), signing in CI, and signature verification before
anything touches disk — which brings key management with it: where the
private key lives, what a loss or compromise means, how CI holds the
secret.

**No self-replacement without signing.** This is the gate.

### 2. The users who need it most are the ones it fails for

| Audience | Can self-update work? |
|---|---|
| Air-gapped operators — a stated target | No. No egress, by definition. |
| Locked-down corporate Windows — the primary target | Usually not. The binary often sits in a directory the user cannot write; egress goes through an authenticating proxy; SmartScreen blocks unsigned downloads. |
| Developers on their own machines | Yes — and they can also just download the zip. |

The population where self-update is both possible and worth the machinery
is narrow. That is the main reason stage 1 is most of the value.

### 3. It would quietly destroy user data

Release archives contain `obs_viewer.conf` **and** `parsers.d/`. Users are
explicitly invited to edit the config and to drop their own YAML rules
into `parsers.d/` — that is the documented way to add a log format
without recompiling.

Extracting a release archive over an install directory deletes both. A
user's custom parser rules are the single thing they would be angriest to
lose, and the loss would be silent.

The safe rule is **replace only the executable** and never touch
`obs_viewer.conf`, `obs_viewer_*.conf`, or `parsers.d/`. But that has a
cost: parser rules shipped in a new release would never reach existing
installs, so a release adding a log format would not take effect. Doing
it properly needs a manifest of shipped files with checksums, so the
updater can overwrite what is unmodified since install and leave
user-touched files alone. That is the part most likely to be
underestimated.

Settings survive for free — `obs_viewer_settings.json` sits beside the
binary and is never touched.

### 4. Platform mechanics, in ascending order of pain

- **Linux** — easy. `unlink` and replace works on a running binary;
  preserve the mode bit.
- **Windows** — a running `.exe` cannot be overwritten, but it *can* be
  renamed. Rename current to `.old`, write the new one, restart, delete
  `.old` on next launch.
- **macOS** — the hard one. A replaced binary loses its ad-hoc signature,
  and builds are not notarized, so Gatekeeper is entitled to refuse the
  result. The updater can strip the quarantine attribute itself, but the
  clean answer is a Developer ID and notarization.

Plus: fail gracefully when the install directory is not writable, which
on Windows is the common case rather than the exception.

## If stage 2 is built

Two decisions come first, and everything else follows from them:

1. **Signing.** Which scheme, where the private key lives, and who can
   use it. Without this, stop.
2. **Whether `parsers.d/` is ever updated automatically.** "No" is
   defensible and much simpler; "yes" requires the checksum manifest.

Then: user-initiated only — a button, never automatic. Verify the
signature before writing. Keep the previous binary for rollback. Restart
through the existing shutdown path rather than around it, and let the
SPA's 30-second `/api/version` poll notice the version change and reload
itself.

## The alternative that may be better than stage 2

Publish to **winget** and a **Homebrew tap**. Both solve signing,
permissions, and replacement mechanics with infrastructure that corporate
IT already trusts, and neither is code this project has to own forever.

What they do not serve is this tool's actual deployment style — copying
the executable onto a share or a USB stick, which is exactly what the
self-copy export feature exists for. Users who get obs-viewer that way
were never going to be reached by a package manager, and are frequently
the same air-gapped users self-update cannot reach either.
