import React from 'react';

// RowDetailPanel — the full contents of one selected row, beside the
// table rather than inside it.
//
// This replaced in-place row expansion. Two reasons, and the second is
// the one that mattered:
//
//   - Expanding in place pushed every following row off screen, so
//     reading a record meant losing your place in the list.
//   - Variable row heights make windowing impractical. With every row
//     the same height, the virtual scroller is arithmetic; without it,
//     it needs per-row measurement and a cache.
//
// The panel is deliberately not a modal: the point is to read a record
// *while* still seeing its neighbours, which is most of what log triage
// is.
export default function RowDetailPanel({ row, index, total, hideNulls, onClose, onNavigate }) {
  if (!row) return null;

  const entries = Object.entries(row).filter(
    ([, v]) => !hideNulls || !isNullish(v)
  );

  return (
    <aside className="row-detail" aria-label="Row detail">
      <header className="row-detail-header">
        <span className="row-detail-title">
          Row {index + 1}
          {total ? <span className="row-detail-total"> of {total}</span> : null}
        </span>
        <div className="row-detail-actions">
          <button
            className="btn btn-sm"
            onClick={() => onNavigate(-1)}
            disabled={index <= 0}
            title="Previous row (k)"
          >
            ↑
          </button>
          <button
            className="btn btn-sm"
            onClick={() => onNavigate(1)}
            disabled={total ? index >= total - 1 : false}
            title="Next row (j)"
          >
            ↓
          </button>
          <button className="btn btn-sm" onClick={onClose} title="Close (Esc)">
            ✕
          </button>
        </div>
      </header>

      <div className="row-detail-body">
        {/* A field list rather than a JSON blob: the values are what
            people read, and a two-column layout beats scanning for a
            key inside pretty-printed JSON. Objects and arrays still
            render as JSON, because there is no better shape for them. */}
        <dl className="row-detail-fields">
          {entries.map(([key, value]) => (
            <React.Fragment key={key}>
              <dt title={key}>{key}</dt>
              <dd>
                <span className="row-detail-value">{renderValue(value)}</span>
              </dd>
            </React.Fragment>
          ))}
        </dl>
        {entries.length === 0 && (
          <div className="empty-state">Every field on this row is empty.</div>
        )}
      </div>
    </aside>
  );
}

function isNullish(v) {
  if (v === null || v === undefined) return true;
  if (typeof v === 'string' && v === '') return true;
  if (Array.isArray(v) && v.length === 0) return true;
  if (typeof v === 'object' && !Array.isArray(v) && Object.keys(v).length === 0) return true;
  return false;
}

function renderValue(v) {
  if (v === null || v === undefined) return '';
  if (typeof v === 'object') return JSON.stringify(v, null, 2);
  return String(v);
}
