# MCP endpoint for AI agents

kopusha can serve its parser and query engine to a local AI agent over
the [Model Context Protocol](https://modelcontextprotocol.io). An agent
pointed at a folder of mixed logs gets columns, a schema and real field
values, instead of writing a regex per format per file.

It ships as the `mcp` module, disabled.

## Enable

1. Rename `kopusha_mcp.conf.example` (next to the binary) to
   `kopusha_mcp.conf`.
2. For agent-only use, set `timeout = 0` in `kopusha.conf`, or the
   server exits after 180 s without requests while the agent is idle.
3. Start kopusha. `--no-browser` skips opening the UI.

```bash
./kopusha --no-browser
```

Startup logs `module "mcp": enabled`. The endpoint is
`http://127.0.0.1:<port>/api/mcp` (port 9200 by default).

## Connect a client

The transport is MCP Streamable HTTP. Clients that support it:

```bash
# Claude Code
claude mcp add --transport http kopusha http://127.0.0.1:9200/api/mcp
```

Generic client configuration:

```json
{
  "mcpServers": {
    "kopusha": { "type": "http", "url": "http://127.0.0.1:9200/api/mcp" }
  }
}
```

With `token` set, add the header `Authorization: Bearer <token>`.

Clients that only launch servers over standard input/output are not
supported yet.

## Tools

| Tool | Arguments | Returns |
|------|-----------|---------|
| `list_files` | — | Loaded files: id, path, records, enabled |
| `load_directory` | `path` (absolute; directory or file) | `loaded[]`, `errors[]`. Not recursive. Zip refused. Hidden when `allow_load = false` |
| `list_formats` | — | Ingest adapters in this build, rules from `parsers.d`, rules directory |
| `get_fields` | — | Field union across enabled files, including STRUCT sub-paths; detected timestamp field |
| `field_samples` | `fields[]`, `cap` (default 30, max 500) | Distinct values per field. Empty list = more than `cap` values |
| `query` | `filters[]`, `search_text`, `time_from`, `time_to`, `sort_order`, `sort_field`, `offset`, `limit` (default 50, max `max_rows`) | `rows[]`, `total_count`, `fields[]` |
| `explain_detection` | `path` (absolute file) | Per-adapter score and reason, first line, encoding notes. Loads nothing |

Filter operators: `is`, `is_not`, `contains`, `not_contains`,
`wildcard`, `not_wildcard`, `exists`, `does_not_exist`. `logic`
(`and`/`or`) joins a filter to the next. Same model as the viewer and
`POST /api/query`.

Tool failures (bad path, unknown field, limit over `max_rows`) come
back as tool results with `isError: true`, so the agent sees the
message.

## Configuration

`[mcp]` in `kopusha_mcp.conf`:

| Key | Default | Effect |
|-----|---------|--------|
| `token` | empty | Require `Authorization: Bearer <token>` |
| `max_rows` | 500 | Largest `limit` one `query` call may use |
| `allow_load` | true | Expose `load_directory` |
| `enabled` | true | `false` disables without deleting the section |

## Security model

- **This machine only.** Requests are refused unless the remote address
  is loopback, the `Host` header names loopback, and any `Origin`
  header names loopback. This holds even when `listen` is non-loopback
  with TLS. The `Host`/`Origin` checks stop a web page in a local
  browser from reaching the endpoint through DNS rebinding.
- **No egress.** The module makes no outbound connection. Data reaches
  the agent process on this machine; where the agent sends it next is
  the agent's configuration, not kopusha's.
- **Read-only on disk.** No export, no rule writes, no settings
  changes. `load_directory` reads files into the session; non-NDJSON
  formats are converted through the system temp directory, as in the
  UI. Zip archives are refused because loading one extracts it next to
  the archive.
- **Shared session.** The agent and the UI see the same loaded files.
  A file the agent loads appears in the UI and the reverse.

## Protocol notes

- Revisions accepted: `2025-11-25`, `2025-06-18`, `2025-03-26`. An
  unknown revision in `initialize` is answered with the newest.
- POST only. Each request gets one `application/json` reply; no SSE
  stream, no session id. `GET` returns 405.
- Notifications get `202 Accepted`. JSON-RPC batches are rejected.
- Request body limit: 1 MiB.
