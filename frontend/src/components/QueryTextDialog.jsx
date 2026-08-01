import React, { useEffect, useMemo, useState } from 'react';
import * as Dialog from '@radix-ui/react-dialog';
import { serializeQuery, parseQuery } from '../utils/queryDsl';

// QueryTextDialog — the LogQL-shaped text view of the current query.
//
// Opens with the current filter/time/search state serialized into the
// textarea. Copy ships it to the clipboard for chat/notes paste. Edit
// + Apply parses the textarea and pushes the result back to the parent
// via onApply({ filters, time_from, time_to, search }) — App.jsx then
// updates its state and the query re-runs through the usual TanStack
// Query path.
//
// Replaces the previous Save / Load Saved Query buttons. There's no
// server-side persistence: the text IS the portable artefact.
export default function QueryTextDialog({ open, onOpenChange, current, onApply }) {
  const initial = useMemo(() => serializeQuery(current), [current]);
  const [text, setText] = useState(initial);
  const [error, setError] = useState(null);
  const [copied, setCopied] = useState(false);

  // Re-seed the textarea whenever the dialog opens or the current state
  // changes underneath us (e.g. user added a pill, closed, reopened).
  useEffect(() => {
    if (open) {
      setText(initial);
      setError(null);
      setCopied(false);
    }
  }, [open, initial]);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch (e) {
      setError(`clipboard: ${e.message}`);
    }
  };

  const handleApply = () => {
    const parsed = parseQuery(text);
    if (parsed.error) {
      setError(parsed.error);
      return;
    }
    // Parser auto-corrections (e.g. ".X." → ".*X.*") are surfaced as
    // warnings. We pass them up so App.jsx can show a transient toast,
    // and we close the dialog so the operator sees the corrected pills.
    onApply({
      filters: parsed.filters,
      time_from: parsed.time_from,
      time_to: parsed.time_to,
      search: parsed.search,
      warnings: parsed.warnings || [],
    });
    onOpenChange(false);
  };

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="rdx-overlay" />
        <Dialog.Content className="rdx-content" style={{ width: '720px' }}>
          <div className="modal-header">
            <Dialog.Title asChild><h3>Query as text (LogQL-shaped)</h3></Dialog.Title>
            <Dialog.Close asChild>
              <button className="btn btn-sm" aria-label="Close">&times;</button>
            </Dialog.Close>
          </div>
          <Dialog.Description className="sr-only">
            Inspect, copy, or paste-and-edit the current query as LogQL-style text.
          </Dialog.Description>

          <div style={{ padding: '12px 16px', display: 'flex', flexDirection: 'column', gap: '10px' }}>
            <div style={{ fontSize: '11px', color: 'var(--text-secondary)' }}>
              Edit and Apply to update the visual builder. Copy to share or save in notes.
              When you migrate to Grafana/Loki, replace <code>{'{}'}</code> with your stream
              selector and pass the <code>@time:</code> range as API params.
            </div>
            <textarea
              value={text}
              onChange={(e) => { setText(e.target.value); setError(null); }}
              spellCheck={false}
              style={{
                width: '100%',
                minHeight: '280px',
                fontFamily: 'var(--font-mono)',
                fontSize: '12px',
                padding: '8px',
                background: 'var(--bg-tertiary)',
                color: 'var(--text-primary)',
                border: '1px solid var(--border)',
                borderRadius: 'var(--radius)',
                resize: 'vertical',
              }}
            />
            {error && (
              <div className="obs-error">{error}</div>
            )}
          </div>

          <div className="modal-footer">
            <button className="btn btn-sm" onClick={handleCopy}>
              {copied ? 'Copied!' : 'Copy'}
            </button>
            <div style={{ flex: 1 }} />
            <button className="btn" onClick={() => onOpenChange(false)}>Close</button>
            <button className="btn btn-primary" onClick={handleApply}>Apply</button>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
