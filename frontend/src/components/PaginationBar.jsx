import React, { useState, useEffect } from 'react';

// Pagination controls for the log table. Shows Prev/Next, the current
// row window, "Page X of Y", and an input for jumping to a specific
// page number. The jump input is editable while focused so typing
// doesn't fight the page-state echo, and commits on Enter or blur.
//
// Out-of-range page numbers snap into [1, totalPages] silently — the
// user gets a valid page rather than an error toast.
export default function PaginationBar({ offset, limit, total, loading, onJump, onLimitChange }) {
  const totalPages = Math.max(1, Math.ceil(total / limit));
  const currentPage = Math.min(totalPages, Math.floor(offset / limit) + 1);

  // Local edit state so typing doesn't get clobbered by the controlled
  // currentPage echo. Sync to currentPage when not focused.
  const [pageInput, setPageInput] = useState(String(currentPage));
  const [focused, setFocused] = useState(false);
  useEffect(() => {
    if (!focused) setPageInput(String(currentPage));
  }, [currentPage, focused]);

  const commitPage = () => {
    const n = parseInt(pageInput, 10);
    if (Number.isNaN(n)) {
      setPageInput(String(currentPage));
      return;
    }
    const clamped = Math.min(totalPages, Math.max(1, n));
    setPageInput(String(clamped));
    if (clamped !== currentPage) {
      onJump((clamped - 1) * limit);
    }
  };

  return (
    <div className="pagination">
      <button
        className="btn btn-sm"
        disabled={offset === 0}
        onClick={() => onJump(Math.max(0, offset - limit))}
      >
        Prev
      </button>
      <span className="page-info">
        {offset + 1}-{Math.min(offset + limit, total)} of {total.toLocaleString()}
      </span>
      <button
        className="btn btn-sm"
        disabled={offset + limit >= total}
        onClick={() => onJump(offset + limit)}
      >
        Next
      </button>

      {/* Page X of Y, with jump input */}
      <span style={{ marginLeft: '8px', fontSize: '12px', color: 'var(--text-secondary)' }}>
        Page
      </span>
      <input
        className="input input-mono"
        type="text"
        inputMode="numeric"
        value={pageInput}
        onChange={(e) => setPageInput(e.target.value.replace(/[^0-9]/g, ''))}
        onFocus={() => setFocused(true)}
        onBlur={() => { setFocused(false); commitPage(); }}
        onKeyDown={(e) => {
          if (e.key === 'Enter') { e.currentTarget.blur(); }
        }}
        style={{ width: '4em', textAlign: 'right', padding: '2px 4px', fontSize: '12px' }}
        title="Type a page number and press Enter to jump"
      />
      <span style={{ fontSize: '12px', color: 'var(--text-secondary)' }}>
        of {totalPages.toLocaleString()}
      </span>

      <span style={{ marginLeft: '8px', fontSize: '12px', color: 'var(--text-secondary)' }}>
        Rows:
      </span>
      <select
        className="btn btn-sm"
        value={limit}
        onChange={(e) => onLimitChange(Number(e.target.value))}
        style={{ fontSize: '12px', padding: '2px 4px' }}
      >
        <option value={200}>200</option>
        <option value={500}>500</option>
        <option value={1000}>1,000</option>
        <option value={2000}>2,000</option>
        <option value={5000}>5,000</option>
        <option value={10000}>10,000</option>
      </select>
      {loading && <span className="spinner-bold" />}
    </div>
  );
}
