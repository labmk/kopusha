# Trace visualization — design + tradeoffs

Status: **deferred** (no concrete user request yet).
Trigger to revisit: real OTel trace exports flowing into obs-viewer
in volumes that make the current row-table view unhelpful.

## Why the current UI is wrong for traces

OpenTelemetry spans carry:

| Field | Purpose |
|------|---------|
| `trace_id` | groups every span of one logical operation |
| `span_id` | this span's identity |
| `parent_span_id` | tree edge to the span that called this one |
| `service.name` | which service emitted this span |
| `name` | operation label (`GET /api/users`, `db.query`, …) |
| `start_time`, `end_time` (or `duration`) | the timing window |
| `attributes` | arbitrary key/value (status, http.status_code, db.statement, …) |
| `status` | OK / ERROR / UNSET |

LogTable renders each span as a row, sorted by `@timestamp`. For 50
spans across 8 services in one trace, the operator reads 50 rows and
manually correlates them — losing the parent-child structure that
makes traces useful in the first place. The visual answer is a
**waterfall** (a.k.a. Gantt-per-trace): one row per span, indented by
depth, bar position = start offset from trace root, bar width =
duration, colour = service.

## Proposed UX

1. **Auto-detect mode.** When `/api/files` returns a set containing
   the trace fields (`trace_id`, `span_id`, `parent_span_id`,
   `start_time`), surface a **"Traces"** tab next to the log table.
   No mode switch in conf — just light up when the data shape matches.
2. **Trace list pane** (left): rows = distinct `trace_id`, each
   showing root span name, total duration, span count, error flag.
   Filterable + sortable like any LogTable, just narrower.
3. **Waterfall pane** (right, on trace select): vertical list of
   spans depth-first, horizontal axis is time. Hover → tooltip with
   attributes. Click → details drawer with full attribute dump +
   linked log lines (any log rows with matching `trace_id`).
4. **Existing filter + time range applies.** The visual is just a
   different render of the same DuckDB query result.

## Implementation options

| Approach | Bundle cost | Effort | Notes |
|---|---|---|---|
| **Hand-built SVG/canvas component** | ~+15-25 KB gzipped | 3-5 days | Full control over UX, no new dep. Have to handle: zoom/pan, clock skew across services, async spans (where end < parent end is legal), error highlighting, virtualization for 10k+ spans. |
| **vis-timeline** | ~+150 KB gzipped (430 KB → ~580 KB; +35%) | 1-2 days | Mature, has zoom/pan/grouping for free. Adds a transitive dep tree; Vite tree-shaking helps but not fully. |
| **react-flow** | ~+100 KB gzipped | 2-3 days | Designed for graph/DAG, not Gantt. Possible but awkward fit. |
| **D3-timeline / d3.js** | ~+90 KB gzipped (or +30 KB for only the modules we need) | 4-6 days | Maximum flexibility, but D3 imperative API doesn't pair cleanly with React's declarative model. |
| **Embed Jaeger / Tempo UI** | n/a — not a React component | n/a | Ships its own SPA + backend. Not a fit for obs-viewer's single-binary shape. |

**Recommended path: hand-built SVG component.** Reasons:
- The waterfall is a single screen with one interaction (hover/click). Generic charting libraries' value (multi-pane dashboards, axis composition) doesn't apply.
- Bundle stays under 500 KB gzipped — important for "single 60 MB binary" positioning.
- DuckDB already handles trace assembly: a CTE that groups by `trace_id` and resolves parent chains is straightforward.
- The result is composable with the existing FieldPicker / time filter / Radix overlays we already have.

## Engine work

- New query mode in `engine.go`: given a `trace_id`, return all rows
  with that trace_id ordered by start_time, parent first. Recursive
  CTE on `parent_span_id`. ~30 lines.
- New endpoint `GET /api/traces` returning the deduplicated list of
  trace_ids with summary stats (root span name, total duration, error
  count, span count). ~40 lines.
- `GET /api/traces/{id}` returning the full span list. ~20 lines.
- Sample-value heuristic to flag a file as "looks like a trace
  export" — adds 1 field to `FileInfo` so the SPA can decide whether
  to show the Traces tab. ~5 lines.

Total backend: ~100 lines + tests.

## Frontend work

- `TracesPanel.jsx` — left list, right waterfall. ~300 lines.
- `Waterfall.jsx` — SVG render: time axis, per-span bars,
  depth-indented labels, hover tooltip, error highlight. ~250 lines.
- `useTraces` hook in `useApiQueries.js` for the two new endpoints.
  ~30 lines.
- App.jsx: conditionally render Traces tab when any loaded file's
  `format` hints at traces. ~10 lines.

Total frontend: ~600 lines. Plus e2e — one new spec to load a
synthetic trace NDJSON fixture, assert the tab appears, click a trace,
assert the waterfall renders ≥ N bars. ~50 lines.

## Disadvantages

| Cost | Notes |
|---|---|
| **+~20 KB frontend bundle (hand-built option)** | Vs +150 KB for vis-timeline. Either way, the JS file grows; gzip helps but not at-rest. Acceptable on its own; concerning if we keep adding features without pruning. |
| **+~100 lines Go + ~600 lines React + ~50 lines tests** | Real maintenance surface. Spans-specific code paths in the engine that don't share much with the existing query path. |
| **Fixture data is bigger than text logs** | A useful trace fixture needs ~50-500 spans. Test fixtures could grow `test-fixtures/` from ~2 MB to ~20 MB if real traces are bundled. Alternative: generate fixtures programmatically in a setup script. |
| **Trace data shape is more brittle than logs** | OTel spec evolves (clock semantics, span links, span events, instrumentation scope). Code that assumes a particular shape today breaks when the producer updates. Logs have no spec to break. |
| **Edge cases multiply** | Clock skew between services (child appears to start before parent), missing parents (parent_span_id refers to a span not in the result set), pre-aggregated spans, sampled traces with gaps. Each edge case is a UX call. |
| **Memory growth on huge traces** | A trace with 10k spans is realistic for distributed pipelines. Rendering 10k SVG elements without virtualization stutters the browser. Virtualization is the right answer but adds another ~100 lines of windowing logic. |
| **No real-time trace ingest** | obs-viewer is file-oriented by design. Operators investigating a live incident would use Jaeger/Tempo directly rather than wait for the next export cycle. obs-viewer's value here is **post-mortem**, which limits the audience. |
| **Opportunity cost** | ~1-2 weeks of focused work. The same time spent on the engine/server Go test suites (currently absent) probably catches more bugs than the trace view solves. |

## When to actually build this

Three concrete triggers, any one of them:

1. **Trace data starts appearing in routine exports.** Currently
   the example fixtures and the schemas operators load are all log /
   metric files. If a customer's NDJSON suddenly contains 30% spans
   with parent_span_id chains, the row-table view becomes hostile and
   we should ship the waterfall.
2. **A support engineer asks for it twice in independent threads.**
   The current backlog entry exists because it *would* be nice; no
   one has been blocked by its absence yet.
3. **A trace-shaped post-mortem investigation actually happens** and
   the operator falls back to copying spans into a spreadsheet to
   reconstruct the hierarchy. That's the signal that the row-table is
   broken for this workload.

Until one of those fires, this proposal stays in the docs folder
unimplemented. Estimated 1-2 weeks of work if/when we commit.
