import React, { useState, useRef, useEffect } from 'react';

// MultiValueInput — chip-based value entry for is_one_of / is_not_one_of.
//
// UX (matches OpenSearch's filter UI):
//   - Click into the input → datalist suggestions appear (browser-native)
//   - Pick a suggestion → adds as chip, narrows remaining suggestions
//   - Type free-text → Enter / Tab / "," commits as chip
//   - Backspace on empty input → removes the last chip
//   - Each chip has an X to remove individually
//
// `value` is a comma-separated string (matches the engine's expected
// shape — see Engine.buildFilterCondition is_one_of branch). Internal
// state derives an array from `value`; onChange emits the joined string.
// Round-trips through queryDsl.js as the same anchored regex
// alternation as the original textarea-based entry.
export default function MultiValueInput({
  value,
  onChange,
  suggestions = [],
  placeholder = 'val1, val2, val3',
  listIdBase = 'mv',
  autoFocus = false,
  onCommit,
}) {
  // Source of truth: the `value` prop string. Internal `chips` is the
  // parsed array. Re-derives whenever value changes externally (text
  // dialog paste, etc.).
  const [draft, setDraft] = useState('');
  const inputRef = useRef(null);

  const chips = parseChips(value);

  useEffect(() => {
    if (autoFocus && inputRef.current) inputRef.current.focus();
  }, [autoFocus]);

  // Datalist options = suggestions MINUS what's already chipped (so
  // the dropdown shrinks as the operator picks).
  const remainingSuggestions = (suggestions || []).filter(
    (s) => !chips.some((c) => c.toLowerCase() === String(s).toLowerCase())
  );
  const listId = `${listIdBase}-mv`;

  const commitDraft = () => {
    const v = draft.trim();
    if (!v) return;
    if (chips.some((c) => c.toLowerCase() === v.toLowerCase())) {
      setDraft('');
      return; // already added, ignore
    }
    onChange([...chips, v].join(', '));
    setDraft('');
  };

  const removeChip = (i) => {
    const next = chips.filter((_, j) => j !== i);
    onChange(next.join(', '));
    // Return focus to the input so keyboard flow continues.
    setTimeout(() => inputRef.current?.focus(), 0);
  };

  const handleKeyDown = (e) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      if (draft.trim()) {
        // Commit the in-progress value as another chip.
        commitDraft();
      }
      // Enter on empty input deliberately does NOTHING — the caller
      // should provide an explicit "Add" button. Calling onCommit
      // here would race with auto-commit-on-suggestion-pick (the
      // browser's datalist click event clears draft + adds a chip
      // asynchronously, so an Enter pressed milliseconds later sees
      // an empty draft and would otherwise prematurely close the form).
    } else if (e.key === 'Tab' || e.key === ',') {
      if (draft.trim()) {
        e.preventDefault();
        commitDraft();
      }
    } else if (e.key === 'Backspace' && draft === '' && chips.length > 0) {
      e.preventDefault();
      removeChip(chips.length - 1);
    }
  };

  // The native datalist fires onChange when the operator picks a
  // suggestion; we detect that by looking for a value that's an exact
  // match for a suggestion AND was set without any keystroke in
  // between. Simpler approach: any change that ends with a value
  // matching one of the suggestions counts as a pick → auto-commit.
  const handleInputChange = (e) => {
    const v = e.target.value;
    setDraft(v);
    // Suggestion pick: when browser fills in the value AND it's a
    // verbatim match for one of the remaining suggestions, commit
    // immediately so the operator doesn't have to also press Enter.
    if (remainingSuggestions.some((s) => String(s) === v)) {
      // Defer commit so React processes the input change first.
      setTimeout(() => {
        onChange([...chips, v].join(', '));
        setDraft('');
        inputRef.current?.focus();
      }, 0);
    }
  };

  return (
    <div
      style={{
        display: 'inline-flex',
        flexWrap: 'wrap',
        alignItems: 'center',
        gap: '4px',
        padding: '2px 4px',
        minHeight: '22px',
        minWidth: '240px',
        background: 'var(--bg-tertiary)',
        border: '1px solid var(--border)',
        borderRadius: 'var(--radius)',
        cursor: 'text',
      }}
      onClick={() => inputRef.current?.focus()}
    >
      {chips.map((c, i) => (
        <span
          key={`${c}-${i}`}
          data-testid="multivalue-chip"
          style={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: '2px',
            padding: '0 4px',
            background: 'var(--bg-secondary)',
            border: '1px solid var(--border)',
            borderRadius: 'var(--radius)',
            fontSize: '11px',
            fontFamily: 'var(--font-mono)',
            color: 'var(--warning)',
          }}
        >
          {/* Chip label gets its own span so its accessible text is
              just the value — `getByText(value, { exact: true })` in
              tests doesn't pick up the × button alongside. */}
          <span data-testid="multivalue-chip-label">{c}</span>
          <button
            type="button"
            onClick={(e) => { e.stopPropagation(); removeChip(i); }}
            title="Remove"
            aria-label={`Remove ${c}`}
            style={{
              border: 'none',
              background: 'transparent',
              color: 'var(--text-muted)',
              cursor: 'pointer',
              fontSize: '12px',
              padding: '0 2px',
              lineHeight: 1,
            }}
          >
            &times;
          </button>
        </span>
      ))}
      <input
        ref={inputRef}
        className="input-mono"
        data-testid="multivalue-input"
        value={draft}
        onChange={handleInputChange}
        onKeyDown={handleKeyDown}
        list={listId}
        placeholder={chips.length === 0 ? placeholder : ''}
        style={{
          flex: 1,
          minWidth: '80px',
          fontSize: '11px',
          border: 'none',
          background: 'transparent',
          outline: 'none',
          color: 'var(--text-primary)',
          padding: '0 2px',
        }}
      />
      <datalist id={listId}>
        {remainingSuggestions.map((s) => (
          <option key={s} value={s} />
        ))}
      </datalist>
    </div>
  );
}

// parseChips — split a comma-separated string into trimmed,
// non-empty values. Exposed so QueryBuilder can use the same
// canonicalisation when comparing.
function parseChips(value) {
  if (!value) return [];
  return String(value)
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean);
}
