import React, { useState, useMemo, useCallback, useEffect } from 'react';
import * as Popover from '@radix-ui/react-popover';
import { formatTimestamp } from '../utils/datetime';
import { useVirtualRows, ROW_HEIGHT } from '../hooks/useVirtualRows';
import RowDetailPanel from './RowDetailPanel';

// Shared empty Set so the default prop is referentially stable — a new
// Set() per render would re-run every memo that depends on it.
const EMPTY_HIDDEN = new Set();

function isNullish(v) {
  if (v === null || v === undefined) return true;
  if (typeof v === 'string' && v === '') return true;
  if (Array.isArray(v) && v.length === 0) return true;
  if (typeof v === 'object' && !Array.isArray(v) && Object.keys(v).length === 0) return true;
  return false;
}

function stripNulls(value) {
  if (Array.isArray(value)) {
    const arr = value.map(stripNulls).filter((v) => !isNullish(v));
    return arr;
  }
  if (value && typeof value === 'object') {
    const out = {};
    for (const [k, v] of Object.entries(value)) {
      const nv = stripNulls(v);
      if (!isNullish(nv)) out[k] = nv;
    }
    return out;
  }
  return value;
}

function formatCellValue(value) {
  if (value === null || value === undefined) return '';
  if (typeof value === 'object') return JSON.stringify(value);
  return String(value);
}

// Determine which columns to show (prioritize timestamp and common fields).
//
// `@timestamp` always wins position 0 when present, regardless of which
// timestamp column the engine is sorting by. If the operator switches
// the sort field to ObservedTimestamp (or any other timestamp-like
// column), it's still listed next — but @timestamp stays leftmost so
// the row's primary clock is always in the same place.
function selectColumns(fields, timestampField) {
  if (!fields || fields.length === 0) return [];

  const priority = ['@timestamp', timestampField, 'level', 'log.level', 'severity', 'message', 'msg'];
  const ordered = [];
  const rest = [];

  for (const p of priority) {
    if (p && fields.includes(p) && !ordered.includes(p)) {
      ordered.push(p);
    }
  }
  for (const f of fields) {
    if (!ordered.includes(f)) {
      rest.push(f);
    }
  }

  return [...ordered, ...rest];
}

export default function LogTable({
  result, sortOrder, onSortOrderChange, timestampField, timezone,
  hourFormat = '24', hideNulls = true,
  // Column visibility is owned by App because it is part of the view a
  // link carries — see utils/urlState.js. The table only reports the
  // toggles.
  hiddenColumns = EMPTY_HIDDEN, onHiddenColumnsChange,
}) {
  // A single selected row, shown in a side panel — not a set of
  // in-place expansions. Expansion used to grow the row, which pushed
  // everything below it off screen and, more consequentially, made row
  // heights variable and windowing impractical.
  const [selectedIndex, setSelectedIndex] = useState(null);
  const [showColumnPicker, setShowColumnPicker] = useState(false);

  const setHiddenColumns = useCallback((updater) => {
    if (!onHiddenColumnsChange) return;
    onHiddenColumnsChange((prev) => (typeof updater === 'function' ? updater(prev) : updater));
  }, [onHiddenColumnsChange]);
  const DEFAULT_COL_WIDTH = 180;
  // Column widths keyed by a hash of the current column set — widths persist
  // across sessions as long as the schema is stable, and reset cleanly when
  // the underlying files change shape.
  const colKey = useMemo(() => {
    const fields = result?.fields;
    if (!fields || !fields.length) return null;
    return 'kopusha_col_widths_' + fields.slice().sort().join('|').length + '_' + fields.length;
  }, [result?.fields]);
  const [colWidths, setColWidths] = useState(() => {
    if (!colKey) return {};
    try { return JSON.parse(localStorage.getItem(colKey) || '{}'); } catch { return {}; }
  });
  useEffect(() => {
    if (!colKey) return;
    try { setColWidths(JSON.parse(localStorage.getItem(colKey) || '{}')); } catch { setColWidths({}); }
  }, [colKey]);
  useEffect(() => {
    if (!colKey) return;
    try { localStorage.setItem(colKey, JSON.stringify(colWidths)); } catch {}
  }, [colKey, colWidths]);

  const onResizeStart = useCallback((col, e) => {
    e.preventDefault();
    e.stopPropagation();
    const startX = e.clientX;
    const th = e.currentTarget.parentElement;
    const startWidth = th ? th.offsetWidth : DEFAULT_COL_WIDTH;
    const onMove = (ev) => {
      const w = Math.max(40, startWidth + (ev.clientX - startX));
      setColWidths((prev) => ({ ...prev, [col]: w }));
    };
    const onUp = () => {
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
    };
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
  }, []);

  const columns = useMemo(() => {
    if (!result?.fields) return [];
    return selectColumns(result.fields, timestampField);
  }, [result?.fields, timestampField]);

  const visibleColumns = useMemo(() => {
    return columns.filter((c) => !hiddenColumns.has(c));
  }, [columns, hiddenColumns]);

  // Windowing. Called unconditionally, before the empty-result early
  // return below — hooks cannot live behind a branch.
  const rowCount = result?.rows?.length ?? 0;
  const { containerRef, startIndex, endIndex, padTop, padBottom, onScroll } =
    useVirtualRows(rowCount);

  // j/k and arrows move the selection while the detail panel is open;
  // Escape closes it. Bound at the document because the table rows are
  // not focusable — making them so would fight the click-to-select
  // interaction for very little gain.
  useEffect(() => {
    if (selectedIndex === null) return undefined;
    const onKey = (e) => {
      const tag = e.target?.tagName;
      if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return;
      if (e.key === 'Escape') { setSelectedIndex(null); return; }
      const delta = (e.key === 'j' || e.key === 'ArrowDown') ? 1
        : (e.key === 'k' || e.key === 'ArrowUp') ? -1 : 0;
      if (delta === 0) return;
      e.preventDefault();
      setSelectedIndex((i) => Math.min(rowCount - 1, Math.max(0, i + delta)));
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [selectedIndex, rowCount]);

  if (!result || !result.rows) {
    return <div className="empty-state">No results</div>;
  }

  const toggleRow = (index) => {
    setSelectedIndex((prev) => (prev === index ? null : index));
  };

  const toggleColumn = (col) => {
    setHiddenColumns((prev) => {
      const next = new Set(prev);
      if (next.has(col)) {
        next.delete(col);
      } else {
        next.add(col);
      }
      return next;
    });
  };

  const toggleSortOrder = () => {
    onSortOrderChange(sortOrder === 'asc' ? 'desc' : 'asc');
  };

  const isTimestampCol = (col) => {
    const lower = col.toLowerCase();
    return lower.includes('timestamp') || lower.includes('time') || lower.includes('date') || col === timestampField;
  };

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
      {/* Toolbar */}
      <div style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '4px 12px', borderBottom: '1px solid var(--border)', background: 'var(--bg-secondary)', position: 'relative' }}>
        <button className="btn btn-sm" onClick={toggleSortOrder}>
          Sort: {sortOrder === 'asc' ? '↑ Oldest first' : '↓ Newest first'}
        </button>
        <Popover.Root open={showColumnPicker} onOpenChange={setShowColumnPicker}>
          <Popover.Trigger asChild>
            <button className="btn btn-sm">
              Columns ({visibleColumns.length}/{columns.length})
            </button>
          </Popover.Trigger>
          <Popover.Portal>
            <Popover.Content
              className="rdx-popover"
              align="start"
              sideOffset={4}
              style={{ minWidth: '200px', maxHeight: '300px' }}
            >
              <div style={{ padding: '6px 12px', borderBottom: '1px solid var(--border)', display: 'flex', justifyContent: 'space-between' }}>
                <span style={{ fontSize: '11px', color: 'var(--text-secondary)' }}>Toggle columns</span>
                <Popover.Close asChild>
                  <button className="btn btn-sm" aria-label="Close">&times;</button>
                </Popover.Close>
              </div>
              <div style={{ overflowY: 'auto' }}>
                {columns.map((col) => (
                  <label key={col} style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '4px 12px', fontSize: '12px', fontFamily: 'var(--font-mono)', cursor: 'pointer' }}>
                    <input
                      type="checkbox"
                      checked={!hiddenColumns.has(col)}
                      onChange={() => toggleColumn(col)}
                      style={{ accentColor: 'var(--accent)' }}
                    />
                    {col}
                  </label>
                ))}
              </div>
            </Popover.Content>
          </Popover.Portal>
        </Popover.Root>
      </div>

      {/* Table */}
      <div className="log-table-split">
      <div className="log-table-container" ref={containerRef} onScroll={onScroll}>
        <table className="log-table" style={{ tableLayout: 'fixed', width: 'max-content', minWidth: '100%' }}>
          <colgroup>
            <col style={{ width: '30px' }} />
            {visibleColumns.map((col) => (
              <col key={col} style={{ width: `${colWidths[col] || DEFAULT_COL_WIDTH}px` }} />
            ))}
          </colgroup>
          <thead>
            <tr>
              <th></th>
              {visibleColumns.map((col) => (
                <th key={col} title={col} style={{ position: 'relative' }}>
                  {col}
                  {col === timestampField && (
                    <span style={{ marginLeft: '4px', fontSize: '10px', color: 'var(--accent)' }}>⏱</span>
                  )}
                  <div
                    onMouseDown={(e) => onResizeStart(col, e)}
                    onClick={(e) => e.stopPropagation()}
                    style={{
                      position: 'absolute',
                      top: 0,
                      right: 0,
                      width: '6px',
                      height: '100%',
                      cursor: 'col-resize',
                      userSelect: 'none',
                    }}
                  />
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {/* Spacer standing in for every row scrolled past. Keeping
                it inside the table means the browser still sizes the
                columns from the rendered rows. */}
            {padTop > 0 && <tr aria-hidden="true" style={{ height: padTop }} />}
            {result.rows.slice(startIndex, endIndex).map((row, offset) => {
              const i = startIndex + offset;
              return (
                <tr
                  key={i}
                  onClick={() => toggleRow(i)}
                  className={selectedIndex === i ? 'row-selected' : undefined}
                  style={{ cursor: 'pointer', height: ROW_HEIGHT }}
                >
                  <td className="expand-btn">{selectedIndex === i ? '▼' : '▶'}</td>
                  {visibleColumns.map((col) => (
                    <td key={col}>
                      {isTimestampCol(col)
                        ? formatTimestamp(row[col], timezone, hourFormat)
                        : formatCellValue(row[col])}
                    </td>
                  ))}
                </tr>
              );
            })}
            {padBottom > 0 && <tr aria-hidden="true" style={{ height: padBottom }} />}
          </tbody>
        </table>
      </div>
      {selectedIndex !== null && result.rows[selectedIndex] && (
        <RowDetailPanel
          row={result.rows[selectedIndex]}
          index={selectedIndex}
          total={result.rows.length}
          hideNulls={hideNulls}
          onClose={() => setSelectedIndex(null)}
          onNavigate={(delta) =>
            setSelectedIndex((i) =>
              Math.min(result.rows.length - 1, Math.max(0, i + delta))
            )
          }
        />
      )}
      </div>
    </div>
  );
}
