import React, { useEffect, useState } from 'react';
import FieldPicker from './FieldPicker';
import QueryTextDialog from './QueryTextDialog';
import MultiValueInput from './MultiValueInput';
import { parseQuery } from '../utils/queryDsl';

// useFieldSamples — fetch distinct sample values for a single field via
// /api/field-samples. Result feeds the native <datalist> rendered next
// to the value input so the operator sees the actual values DuckDB has
// in that column instead of guessing.
//
// Returns [] when:
//   - field is empty (no fetch)
//   - the field is too high-cardinality (backend returns an empty array
//     as a signal to skip)
//   - the request fails (silent — datalist just shows nothing)
//
// Re-runs only when the field changes; no debounce because field
// changes are discrete (clicked from FieldPicker), not keystroke-y.
function useFieldSamples(field) {
  const [vals, setVals] = useState([]);
  useEffect(() => {
    if (!field) { setVals([]); return; }
    let cancelled = false;
    fetch(`/api/field-samples?fields=${encodeURIComponent(field)}`)
      .then((r) => (r.ok ? r.json() : null))
      .then((data) => {
        if (cancelled || !data) return;
        const arr = data[field];
        setVals(Array.isArray(arr) ? arr : []);
      })
      .catch(() => { if (!cancelled) setVals([]); });
    return () => { cancelled = true; };
  }, [field]);
  return vals;
}

const OPERATORS = [
  { value: 'is', label: 'is' },
  { value: 'is_not', label: 'is not' },
  { value: 'is_one_of', label: 'is one of' },
  { value: 'is_not_one_of', label: 'is not one of' },
  { value: 'contains', label: 'contains' },
  { value: 'not_contains', label: 'not contains' },
  { value: 'wildcard', label: 'wildcard' },
  { value: 'not_wildcard', label: 'not wildcard' },
  { value: 'exists', label: 'exists' },
  { value: 'does_not_exist', label: 'does not exist' },
];

// Operators that take no value (OpenSearch parity for "exists" /
// "does not exist"). The value input is hidden and the engine
// builds the SQL clause from the field alone.
const VALUELESS = new Set(['exists', 'does_not_exist']);

// Operators that take a comma-separated value list. The value input
// is the same single field but the placeholder + datalist behaviour
// hint at multi-value entry.
const MULTIVALUE = new Set(['is_one_of', 'is_not_one_of']);

export default function QueryBuilder({
  fields,
  filters,
  onFiltersChange,
  searchText,
  onSearchTextChange,
  timeFrom,
  timeTo,
  onTimeChange,
  onApply,
  onNotice,
}) {
  const [showAddFilter, setShowAddFilter] = useState(false);
  const [newField, setNewField] = useState('');
  const [newOperator, setNewOperator] = useState('contains');
  const [newValue, setNewValue] = useState('');
  const [newLogic, setNewLogic] = useState('and');
  const [editIndex, setEditIndex] = useState(null);
  const [editField, setEditField] = useState('');
  const [editOperator, setEditOperator] = useState('contains');
  const [editValue, setEditValue] = useState('');
  const [editLogic, setEditLogic] = useState('and');
  const [showText, setShowText] = useState(false);

  // Value-input suggestions (datalist) per active picker — only fetched
  // when a field is chosen. Edit and add forms have separate state so
  // they don't clobber each other when both are open.
  const addValueSuggestions = useFieldSamples(showAddFilter ? newField : '');
  const editValueSuggestions = useFieldSamples(editIndex !== null ? editField : '');

  const addFilter = () => {
    const valueless = VALUELESS.has(newOperator);
    if (!newField) return;
    if (!valueless && !newValue) return;
    const filter = {
      field: newField,
      operator: newOperator,
      value: valueless ? '' : newValue,
      logic: newLogic,
    };
    onFiltersChange([...filters, filter]);
    setNewValue('');
    setShowAddFilter(false);
  };

  const removeFilter = (index) => {
    onFiltersChange(filters.filter((_, i) => i !== index));
  };

  const startEdit = (index) => {
    const f = filters[index];
    setEditIndex(index);
    setEditField(f.field);
    setEditOperator(f.operator);
    setEditValue(f.value);
    setEditLogic(f.logic);
  };

  const applyEdit = () => {
    if (editIndex === null || !editField) return;
    const valueless = VALUELESS.has(editOperator);
    if (!valueless && !editValue) return;
    const updated = filters.map((f, i) =>
      i === editIndex
        ? {
            field: editField,
            operator: editOperator,
            value: valueless ? '' : editValue,
            logic: editLogic,
          }
        : f
    );
    onFiltersChange(updated);
    setEditIndex(null);
  };

  const cancelEdit = () => {
    setEditIndex(null);
  };

  const handleEditKeyDown = (e) => {
    if (e.key === 'Enter') { e.preventDefault(); applyEdit(); }
    if (e.key === 'Escape') cancelEdit();
  };

  const handleKeyDown = (e) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      addFilter();
    }
    if (e.key === 'Escape') {
      setShowAddFilter(false);
    }
  };

  const operatorLabel = (op) => OPERATORS.find((o) => o.value === op)?.label || op;

  // Applied by QueryTextDialog. Parsed DSL → live state, then trigger
  // an Apply so the table re-queries against the new criteria.
  const handleTextApply = (parsed) => {
    onFiltersChange(parsed.filters);
    onSearchTextChange(parsed.search);
    if (onTimeChange) onTimeChange(parsed.time_from, parsed.time_to);
    // Parser auto-corrections (single-dot regex → contains regex, etc.)
    // surface as a transient notice so the operator knows their pasted
    // text was rewritten on the way in.
    if (onNotice && parsed.warnings && parsed.warnings.length > 0) {
      onNotice(parsed.warnings.join(' · '));
    }
    if (onApply) {
      // Defer so the state updates have a tick to land before the
      // applied-snapshot in App.jsx is captured.
      setTimeout(() => onApply(), 0);
    }
  };

  return (
    <div className="query-builder">
      {/* Existing filter pills */}
      {filters.map((f, i) => (
        <React.Fragment key={i}>
          {i > 0 && editIndex !== i && (
            // The connector word between two pills is a one-click toggle:
            // AND ↔ OR. Goes through the same onFiltersChange path so the
            // applied query refreshes (via the auto-apply effect when
            // enabled, or on next Apply click).
            <button
              type="button"
              onClick={() => {
                const updated = filters.map((x, j) => (
                  j === i ? { ...x, logic: x.logic === 'or' ? 'and' : 'or' } : x
                ));
                onFiltersChange(updated);
              }}
              title="Click to toggle AND ↔ OR"
              style={{
                fontSize: '10px',
                color: f.logic === 'or' ? 'var(--accent)' : 'var(--text-muted)',
                textTransform: 'uppercase',
                background: 'transparent',
                border: 'none',
                padding: '0 4px',
                cursor: 'pointer',
                fontWeight: f.logic === 'or' ? 600 : 400,
              }}
            >
              {f.logic === 'or' ? 'or' : 'and'}
            </button>
          )}
          {editIndex === i ? (
            <div style={{ display: 'flex', gap: '4px', alignItems: 'center' }}>
              {i > 0 && (
                <select className="input" value={editLogic} onChange={(e) => setEditLogic(e.target.value)}
                  style={{ width: '60px', fontSize: '11px' }}>
                  <option value="and">AND</option>
                  <option value="or">OR</option>
                </select>
              )}
              <FieldPicker fields={fields} value={editField} onChange={setEditField} width="160px" />
              <select className="input" value={editOperator} onChange={(e) => setEditOperator(e.target.value)}
                style={{ fontSize: '11px' }}>
                {OPERATORS.map((op) => (<option key={op.value} value={op.value}>{op.label}</option>))}
              </select>
              {!VALUELESS.has(editOperator) && (
                MULTIVALUE.has(editOperator) ? (
                  <MultiValueInput
                    value={editValue}
                    onChange={setEditValue}
                    suggestions={editValueSuggestions}
                    listIdBase={`edit-${editField || 'none'}`}
                    autoFocus
                    onCommit={applyEdit}
                  />
                ) : (
                  <>
                    <input className="input input-mono" value={editValue} onChange={(e) => setEditValue(e.target.value)}
                      onKeyDown={handleEditKeyDown} placeholder="value"
                      list={editField ? `vals-edit-${editField}` : undefined}
                      style={{ width: '160px', fontSize: '11px' }} autoFocus />
                    {editField && (
                      <datalist id={`vals-edit-${editField}`}>
                        {(editValueSuggestions || []).map((v) => (
                          <option key={v} value={v} />
                        ))}
                      </datalist>
                    )}
                  </>
                )
              )}
              <button className="btn btn-sm btn-primary" onClick={applyEdit}>OK</button>
              <button className="btn btn-sm" onClick={cancelEdit}>&times;</button>
            </div>
          ) : (
            <div className="filter-pill" onClick={() => startEdit(i)} style={{ cursor: 'pointer' }} title="Click to edit">
              <span style={{ color: 'var(--accent)' }}>{f.field}</span>
              <span style={{ color: 'var(--text-muted)', fontSize: '10px' }}>{operatorLabel(f.operator)}</span>
              {!VALUELESS.has(f.operator) && (
                <span style={{ color: 'var(--warning)' }}>"{f.value}"</span>
              )}
              <span className="remove" onClick={(e) => { e.stopPropagation(); removeFilter(i); }}>&times;</span>
            </div>
          )}
        </React.Fragment>
      ))}

      {/* Add filter inline form */}
      {showAddFilter ? (
        <div style={{ display: 'flex', gap: '4px', alignItems: 'center' }}>
          {filters.length > 0 && (
            <select
              className="input"
              value={newLogic}
              onChange={(e) => setNewLogic(e.target.value)}
              style={{ width: '60px', fontSize: '11px' }}
            >
              <option value="and">AND</option>
              <option value="or">OR</option>
            </select>
          )}
          <FieldPicker fields={fields} value={newField} onChange={setNewField} width="160px" autoOpen />
          <select
            className="input"
            value={newOperator}
            onChange={(e) => setNewOperator(e.target.value)}
            style={{ fontSize: '11px' }}
          >
            {OPERATORS.map((op) => (
              <option key={op.value} value={op.value}>{op.label}</option>
            ))}
          </select>
          {!VALUELESS.has(newOperator) && (
            MULTIVALUE.has(newOperator) ? (
              <MultiValueInput
                value={newValue}
                onChange={setNewValue}
                suggestions={addValueSuggestions}
                listIdBase={`add-${newField || 'none'}`}
                autoFocus
                onCommit={addFilter}
              />
            ) : (
              <>
                <input
                  className="input input-mono"
                  value={newValue}
                  onChange={(e) => setNewValue(e.target.value)}
                  onKeyDown={handleKeyDown}
                  placeholder="value (* = wildcard)"
                  list={newField ? `vals-add-${newField}` : undefined}
                  style={{ width: '160px', fontSize: '11px' }}
                />
                {newField && (
                  <datalist id={`vals-add-${newField}`}>
                    {(addValueSuggestions || []).map((v) => (
                      <option key={v} value={v} />
                    ))}
                  </datalist>
                )}
              </>
            )
          )}
          <button className="btn btn-sm btn-primary" onClick={addFilter}>Add</button>
          <button className="btn btn-sm" onClick={() => setShowAddFilter(false)}>&times;</button>
        </div>
      ) : (
        <button className="btn btn-sm" onClick={() => setShowAddFilter(true)}>
          + Filter
        </button>
      )}

      {/* Separator */}
      <div style={{ width: '1px', height: '20px', background: 'var(--border)' }} />

      {/* Free-text search */}
      <input
        className="input input-mono"
        value={searchText}
        onChange={(e) => onSearchTextChange(e.target.value)}
        onKeyDown={(e) => { if (e.key === 'Enter' && onApply) onApply(); }}
        placeholder="Search all fields..."
        style={{ width: '200px', fontSize: '11px' }}
      />

      {/* Apply filter — always active; re-runs the query even when auto-filter is on.
          Covers filter pills, time range, and "Search all fields" text. */}
      <button
        className="btn btn-sm btn-primary"
        onClick={() => onApply && onApply()}
        title="Apply filter (re-run query)"
      >
        Apply
      </button>

      {/* Clear all */}
      {(filters.length > 0 || searchText) && (
        <button
          className="btn btn-sm"
          onClick={() => {
            onFiltersChange([]);
            onSearchTextChange('');
          }}
        >
          Clear all
        </button>
      )}

      {/* Separator before the text-view toggle */}
      <div style={{ width: '1px', height: '20px', background: 'var(--border)' }} />

      {/* LogQL-shaped text view. Replaces the previous Save/Load buttons —
          the text is the portable artefact (copy for chat/notes, paste back
          to restore). See utils/queryDsl.js for the format. */}
      <button
        className="btn btn-sm"
        onClick={() => setShowText(true)}
        title="View / edit query as LogQL-shaped text"
      >
        Text
      </button>

      <QueryTextDialog
        open={showText}
        onOpenChange={setShowText}
        current={{
          filters,
          time_from: timeFrom || '',
          time_to: timeTo || '',
          search: searchText || '',
        }}
        onApply={handleTextApply}
      />
    </div>
  );
}
