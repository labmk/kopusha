import React, { useState, useCallback, useEffect } from 'react';
import { isValidDateTime, isoToText, parseUserDateTime } from '../utils/datetime';

// Plain text 24H datetime field: "YYYY-MM-DD HH:MM:SS" (UTC).
function TextDateTime({ value, onCommit, title }) {
  const [text, setText] = useState(() => isoToText(value));
  useEffect(() => { setText(isoToText(value)); }, [value]);

  const valid = isValidDateTime(text);
  const invalidStyle = valid ? {} : {
    borderColor: 'var(--danger)',
    boxShadow: '0 0 0 1px var(--danger)',
  };

  return (
    <input
      type="text"
      className="input input-mono"
      value={text}
      onChange={(e) => setText(e.target.value)}
      onBlur={() => { if (valid) onCommit(text); }}
      onKeyDown={(e) => {
        if (e.key === 'Enter') { e.preventDefault(); if (valid) onCommit(text); }
        if (e.key === 'Escape') setText(isoToText(value));
      }}
      placeholder="YYYY-MM-DD HH:MM:SS"
      spellCheck={false}
      style={{ width: '170px', fontSize: '11px', ...invalidStyle }}
      title={valid ? title : 'Invalid format — expected YYYY-MM-DD HH:MM:SS'}
    />
  );
}

const PRESETS = [
  { label: '15m', minutes: 15 },
  { label: '1h', minutes: 60 },
  { label: '4h', minutes: 240 },
  { label: '24h', minutes: 1440 },
  { label: '7d', minutes: 10080 },
];

export default function TimeFilter({ timeFrom, timeTo, onTimeFromChange, onTimeToChange }) {
  const [activePreset, setActivePreset] = useState(null);

  const applyPreset = useCallback((preset) => {
    const now = new Date();
    const fromDate = new Date(now.getTime() - preset.minutes * 60 * 1000);
    onTimeFromChange(fromDate.toISOString());
    onTimeToChange(now.toISOString());
    setActivePreset(preset.label);
  }, [onTimeFromChange, onTimeToChange]);

  const clearTimeFilter = () => {
    onTimeFromChange(null);
    onTimeToChange(null);
    setActivePreset(null);
  };

  const commitFrom = (val) => { onTimeFromChange(parseUserDateTime(val)); setActivePreset(null); };
  const commitTo = (val) => { onTimeToChange(parseUserDateTime(val)); setActivePreset(null); };

  return (
    <div className="time-filter">
      <div className="time-presets">
        {PRESETS.map((p) => (
          <button
            key={p.label}
            className={`time-preset ${activePreset === p.label ? 'active' : ''}`}
            onClick={() => applyPreset(p)}
          >
            {p.label}
          </button>
        ))}
      </div>

      <TextDateTime value={timeFrom} onCommit={commitFrom} title="From (UTC, 24H)" />
      <span style={{ color: 'var(--text-muted)', fontSize: '11px' }}>to</span>
      <TextDateTime value={timeTo} onCommit={commitTo} title="To (UTC, 24H)" />

      {(timeFrom || timeTo) && (
        <button className="btn btn-sm" onClick={clearTimeFilter} title="Clear time filter">
          &times;
        </button>
      )}
    </div>
  );
}
