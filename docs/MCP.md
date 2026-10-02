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

### Clients that only speak standard input/output

Some clients launch every MCP server as a subprocess and talk to it
over standard input/output. For those, `kopusha mcp` is a thin bridge:
it reads one JSON-RPC message per line from standard input, forwards it
to the running viewer's `/api/mcp`, and writes the reply to standard
output. It starts no engine and loads nothing itself, so the agent and
the UI still share one session.

The viewer must already be running with the module enabled. Start it
first, then register the bridge with the client:

```bash
# Claude Code
claude mcp add kopusha -- /path/to/kopusha mcp
```

```json
{
  "mcpServers": {
    "kopusha": { "command": "/path/to/kopusha", "args": ["mcp"] }
  }
}
```

| Flag | Default | Effect |
|------|---------|--------|
| `--port` | `port` from `kopusha.conf`, else 9200 | Port of the running viewer |
| `--url` | `http://127.0.0.1:<port>/api/mcp` | Full endpoint, overrides `--port` |
| `--token` | `token` from `kopusha_mcp.conf` | Bearer token |

The defaults come from the `kopusha*.conf` files next to the binary, so
a bridge launched from the same install finds its viewer without flags.
Prefer the config file over `--token`: command-line arguments are
visible to other local users in the process list.

Behaviour:

- **Viewer not running, or module disabled:** every request gets a
  JSON-RPC error (code `-32000`) saying which, and the bridge keeps
  running. Start or fix the viewer and the next request goes through.
- **Ordering:** messages are forwarded one at a time, in order. A long
  `query` delays the messages behind it.
- **Output:** standard output carries protocol messages only, one
  compact JSON object per line. Diagnostics go to standard error.
- **Size:** a message over 1 MiB is dropped and answered with an error
  with a `null` id, matching the endpoint's own body limit.
- **TLS:** the bridge uses the system certificate store. A viewer
  started with `--cert`/`--key` and a self-signed certificate is not
  reachable through it; use the client's HTTP transport instead.

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
- **No egress.** The module makes no outbound connection. The
  `kopusha mcp` bridge connects only to the endpoint it is given, by
  default on `127.0.0.1`. Data reaches
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
