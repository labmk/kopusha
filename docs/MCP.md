# MCP endpoint for AI agents

kopusha can give a local AI agent access to its parsers and query
engine over the [Model Context Protocol](https://modelcontextprotocol.io)
(MCP). The agent gets parsed columns, field lists and real field values
from a folder of mixed logs, instead of writing a regex per format.

This is the `mcp` module. It ships turned off.

## Turn it on

1. In the folder with the binary, rename `kopusha_mcp.conf.example` to
   `kopusha_mcp.conf`.
2. If only an agent will use kopusha, set `timeout = 0` in
   `kopusha.conf`. Otherwise the server exits after 180 seconds without
   requests while the agent is idle.
3. Start kopusha. `--no-browser` stops it from opening the UI.

```bash
./kopusha --no-browser
```

The startup log shows `module "mcp": enabled`. The endpoint is
`http://127.0.0.1:<port>/api/mcp` (port 9200 by default).

## Connect a client over HTTP

The transport is MCP Streamable HTTP.

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

If `token` is set, the client must send `Authorization: Bearer <token>`.

## Connect a client over stdin/stdout

Some clients can only start an MCP server as a subprocess and talk to it
over standard input and output. For them, `kopusha mcp` is a bridge: it
reads one JSON-RPC message per line from standard input, sends it to
the running kopusha's `/api/mcp`, and writes the reply to standard
output. It does not start an engine or load anything itself, so the
agent and the UI share one session.

Start kopusha with the module enabled first, then register the bridge:

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
| `--port` | `port` from `kopusha.conf`, else 9200 | Port of the running kopusha |
| `--url` | `http://127.0.0.1:<port>/api/mcp` | Full endpoint; overrides `--port` |
| `--token` | `token` from `kopusha_mcp.conf` | Bearer token |

The defaults come from the `kopusha*.conf` files next to the binary, so
a bridge started from the same install finds its server without flags.
Prefer the config file to `--token`: other local users can see
command-line arguments in the process list.

Bridge behaviour:

- **Server not running, or module off:** each request gets a JSON-RPC
  error (code `-32000`) that says which. The bridge keeps running, and
  the next request goes through once the server is fixed.
- **Order:** messages are forwarded one at a time, in order. A slow
  `query` delays the messages after it.
- **Output:** standard output carries only protocol messages, one
  compact JSON object per line. Diagnostics go to standard error.
- **Size:** a message larger than 1 MiB is dropped and answered with an
  error with a `null` id, the same limit as the endpoint.
- **TLS:** the bridge uses the system certificate store. It cannot
  reach a server started with `--cert`/`--key` and a self-signed
  certificate; use the HTTP transport for that.

## Tools

| Tool | Arguments | Returns |
|------|-----------|---------|
| `list_files` | none | Loaded files: id, path, records, enabled |
| `load_directory` | `path` (absolute; a folder or a file) | `loaded[]`, `errors[]`. Not recursive. Zip files are refused. Hidden when `allow_load = false`. |
| `list_formats` | none | Adapters in this build, rules from `parsers.d/`, the rules folder |
| `get_fields` | none | All fields across enabled files, including nested paths; the detected timestamp field |
| `field_samples` | `fields[]`, `cap` (default 30, max 500) | Distinct values per field. An empty list means more than `cap` values. |
| `query` | `filters[]`, `search_text`, `time_from`, `time_to`, `sort_order`, `sort_field`, `offset`, `limit` (default 50, max `max_rows`) | `rows[]`, `total_count`, `fields[]` |
| `explain_detection` | `path` (absolute file) | Score and reason per adapter, first line, encoding notes. Loads nothing. |

Filter operators: `is`, `is_not`, `contains`, `not_contains`,
`wildcard`, `not_wildcard`, `exists`, `does_not_exist`. `logic`
(`and` / `or`) joins a filter to the next one. This is the same filter
model as the UI and `POST /api/query`, without `is_one_of` and
`is_not_one_of`.

Tool errors (bad path, unknown field, `limit` above `max_rows`) are
returned as tool results with `isError: true`, so the agent sees the
message.

## Configuration

The `[mcp]` section in `kopusha_mcp.conf`:

| Key | Default | Effect |
|-----|---------|--------|
| `token` | empty | Require `Authorization: Bearer <token>` |
| `max_rows` | 500 | Largest `limit` one `query` call may use |
| `allow_load` | true | Offer the `load_directory` tool |
| `enabled` | true | `false` turns the module off without deleting the section |

## Security model

- **This machine only.** A request is refused unless the client address
  is loopback, the `Host` header names a loopback host, and any
  `Origin` header names a loopback host. This applies even when
  `listen` is set to a network address with TLS. The `Host` and
  `Origin` checks stop a web page open in a local browser from reaching
  the endpoint through DNS rebinding.
- **No outbound connections.** The module connects to nothing. The
  bridge connects only to the endpoint it is given (by default on
  `127.0.0.1`). Data reaches the agent process on this machine; where
  the agent sends it next depends on the agent's configuration.
- **Read-only on disk.** No export, no rule changes, no settings
  changes. `load_directory` reads files into the session; EVTX, XML
  and text logs are converted through a temporary file in the system
  temp folder, as in the UI. Zip files are refused because loading one
  extracts it next to the archive.
- **Shared session.** The agent and the UI see the same loaded files. A
  file loaded by the agent appears in the UI, and the other way round.

## Protocol details

- Supported protocol versions: `2025-11-25`, `2025-06-18`,
  `2025-03-26`. An unknown version in `initialize` is answered with the
  newest.
- POST only. Each request gets one `application/json` reply; there is
  no SSE stream and no session id. `GET` returns 405.
- Notifications get `202 Accepted`. JSON-RPC batches are rejected.
- Request body limit: 1 MiB.
