import { useCallback, useEffect, useRef, useState } from 'react';

// Fixed row height in pixels, and the contract that makes windowing
// possible at all. It must match `.log-table td` in app.css: 4px padding
// top and bottom, ~13px line box, 1px border. If the two ever disagree
// the rows drift out of alignment with the scrollbar, so the CSS carries
// a comment pointing back here.
//
// A fixed height is affordable because row expansion opens a side panel
// rather than growing the row. That was the design decision that turned
// this from variable-height windowing — which needs per-row measurement,
// a resize observer and a cache — into arithmetic.
export const ROW_HEIGHT = 22;

// Rows rendered beyond the viewport on each side. Enough that a fast
// scroll or a keyboard PageDown does not expose blank space before the
// next render; small enough that the DOM stays tiny.
const OVERSCAN = 12;

/**
 * Windowing for a long, uniform list.
 *
 * Returns the slice of rows to render plus the two spacer heights that
 * stand in for everything above and below it. The caller renders those
 * as empty rows, which keeps the scrollbar honest and — importantly for
 * a table — keeps the browser's column-width algorithm working, because
 * the real rows are still inside the same <table>.
 *
 * @param {number} rowCount total rows in the result
 * @returns {{containerRef, startIndex, endIndex, padTop, padBottom, onScroll}}
 */
export function useVirtualRows(rowCount) {
  const containerRef = useRef(null);
  const [scrollTop, setScrollTop] = useState(0);
  const [viewportHeight, setViewportHeight] = useState(0);

  // Measure on mount and whenever the container resizes. Without the
  // observer, a window resize or the file panel collapsing would leave
  // the visible count stale and the list would render short.
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return undefined;
    const measure = () => setViewportHeight(el.clientHeight);
    measure();
    if (typeof ResizeObserver === 'undefined') return undefined;
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const onScroll = useCallback((e) => {
    setScrollTop(e.currentTarget.scrollTop);
  }, []);

  // Reset to the top when the result changes underneath us. Staying at a
  // stale offset after a new query is disorienting, and can leave the
  // view scrolled past the end of a shorter result.
  useEffect(() => {
    if (containerRef.current) containerRef.current.scrollTop = 0;
    setScrollTop(0);
  }, [rowCount]);

  // Before the first measurement, render a screenful rather than
  // nothing — otherwise the table flashes empty on mount.
  const effectiveHeight = viewportHeight || 600;

  const visibleCount = Math.ceil(effectiveHeight / ROW_HEIGHT);
  const startIndex = Math.max(0, Math.floor(scrollTop / ROW_HEIGHT) - OVERSCAN);
  const endIndex = Math.min(rowCount, startIndex + visibleCount + OVERSCAN * 2);

  return {
    containerRef,
    startIndex,
    endIndex,
    padTop: startIndex * ROW_HEIGHT,
    padBottom: Math.max(0, (rowCount - endIndex) * ROW_HEIGHT),
    onScroll,
  };
}
