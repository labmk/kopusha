# Roadmap

This file lists the project's direction, not dates. kopusha has one
maintainer; items move when someone does the work, and "future" means
wanted but not scheduled.

Work is tracked in [issues](https://github.com/labmk/kopusha/issues),
grouped by [milestone](https://github.com/labmk/kopusha/milestones)
and labelled `horizon:*`. This file gives the reasons; the issues hold
the details.

## Direction

Running SQL over local CSV, JSON or Parquet files is already well
covered by other tools. Those tools do not turn unstructured logs into
typed columns. That is the part of kopusha with no ready equivalent:
the `parsers.d/` rules, the scored format detection, and querying
files with unrelated schemas as one table.

So the work goes into parsing and the tools around it. For everything
after parsing, kopusha hands off to other tools instead of competing
with them. Parquet export is the main example: parse the logs here,
then open the result in any analytical tool.

The intended users are people who need answers from logs but do not
write regular expressions as a matter of course. People who are fluent
with grep and regex already have tools that work for them.

## Shipped

Details are in the [changelog](../CHANGELOG.md).

| Issue | Feature |
|-------|---------|
| #13, #14 | Rule builder in the UI, and an explanation of why a file did not parse. The failure screen leads straight to the rule builder. |
| #19 | Time histogram |
| #20 | View state in the URL, for sharing and reloads |
| #12 | Virtualised result table |
| #15 | Parquet read and export |
| #25 | Export writes to the folder the user is looking at |
| #18 | Field profiling panel |
| #17 | Self-update on request, checked against the release build attestation; see [SECURITY.md](../SECURITY.md#self-update) |
| [#16](https://github.com/labmk/kopusha/issues/16) | MCP server (0.3.12): the `mcp` module serves seven tools at `/api/mcp`, and `kopusha mcp` bridges stdin/stdout for clients that only start subprocesses. See [MCP.md](./MCP.md). |

## Future

Wanted, not scheduled.

| Issue | Item |
|-------|------|
| [#24](https://github.com/labmk/kopusha/issues/24) | **Platform signing.** Releases already have a Sigstore build attestation that self-update checks against GitHub's record. Still open: checking the signature inside the binary instead of trusting `api.github.com`, macOS notarization so downloads are not quarantined, and Windows code signing. |
| [#21](https://github.com/labmk/kopusha/issues/21) | **Remote sources.** NFS and SMB already work as mounted paths and only need documenting. S3 needs DuckDB's `httpfs` extension, which is not linked in; loading it at runtime would break offline use. |

## Later

Blocked on a precondition or a decision.

| Issue | Item |
|-------|------|
| [#22](https://github.com/labmk/kopusha/issues/22) | **Live tail.** A second mode with its own state handling, in an area other tools already cover well. |
| [#23](https://github.com/labmk/kopusha/issues/23) | **LLM-assisted query building.** Conflicts with offline use, and the query language is probably not what slows users down. Field profiling has shipped; revisit only if the need remains. |

## Not planned

- **An observability platform** (ingestion, retention, alerting,
  dashboards). Established tools do this better than a project of this
  size could.
- **A terminal UI.** Existing terminal tools have far more work behind
  them than this project would put in.
- **Authentication.** The loopback-only bind is the access control; see
  [SECURITY.md](../SECURITY.md). Anything more belongs in a reverse
  proxy.
