// Query state in the URL fragment, so a view can be copied to someone
// else, bookmarked, or survive a reload.
//
// **The fragment, never the query string.** Everything here is derived
// from log content — a filter value can be a hostname, a user name, a
// fragment of a message body. Anything after `#` is not sent to the
// server, so none of it reaches an access log, a proxy, or a referrer
// header on an outbound link. That is not a nicety: obs-viewer runs
// against files people are not supposed to paste into a web form.
//
// **The query only, not the loaded files.** A path is meaningful on the
// machine that produced it and nowhere else, so encoding one would ship
// a link that silently resolves to nothing on the recipient's machine.
// The recipient loads their own files and gets the sender's question
// applied to them, which is both honest and usually what was wanted.
//
// Filters are JSON rather than the pipeline-style text form. The text
// form is for humans to read and paste into a conversation, and it
// deliberately loses the distinction between `contains` and `wildcard`
// because they compile to identical SQL. That is a fine trade for a
// message in a chat window and a poor one here, where the round trip
// happens on every reload and would rewrite the operator's own filter
// rows in front of them.

const KEYS = {
  filters: 'f',
  timeFrom: 'from',
  timeTo: 'to',
  searchText: 'q',
  sortField: 'sort',
  sortOrder: 'order',
  hiddenColumns: 'hide',
  limit: 'limit',
};

export const EMPTY_STATE = {
  filters: [],
  timeFrom: '',
  timeTo: '',
  searchText: '',
  sortField: '',
  sortOrder: '',
  hiddenColumns: [],
  limit: 0,
};

// encodeState renders the shareable part of the view as a URL fragment,
// without the leading '#'. Empty values are omitted rather than written
// as blanks — a default view produces an empty string, which keeps the
// address bar clean until there is something worth sharing.
export function encodeState(state) {
  const p = new URLSearchParams();
  const s = { ...EMPTY_STATE, ...state };

  const filters = (s.filters || []).filter((f) => f && f.field);
  if (filters.length > 0) {
    p.set(KEYS.filters, JSON.stringify(filters.map(compactFilter)));
  }
  if (s.timeFrom) p.set(KEYS.timeFrom, s.timeFrom);
  if (s.timeTo) p.set(KEYS.timeTo, s.timeTo);
  if (s.searchText) p.set(KEYS.searchText, s.searchText);
  if (s.sortField) p.set(KEYS.sortField, s.sortField);
  if (s.sortOrder && s.sortOrder !== 'asc') p.set(KEYS.sortOrder, s.sortOrder);
  if (s.hiddenColumns?.length > 0) p.set(KEYS.hiddenColumns, s.hiddenColumns.join(','));
  if (s.limit && s.limit !== 200) p.set(KEYS.limit, String(s.limit));

  return p.toString();
}

// decodeState reads a fragment back. Anything malformed is dropped
// rather than thrown: a truncated link — and links get truncated by
// every chat client that has ever existed — should restore whatever
// survived, not fail to open.
export function decodeState(hash) {
  const out = { ...EMPTY_STATE };
  const raw = String(hash || '').replace(/^#/, '');
  if (!raw) return out;

  let p;
  try {
    p = new URLSearchParams(raw);
  } catch {
    return out;
  }

  const f = p.get(KEYS.filters);
  if (f) {
    try {
      const parsed = JSON.parse(f);
      if (Array.isArray(parsed)) out.filters = parsed.map(expandFilter).filter((x) => x.field);
    } catch { /* keep the rest of the link */ }
  }
  out.timeFrom = p.get(KEYS.timeFrom) || '';
  out.timeTo = p.get(KEYS.timeTo) || '';
  out.searchText = p.get(KEYS.searchText) || '';
  out.sortField = p.get(KEYS.sortField) || '';
  out.sortOrder = p.get(KEYS.sortOrder) === 'desc' ? 'desc' : '';

  const hide = p.get(KEYS.hiddenColumns);
  out.hiddenColumns = hide ? hide.split(',').filter(Boolean) : [];

  const limit = parseInt(p.get(KEYS.limit) || '', 10);
  out.limit = Number.isFinite(limit) && limit > 0 ? limit : 0;

  return out;
}

// Filters go over the wire with short keys. A filter row is four
// fields and a view can carry a dozen of them, so the long spelling
// roughly triples the length of the part of the link most likely to be
// cut off in transit.
function compactFilter(f) {
  const out = { f: f.field, o: f.operator, v: f.value };
  if (f.logic && f.logic !== 'AND') out.l = f.logic;
  return out;
}

function expandFilter(f) {
  if (!f || typeof f !== 'object') return {};
  return {
    field: f.f ?? f.field ?? '',
    operator: f.o ?? f.operator ?? 'is',
    value: f.v ?? f.value ?? '',
    logic: f.l ?? f.logic ?? 'AND',
  };
}

// writeState replaces the fragment in place.
//
// replaceState rather than pushState: the state changes on every
// keystroke in a filter value, and a history entry per keystroke turns
// the browser's Back button into an undo log for typing — which is not
// what anyone presses it for.
export function writeState(state) {
  const encoded = encodeState(state);
  const url = window.location.pathname + window.location.search + (encoded ? '#' + encoded : '');
  try {
    window.history.replaceState(null, '', url);
  } catch { /* file:// and some embedded views refuse; the app still works */ }
}

// shareableURL is what the Copy link button puts on the clipboard.
export function shareableURL(state) {
  const encoded = encodeState(state);
  return window.location.origin + window.location.pathname + (encoded ? '#' + encoded : '');
}
