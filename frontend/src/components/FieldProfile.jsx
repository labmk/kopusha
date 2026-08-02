import React, { useState } from 'react';
import { api } from '../api/client';

// FieldProfile — what is actually in the data, before a query is
// written.
//
// The hardest moment for someone new is not writing the query; the DSL
// is small and there is a visual builder for it. It is not knowing what
// the data contains — which fields exist, which are populated, what
// values they take. Without this the only way to find out is to load a
// file and read rows until you have a feel for it.
//
// Clicking a value adds it as a filter. That is what makes this
// navigation rather than a report: look, then formulate, then narrow —
// without typing a field name once.
export default function FieldProfile({ profile, loading, queryParams, onAddFilter, onClose }) {
  // Value distributions are fetched per field, on expand. Each one
  // needs its own GROUP BY, so doing them all up front would turn one
  // scan into one per column.
  const [expanded, setExpanded] = useState(null);
  const [values, setValues] = useState({});
  const [valuesLoading, setValuesLoading] = useState(null);

  const toggle = async (name) => {
    if (expanded === name) { setExpanded(null); return; }
    setExpanded(name);
    if (values[name]) return;
    setValuesLoading(name);
    try {
      const res = await api.fieldValues({ ...queryParams, field: name, top: 15 });
      setValues((v) => ({ ...v, [name]: res }));
    } catch {
      setValues((v) => ({ ...v, [name]: { values: [], total: 0 } }));
    } finally {
      setValuesLoading(null);
    }
  };

  const total = profile?.total ?? 0;
  const fields = profile?.fields || [];

  return (
    <aside className="field-profile" aria-label="Field profile">
      <div className="field-profile-head">
        <span>
          Fields
          {total > 0 && (
            <span className="field-profile-total"> over {total.toLocaleString()} rows</span>
          )}
        </span>
        <button className="btn btn-sm" onClick={onClose} title="Close (Esc)">&times;</button>
      </div>

      {loading && fields.length === 0 && (
        <div className="loading"><span className="spinner" /> Profiling…</div>
      )}

      {!loading && fields.length === 0 && (
        <div className="empty-state" style={{ fontSize: '12px' }}>
          Nothing to profile yet.
        </div>
      )}

      <div className={`field-profile-body${loading ? ' is-stale' : ''}`}>
        {fields.map((f) => {
          const fill = total > 0 ? f.present / total : 0;
          const isOpen = expanded === f.name;
          return (
            <div key={f.name} className={`fp-field${isOpen ? ' is-open' : ''}`}>
              <button className="fp-field-head" onClick={() => toggle(f.name)}>
                <span className="fp-name" title={f.name}>{f.name}</span>
                <span className="fp-stats">
                  {/* Distinct is approximate above a few hundred — the
                      question it answers is "identifier or category?",
                      which does not turn on the last digit. */}
                  <span title="Approximate number of distinct values">
                    {f.distinct.toLocaleString()}
                  </span>
                  <span
                    className={fill < 1 ? 'fp-fill-partial' : ''}
                    title={`${f.present.toLocaleString()} of ${total.toLocaleString()} rows have a value`}
                  >
                    {formatPercent(fill)}
                  </span>
                </span>
              </button>

              {/* A field carried by only some of the loaded files is a
                  fact about the sources, not about missing data — and
                  it is usually the more useful of the two. */}
              {f.files_total > 1 && f.files < f.files_total && (
                <div className="fp-files">in {f.files} of {f.files_total} files</div>
              )}

              <div className="fp-bar" aria-hidden="true">
                <div className="fp-bar-fill" style={{ width: `${fill * 100}%` }} />
              </div>

              {isOpen && (
                <div className="fp-values">
                  {valuesLoading === f.name && (
                    <div className="fp-values-loading"><span className="spinner" /></div>
                  )}
                  {values[f.name]?.values?.length === 0 && valuesLoading !== f.name && (
                    <div className="fp-hint">No values — this field is empty in every matching row.</div>
                  )}
                  {(values[f.name]?.values || []).map((v) => {
                    const share = f.present > 0 ? v.count / f.present : 0;
                    return (
                      <button
                        key={v.value}
                        className="fp-value"
                        onClick={() => onAddFilter(f.name, v.value)}
                        title={`Filter to ${f.name} = ${v.value}`}
                      >
                        <span className="fp-value-bar" style={{ width: `${share * 100}%` }} />
                        <span className="fp-value-text">{v.value}</span>
                        <span className="fp-value-count">
                          {v.count.toLocaleString()} · {formatPercent(share)}
                        </span>
                      </button>
                    );
                  })}
                </div>
              )}
            </div>
          );
        })}
      </div>

      {profile?.truncated && (
        <div className="fp-hint fp-truncated">
          Only the first {fields.length} fields are shown.
        </div>
      )}
    </aside>
  );
}

// Percentages round toward the informative answer: a field present in
// 99.7% of rows must not read as 100%, because "always there" and
// "nearly always there" lead to different queries.
function formatPercent(x) {
  if (x <= 0) return '0%';
  if (x >= 1) return '100%';
  const pct = x * 100;
  if (pct >= 99.5) return '99.9%';
  if (pct < 0.1) return '<0.1%';
  return `${pct.toFixed(pct < 10 ? 1 : 0)}%`;
}
