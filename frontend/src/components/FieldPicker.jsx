import React, { useEffect, useState, useMemo } from 'react';
import * as Popover from '@radix-ui/react-popover';

// Searchable field picker. Case-insensitive substring match; handles special
// chars like . _ - @ naturally because we use plain `includes` (not regex).
// Fields are shown sorted alphabetically (case-insensitive).
//
// Built on Radix Popover so anchoring, outside-click, focus return, and
// ESC handling are correct without per-component plumbing. The trigger
// button keeps the existing .input look so it slots into the QueryBuilder
// row without visual churn.
//
// `autoOpen` is set by callers (notably the QueryBuilder's add-filter
// form) when the component appears in response to an explicit user
// action and the natural next interaction is field selection. The
// popover opens on first mount so the operator can type to filter
// immediately, instead of having to click the trigger first.
export default function FieldPicker({ fields, value, onChange, placeholder = 'field...', width = '160px', autoOpen = false }) {
  const [open, setOpen] = useState(autoOpen);
  const [query, setQuery] = useState('');

  // Re-open on later autoOpen flips so the picker tracks "this form
  // just appeared" semantics, not just first mount.
  useEffect(() => {
    if (autoOpen) setOpen(true);
  }, [autoOpen]);

  const sorted = useMemo(
    () => [...(fields || [])].sort((a, b) => a.toLowerCase().localeCompare(b.toLowerCase())),
    [fields]
  );

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return sorted;
    return sorted.filter((f) => f.toLowerCase().includes(q));
  }, [sorted, query]);

  const pick = (f) => {
    onChange(f);
    setOpen(false);
    setQuery('');
  };

  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger asChild>
        <button
          type="button"
          className="input"
          style={{
            width,
            fontSize: '11px',
            textAlign: 'left',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
            cursor: 'pointer',
          }}
          title={value || placeholder}
        >
          {value || <span style={{ color: 'var(--text-muted)' }}>{placeholder}</span>}
        </button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content
          className="rdx-popover"
          align="start"
          sideOffset={4}
          style={{ minWidth: '260px', maxHeight: '320px' }}
        >
          <input
            className="input input-mono"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="filter fields..."
            autoFocus
            style={{ margin: '6px', fontSize: '11px' }}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && filtered.length > 0) pick(filtered[0]);
            }}
          />
          <div style={{ overflowY: 'auto', flex: 1 }}>
            {filtered.length === 0 ? (
              <div style={{ padding: '8px 12px', fontSize: '11px', color: 'var(--text-muted)' }}>
                No matches
              </div>
            ) : (
              filtered.map((f) => (
                <div
                  key={f}
                  onClick={() => pick(f)}
                  style={{
                    padding: '4px 12px',
                    fontSize: '11px',
                    cursor: 'pointer',
                    fontFamily: 'var(--font-mono)',
                    background: f === value ? 'var(--bg-tertiary)' : 'transparent',
                  }}
                  onMouseEnter={(e) => (e.currentTarget.style.background = 'var(--bg-tertiary)')}
                  onMouseLeave={(e) =>
                    (e.currentTarget.style.background = f === value ? 'var(--bg-tertiary)' : 'transparent')
                  }
                >
                  {f}
                </div>
              ))
            )}
          </div>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}
