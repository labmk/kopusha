import React from 'react';

// ParseDiagnosis — what every adapter thought of a file, and why.
//
// This is the screen shown at the moment a load fails, which is the
// moment the tool either earns trust or gets closed. "It did not work
// and I do not know why" is worse than a clear failure, so the panel
// answers the three questions in the order they get asked: what was
// tried, what did the parser actually see, and what can I do now.
//
// The last one is the button. A file that matched nothing is exactly
// the input the rule builder needs, so the failure is also the entry
// point to the feature that fixes it.
export default function ParseDiagnosis({ diagnosis, fileName, onBuildRule, onClose }) {
  if (!diagnosis) return null;

  const matched = !!diagnosis.chosen;
  const adapters = diagnosis.adapters || [];
  const notes = diagnosis.notes || [];

  return (
    <div className="parse-diagnosis">
      <div className="parse-diagnosis-head">
        <span className={matched ? 'diag-verdict-ok' : 'diag-verdict-none'}>
          {fileName ? <strong>{fileName}</strong> : null}
          {matched
            ? ` — handled by ${diagnosis.chosen} (score ${diagnosis.best_score})`
            : ' — no rule matched'}
        </span>
        {onClose && (
          <button className="btn btn-sm" onClick={onClose} title="Close">&times;</button>
        )}
      </div>

      <table className="diag-table">
        <tbody>
          {adapters.map((a) => (
            <tr key={a.name} className={a.score > 0 ? 'diag-row-scored' : ''}>
              <td className="diag-adapter">{a.name}</td>
              <td className="diag-score">{a.score}</td>
              <td className="diag-reason">{a.reason || '—'}</td>
            </tr>
          ))}
        </tbody>
      </table>

      {diagnosis.first_line && (
        <div className="diag-first-line">
          <div className="diag-label">first line seen</div>
          {/* Rendered as-is rather than trimmed: leading whitespace is
              part of why a rule anchored with ^ did not match, and
              tidying it away would hide the answer. */}
          <pre>{diagnosis.first_line}</pre>
        </div>
      )}

      {notes.length > 0 && (
        <ul className="diag-notes">
          {notes.map((n, i) => <li key={i}>{n}</li>)}
        </ul>
      )}

      {onBuildRule && (
        <div className="diag-actions">
          <button className="btn btn-primary btn-sm" onClick={onBuildRule}>
            Build a rule from this line
          </button>
          <span className="diag-hint">
            Opens the rule builder with this line as the sample.
          </span>
        </div>
      )}
    </div>
  );
}
