// LogQL-shaped query DSL for obs-viewer.
//
// Design goal: text that can be pasted into a chat/notes, re-pasted here
// to restore state, AND ported to a Grafana/Loki backend with minimal
// edits (basically: replace `{}` with `{job=...}`, drop @time:).
//
// Grammar (informal):
//
//   QUERY      := { COMMENT | BLANK | LINE }
//   COMMENT    := "#" anything "\n"
//   BLANK      := whitespace "\n"
//   LINE       := SELECTOR | PIPE | TIME
//   SELECTOR   := "{" "}"                       (only empty form is meaningful here)
//   PIPE       := "|" LINE_FILTER | "|" LABEL_FILTER | "|" PARSER
//   LINE_FILTER:= "=" QSTR | "!=" QSTR | "~" QSTR | "!~" QSTR
//   PARSER     := "json"                        (accepted, no-op for obs-viewer)
//   LABEL_FILTER := IDENT OP QSTR
//   OP         := "=" | "!=" | "=~" | "!~"
//   TIME       := "@time:" DT ".." DT
//   IDENT      := [A-Za-z0-9_@.-]+
//   QSTR       := '"' ... '"'  (no backslash handling needed for our value set)
//   DT         := "YYYY-MM-DD HH:MM:SS"
//
// Line filters that target the raw row text (no field) become the
// `search` channel in obs-viewer state. Label filters become entries in
// `filters[]`. Time becomes `time_from` / `time_to`.
//
// `contains` and `wildcard` both serialize as `=~ ".*X.*"`. Parse always
// returns `wildcard` — semantically identical SQL (ILIKE) so no
// functional loss, just a label change in the visual builder.

const RE_TIME = /^@time:\s*(\S+\s+\S+)\s*\.\.\s*(\S+\s+\S+)\s*$/;
const RE_SELECTOR = /^\{\s*\}$/;
const RE_LABEL = /^\|\s*([A-Za-z0-9_@.-]+)\s*(=~|!~|!=|=)\s*"(.*)"\s*$/;
const RE_LINE_FILTER = /^\|(=|!=|~|!~)\s*"(.*)"\s*$/;
const RE_PARSER = /^\|\s*(json|logfmt)\s*$/;

// quoteValue wraps a string in double quotes and escapes embedded ".
// Newlines aren't expected (the value comes from a single-line filter
// input) so we don't need to handle them.
function quoteValue(v) {
  return '"' + String(v).replace(/"/g, '\\"') + '"';
}

// regexEscape — escape regex metachars for inserting a literal substring
// into a pattern. Used by contains / not_contains serialization.
function regexEscape(s) {
  return String(s).replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

// globToRegex — wildcard inputs use * (any) and ? (one). Translate to
// regex while escaping every other metachar literally.
function globToRegex(s) {
  let out = '';
  for (const ch of String(s)) {
    if (ch === '*') out += '.*';
    else if (ch === '?') out += '.';
    else if ('.+^${}()|[]\\'.indexOf(ch) >= 0) out += '\\' + ch;
    else out += ch;
  }
  return out;
}

// Serialize an obs-viewer filter to its `| field op "value"` line.
function serializeFilter(f) {
  const field = f.field;
  switch (f.operator) {
    case 'is':
      return `  | ${field} = ${quoteValue(f.value)}`;
    case 'is_not':
      return `  | ${field} != ${quoteValue(f.value)}`;
    case 'contains':
      return `  | ${field} =~ ${quoteValue('.*' + regexEscape(f.value) + '.*')}`;
    case 'not_contains':
      return `  | ${field} !~ ${quoteValue('.*' + regexEscape(f.value) + '.*')}`;
    case 'wildcard':
      return `  | ${field} =~ ${quoteValue(globToRegex(f.value))}`;
    case 'not_wildcard':
      return `  | ${field} !~ ${quoteValue(globToRegex(f.value))}`;
    case 'exists':
      return `  | ${field} =~ ".+"`;
    case 'does_not_exist':
      return `  | ${field} = ""`;
    case 'is_one_of':
    case 'is_not_one_of': {
      // LogQL has no `IN` keyword — emit anchored regex alternation
      // (`^(v1|v2|v3)$`) so the round-trip stays a LogQL-shaped query
      // and the engine can fall back to wildcard semantics if a future
      // parse doesn't recognise this specific shape.
      const parts = String(f.value || '').split(',')
        .map((s) => s.trim())
        .filter(Boolean)
        .map(regexEscape);
      const pattern = '^(' + parts.join('|') + ')$';
      const op = f.operator === 'is_one_of' ? '=~' : '!~';
      return `  | ${field} ${op} ${quoteValue(pattern)}`;
    }
    default:
      return `  # unknown operator ${f.operator} for field ${field}`;
  }
}

// Detect the `^(a|b|c)$` shape so a round-trip through serialize/parse
// preserves is_one_of as is_one_of (rather than degrading to wildcard).
// Returns the comma-joined value list, or null if the pattern doesn't
// match the strict anchored-alternation shape.
function unpackOneOfPattern(pattern) {
  const m = String(pattern || '').match(/^\^\(([^()]+)\)\$$/);
  if (!m) return null;
  const parts = m[1].split('|').map((s) => s.trim()).filter(Boolean);
  if (parts.length === 0) return null;
  // Reject if any part contains regex metachars beyond the alternation
  // (we only round-trip the simple "list of literals" case).
  for (const p of parts) {
    if (/[.*+?^${}()[\]\\|]/.test(p)) return null;
  }
  return parts.join(',');
}

// serializeQuery — render the four-part obs-viewer state as LogQL-ish
// text. Always emits the header comment + selector for shape stability;
// optional sections drop when empty.
export function serializeQuery({ filters = [], time_from = '', time_to = '', search = '' } = {}) {
  const lines = [
    '# obs-viewer query (LogQL-shaped)',
    '# Edit + paste back to restore. Migrate to Loki: replace {} with',
    '# your stream selector, drop the @time: line (use API params).',
    '',
    '{}',
  ];
  if (search) lines.push(`  |= ${quoteValue(search)}`);
  // `| json` is implicit in obs-viewer (everything's already structured)
  // but we emit it so a paste into Loki has the parser stage ready.
  if (filters.length > 0) lines.push('  | json');
  for (const f of filters) lines.push(serializeFilter(f));
  if (time_from || time_to) {
    lines.push('');
    const from = time_from ? isoToTextLocal(time_from) : '*';
    const to = time_to ? isoToTextLocal(time_to) : '*';
    lines.push(`@time: ${from} .. ${to}`);
  }
  return lines.join('\n') + '\n';
}

// isoToTextLocal — render ISO timestamps as YYYY-MM-DD HH:MM:SS (UTC
// wall-clock). Matches the time inputs the TimeFilter component uses.
function isoToTextLocal(iso) {
  if (!iso) return '';
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  return d.toISOString().slice(0, 19).replace('T', ' ');
}

// textToIso — accept YYYY-MM-DD HH:MM:SS (UTC) and return ISO. Returns
// '' if value is empty/`*`/invalid.
function textToIso(text) {
  const t = (text || '').trim();
  if (!t || t === '*') return '';
  const m = t.match(/^(\d{4}-\d{2}-\d{2})[ T](\d{2}:\d{2}(?::\d{2})?)$/);
  if (!m) return '';
  const d = new Date(`${m[1]}T${m[2]}Z`);
  return isNaN(d.getTime()) ? '' : d.toISOString();
}

// autoFixContainsRegex detects the very common LLM anti-pattern where
// the model writes `.WORD.` (single dots on either side) when it
// meant "contains WORD". A single `.` in regex matches one literal
// character, so `.web.` requires a character on each side of `web`
// and won't match `web` standalone or `web-foo`. The intent is
// obvious so we silently rewrite to `.*WORD.*` and return a warning
// for the UI to surface.
//
// Conservative trigger: rewrite ONLY when the pattern is exactly
// `.WORD.` where WORD contains no regex metacharacters at all (word
// chars, hyphen, colon, slash). Anything more complex is left alone.
function autoFixContainsRegex(value) {
  const m = String(value || '').match(/^\.([\w:/-]+)\.$/);
  if (!m) return null;
  return { fixed: '.*' + m[1] + '.*', original: value };
}

// canonicalizeText pre-processes the input so the line-based parser
// below can consume single-line LogQL too. LLMs (Copilot in
// particular) routinely emit `{} |= "a" |= "b" | json | f = "v"` on
// one line; without this step every such paste would fail. We split
// on top-level ` |` boundaries that are OUTSIDE quoted strings so a
// legitimate `|~ "(a|b)"` doesn't shred. Idempotent — already-multi-
// line input passes through unchanged because each line is processed
// independently.
function canonicalizeText(text) {
  return text.split(/\r?\n/).map(splitTopLevelPipes).join('\n');
}

function splitTopLevelPipes(line) {
  const out = [];
  let inQuote = false;
  let segStart = 0;
  for (let i = 0; i < line.length; i++) {
    const c = line[i];
    // Track quote state. Backslash-escaped quotes don't toggle.
    if (c === '"' && line[i - 1] !== '\\') inQuote = !inQuote;
    // Top-level ` |` outside quotes is a stage boundary.
    if (!inQuote && c === ' ' && line[i + 1] === '|') {
      out.push(line.slice(segStart, i));
      segStart = i + 1;
    }
  }
  out.push(line.slice(segStart));
  if (out.length === 1) return line;
  // First segment keeps original indentation; subsequent ones get the
  // canonical 2-space indent so the existing regexes (which trim) still
  // match. The first segment is usually `{}` or empty whitespace.
  return out[0] + '\n' + out.slice(1).map((s) => '  ' + s).join('\n');
}

// Parse a label-filter pipe line into a filter object. Returns null when
// the line shape is recognised but the operator/value combo isn't
// supported (caller falls through and reports a parse error).
//
// `warnings` is mutated when an auto-fix is applied so the UI can
// surface "corrected N patterns" feedback.
function parseLabelFilter(field, op, value, warnings) {
  switch (op) {
    case '=':
      // `field = ""` → does_not_exist; otherwise plain `is`.
      if (value === '') return { field, operator: 'does_not_exist', value: '' };
      return { field, operator: 'is', value };
    case '!=':
      return { field, operator: 'is_not', value };
    case '=~': {
      // `field =~ ".+"` → exists.
      if (value === '.+') return { field, operator: 'exists', value: '' };
      // `field =~ "^(a|b|c)$"` → is_one_of (round-trips losslessly when
      // the values are simple literals).
      const oneOf = unpackOneOfPattern(value);
      if (oneOf !== null) return { field, operator: 'is_one_of', value: oneOf };
      const fix = autoFixContainsRegex(value);
      if (fix) {
        warnings.push(`auto-corrected ${field} =~ "${fix.original}" → "${fix.fixed}" (single-dot regex almost never matches what's intended)`);
        return { field, operator: 'wildcard', value: fix.fixed };
      }
      return { field, operator: 'wildcard', value };
    }
    case '!~': {
      const oneOf = unpackOneOfPattern(value);
      if (oneOf !== null) return { field, operator: 'is_not_one_of', value: oneOf };
      const fix = autoFixContainsRegex(value);
      if (fix) {
        warnings.push(`auto-corrected ${field} !~ "${fix.original}" → "${fix.fixed}"`);
        return { field, operator: 'not_wildcard', value: fix.fixed };
      }
      return { field, operator: 'not_wildcard', value };
    }
    default:
      return null;
  }
}

// parseQuery — turn DSL text back into obs-viewer state. Always returns
// the four state fields; `error` is set with a human-readable message
// when parsing fails halfway through. Partial state up to the failing
// line is preserved so callers can show "applied through line N".
export function parseQuery(text) {
  const out = { filters: [], time_from: '', time_to: '', search: '', warnings: [] };
  if (!text || !text.trim()) return out;
  // Tolerate one-liner pipe stages (Copilot et al. emit these). The
  // canonicalizer is idempotent: already-multi-line input passes
  // through unchanged.
  const canonical = canonicalizeText(text);
  const lines = canonical.split(/\r?\n/);
  let sawSelector = false;
  let logicForNext = 'and'; // currently always AND; reserved for future OR support
  for (let i = 0; i < lines.length; i++) {
    const raw = lines[i];
    const trimmed = raw.trim();
    if (!trimmed) continue;
    if (trimmed.startsWith('#')) continue;
    // Time extension line.
    const tm = trimmed.match(RE_TIME);
    if (tm) {
      out.time_from = textToIso(tm[1]);
      out.time_to = textToIso(tm[2]);
      continue;
    }
    // Stream selector.
    if (RE_SELECTOR.test(trimmed)) { sawSelector = true; continue; }
    if (trimmed.startsWith('{') && trimmed.endsWith('}')) {
      // Non-empty selector — forward-compat with future Loki migration.
      // For now we accept and ignore the labels.
      sawSelector = true;
      continue;
    }
    // Parser stage — accept and ignore for obs-viewer.
    if (RE_PARSER.test(trimmed)) continue;
    // Label filter: `| field op "value"`.
    const lf = trimmed.match(RE_LABEL);
    if (lf) {
      const filter = parseLabelFilter(lf[1], lf[2], lf[3], out.warnings);
      if (!filter) {
        return { ...out, error: `line ${i + 1}: unsupported operator "${lf[2]}"` };
      }
      filter.logic = out.filters.length === 0 ? undefined : logicForNext;
      out.filters.push(filter);
      continue;
    }
    // Line filter: `|= "phrase"` etc. — first one becomes the search
    // channel; further line filters concat with " " as separators.
    const lnf = trimmed.match(RE_LINE_FILTER);
    if (lnf) {
      const op = lnf[1];
      const val = lnf[2];
      if (op === '=') {
        // |= "phrase" — substring match on the row text (free-text search)
        out.search = out.search ? `${out.search} ${val}` : val;
      } else if (op === '!=' || op === '~' || op === '!~') {
        // Negative / regex line filters don't have a direct slot in the
        // obs-viewer model (we only carry one positive substring). Accept
        // them and fold into `search` as best-effort, with a comment-style
        // marker so a re-serialize loses no information silently.
        return { ...out, error: `line ${i + 1}: line filter "${op}" not yet supported (only |= "..." round-trips)` };
      }
      continue;
    }
    return { ...out, error: `line ${i + 1}: unrecognised "${trimmed.slice(0, 60)}"` };
  }
  if (!sawSelector && (out.filters.length > 0 || out.search)) {
    return { ...out, error: `expected "{}" stream selector before any | line` };
  }
  return out;
}
