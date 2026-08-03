# Self-update — design + tradeoffs

Status: **both stages shipped** — release notification in 0.2.x, and
download-and-replace in 0.3.0.

The gate below was release signing, and it was met by build attestations
rather than by a signing key: every release archive carries one, and the
updater refuses any download GitHub holds no attestation for. What that
does and does not establish is stated in `internal/selfupdate/verify.go`
and in SECURITY.md — it is not a full Sigstore signature check, and it is
not described as one.

The rest of this document is the reasoning as it stood while the feature
was deferred. It is kept because the tradeoffs did not stop being true
when the code was written; the `parsers.d/` table in particular is the
specification the implementation follows.

## What shipped

A startup check against the GitHub releases API. It compares the running
version to the newest published release and, when the release is newer,
the status bar shows a link to the release page.

- On by default; `update_check = false` in `kopusha.conf`, or
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

Release archives contain `kopusha.conf` **and** `parsers.d/`. Users are
explicitly invited to edit the config and to drop their own YAML rules
into `parsers.d/` — that is the documented way to add a log format
without recompiling.

Extracting a release archive over an install directory deletes both. A
user's custom parser rules are the single thing they would be angriest to
lose, and the loss would be silent.

Settings survive for free — `kopusha_settings.json` sits beside the
binary and is never touched.

## Decided: how `parsers.d/` is updated

**A shipped rule is replaced only if it is byte-identical to what it
shipped as. Anything the user has touched is left alone.** Config files
are never replaced at all.

This keeps new log formats flowing to existing installs — otherwise a
release adding a parser would silently do nothing — without ever
overwriting work someone did by hand.

### Where the reference checksums come from

There is no installer, so nothing records what a file looked like when it
arrived. The answer is that **the binary carries the manifest**: build.sh
hashes `parsers.d/` and the result is embedded with `go:embed`. The
*running* binary therefore knows what it shipped with, and the updater
compares the files on disk against that, not against the incoming
release. No install-time state, nothing to corrupt, nothing to migrate.

This has to start before the updater exists. An install of a binary with
no embedded manifest can never be reasoned about later, so the first
self-update on such an install must fall back to touching nothing. Every
release that carries a manifest is one more install the eventual updater
can handle precisely.

### The cases that matter

| On disk | In the running binary's manifest | Action |
|---|---|---|
| Matches its recorded hash | yes | Replace. This is the whole point. |
| Differs from its recorded hash | yes | **Keep.** User edited a shipped rule. |
| Absent | yes | **Keep absent.** Deleting a rule is how you disable it; restoring it would undo a deliberate act. |
| Present | no | **Keep.** User's own rule. |
| Unmodified, and gone from the new release | yes | Delete. A withdrawn rule left behind can shadow its replacement, because `parsers.d/` loads in lexicographic order and the first match wins. |
| Modified, and gone from the new release | yes | Keep. Their edit, their call. |

The last two are the ones that get missed. Rule precedence is positional,
so a stale file is not inert — it can quietly outrank the rule meant to
supersede it.

**Report, don't just act.** When rules are skipped because the user
changed them, say so after updating: *"3 parser rules kept because you
modified them: 20-line-iso-bracket.yaml, …"*. Otherwise a user who edited
a shipped rule to fix a regex will silently never receive upstream fixes
to it, and will have no way to discover that.

### Config files stay out

`kopusha.conf` and `kopusha_*.conf` are never replaced, even when
unmodified. The shipped file is entirely commented-out defaults, so
replacing it gains only fresher documentation while risking a silent
behaviour change if a default moves. New `.example` siblings may be
added, since adding a file nobody references cannot change behaviour.

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

One decision remains, and everything else follows from it:

1. **Signing.** Which scheme, where the private key lives, and who can
   use it. Without this, stop.

The `parsers.d/` question is settled — see "Decided" above. Its only
prerequisite, the embedded manifest, is independent of signing and can
land at any time; the sooner it does, the more installs the eventual
updater can treat precisely rather than conservatively.

Then: user-initiated only — a button, never automatic. Verify the
signature before writing. Keep the previous binary for rollback. Restart
through the existing shutdown path rather than around it, and let the
SPA's 30-second `/api/version` poll notice the version change and reload
itself.

## The alternative that may be better than stage 2

Publishing through the platform package managers. They solve signing,
permissions, and replacement mechanics with infrastructure that
corporate IT already trusts, and none of it is code this project has to
own forever.

What they do not serve is this tool's actual deployment style — copying
the executable onto a share or a USB stick, which is exactly what the
self-copy export feature exists for. Users who get kopusha that way
were never going to be reached by a package manager, and are frequently
the same air-gapped users self-update cannot reach either.
