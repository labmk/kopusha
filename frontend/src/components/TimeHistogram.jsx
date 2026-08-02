import React, { useRef, useState } from 'react';
import { formatTimestamp } from '../utils/datetime';

// TimeHistogram — record counts over time for the current filter,
// above the table.
//
// It answers "when did this happen" and "is this normal" before the
// operator has to form either question, and it turns time filtering
// from typing into a gesture: drag across the bars to narrow the range.
// That matters for the people this is for, who are not going to type an
// ISO-8601 timestamp into a box.
//
// The strip is deliberately silent when it has nothing to say. No
// timestamp column, no matching rows, or a backend that declined —
// each renders nothing at all rather than an empty frame, because a
// permanently blank chart is worse than no chart.
export default function TimeHistogram({ data, timezone, hourFormat, onRangeSelect, loading }) {
  const trackRef = useRef(null);
  // Drag state as fractions of the track width, so the bars and the
  // selection overlay cannot disagree about where the pointer is.
  const [drag, setDrag] = useState(null);

  const buckets = data?.buckets || [];
  if (buckets.length === 0) return null;

  const max = buckets.reduce((m, b) => (b.count > m ? b.count : m), 0);
  if (max <= 0) return null;

  const startMs = new Date(buckets[0].start).getTime();
  // The last bar covers a whole interval, so the span runs to its end,
  // not to its start — otherwise a drag over the final bar maps to a
  // range that excludes the records in it.
  const intervalMs = (data.interval_seconds || 1) * 1000;
  const endMs = new Date(buckets[buckets.length - 1].start).getTime() + intervalMs;
  const spanMs = Math.max(endMs - startMs, 1);

  const fractionAt = (clientX) => {
    const rect = trackRef.current?.getBoundingClientRect();
    if (!rect || rect.width === 0) return 0;
    return Math.min(1, Math.max(0, (clientX - rect.left) / rect.width));
  };

  const instantAt = (fraction) => new Date(startMs + fraction * spanMs);

  const onPointerDown = (e) => {
    if (e.button !== 0) return;
    const f = fractionAt(e.clientX);
    setDrag({ from: f, to: f });
    e.currentTarget.setPointerCapture(e.pointerId);
  };

  const onPointerMove = (e) => {
    if (!drag) return;
    setDrag((d) => ({ ...d, to: fractionAt(e.clientX) }));
  };

  const onPointerUp = () => {
    if (!drag) return;
    const lo = Math.min(drag.from, drag.to);
    const hi = Math.max(drag.from, drag.to);
    setDrag(null);
    // A click is a drag of zero width. Treat anything under a bar's
    // worth of travel as a click and ignore it, so a stray press does
    // not collapse the range to an instant that matches nothing.
    if ((hi - lo) * spanMs < intervalMs) return;
    onRangeSelect(toLocalInput(instantAt(lo)), toLocalInput(instantAt(hi)));
  };

  const sel = drag && {
    left: `${Math.min(drag.from, drag.to) * 100}%`,
    width: `${Math.abs(drag.to - drag.from) * 100}%`,
  };

  return (
    <div className={`time-histogram${loading ? ' is-loading' : ''}`}>
      <div
        className="time-histogram-track"
        ref={trackRef}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onPointerCancel={() => setDrag(null)}
        role="presentation"
        title="Drag across the bars to narrow the time range"
      >
        {buckets.map((b) => (
          <div
            key={b.start}
            className="time-histogram-bar"
            style={{ height: `${Math.max((b.count / max) * 100, 2)}%` }}
            title={`${formatTimestamp(b.start, timezone, hourFormat)} · ${b.count.toLocaleString()}`}
          />
        ))}
        {sel && <div className="time-histogram-selection" style={sel} />}
      </div>
      <div className="time-histogram-axis">
        <span>{formatTimestamp(buckets[0].start, timezone, hourFormat)}</span>
        <span className="time-histogram-meta">
          {describeInterval(data.interval_seconds)} per bar · peak {max.toLocaleString()}
        </span>
        <span>{formatTimestamp(new Date(endMs).toISOString(), timezone, hourFormat)}</span>
      </div>
    </div>
  );
}

// toLocalInput renders an instant in the shape the time inputs and the
// backend both use: `YYYY-MM-DD HH:MM:SS`, UTC. The bars are drawn from
// UTC instants, so the range a drag produces has to be UTC too — going
// through the viewer's display timezone here would shift the filter by
// the offset it was only meant to label.
function toLocalInput(d) {
  return d.toISOString().slice(0, 19).replace('T', ' ');
}

function describeInterval(seconds) {
  if (!seconds) return '';
  const units = [
    [31536000, 'year'],
    [2592000, 'month'],
    [604800, 'week'],
    [86400, 'day'],
    [3600, 'hour'],
    [60, 'minute'],
    [1, 'second'],
  ];
  for (const [size, name] of units) {
    if (seconds >= size) {
      const n = Math.round(seconds / size);
      return `${n} ${name}${n === 1 ? '' : 's'}`;
    }
  }
  return `${seconds}s`;
}
