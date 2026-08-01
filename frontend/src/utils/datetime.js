// Datetime helpers shared by TimeFilter and LogTable.

// Strict 24H format: "YYYY-MM-DD HH:MM:SS"
export const DT_RE = /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/;

export function isValidDateTime(text) {
  if (!text) return true;
  if (!DT_RE.test(text)) return false;
  const d = new Date(text.replace(' ', 'T') + 'Z');
  return !isNaN(d.getTime());
}

export function isoToText(iso) {
  if (!iso) return '';
  const d = new Date(iso);
  if (isNaN(d.getTime())) return '';
  return d.toISOString().slice(0, 19).replace('T', ' ');
}

// Accepts "YYYY-MM-DD HH:MM[:SS]" (UTC) or any ISO string. Returns ISO or null.
export function parseUserDateTime(text) {
  const t = (text || '').trim();
  if (!t) return null;
  const m = t.match(/^(\d{4}-\d{2}-\d{2})[ T](\d{2}:\d{2}(?::\d{2})?)$/);
  const candidate = m ? `${m[1]}T${m[2]}Z` : t;
  const d = new Date(candidate);
  return isNaN(d.getTime()) ? null : d.toISOString();
}

// Today at 00:00:00 UTC, as an ISO string.
export function todayStartIso() {
  const d = new Date();
  d.setUTCHours(0, 0, 0, 0);
  return d.toISOString();
}

// Today at 23:59:59 UTC, as an ISO string.
export function todayEndIso() {
  const d = new Date();
  d.setUTCHours(23, 59, 59, 0);
  return d.toISOString();
}

// Compact human format for a duration in seconds.
// 60       → "1m"
// 3661     → "1h 1m"
// 90061    → "1d 1h"
// 0 / null → ""
export function formatTimeout(seconds) {
  const s = Number(seconds);
  if (!Number.isFinite(s) || s <= 0) return '';
  if (s < 60) return `${Math.round(s)}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  const remM = m % 60;
  if (h < 24) return remM ? `${h}h ${remM}m` : `${h}h`;
  const d = Math.floor(h / 24);
  const remH = h % 24;
  return remH ? `${d}d ${remH}h` : `${d}d`;
}

export function formatTimestamp(value, timezone, hourFormat = '24') {
  if (!value) return '';
  try {
    const date = new Date(value);
    if (isNaN(date.getTime())) return String(value);
    const hour12 = hourFormat === '12';
    if (timezone === 'UTC') {
      if (!hour12) return date.toISOString().replace('T', ' ').replace('Z', '');
      return date.toLocaleString('en-US', {
        timeZone: 'UTC',
        year: 'numeric', month: '2-digit', day: '2-digit',
        hour: '2-digit', minute: '2-digit', second: '2-digit',
        fractionalSecondDigits: 3,
        hour12: true,
      });
    }
    const opts = {
      year: 'numeric', month: '2-digit', day: '2-digit',
      hour: '2-digit', minute: '2-digit', second: '2-digit',
      fractionalSecondDigits: 3,
      hour12,
    };
    if (timezone === 'local') return date.toLocaleString(undefined, opts);
    return date.toLocaleString('en-US', { ...opts, timeZone: timezone });
  } catch {
    return String(value);
  }
}
