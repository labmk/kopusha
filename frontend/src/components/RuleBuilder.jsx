import React, { useState, useEffect, useCallback, useRef } from 'react';
import * as Dialog from '@radix-ui/react-dialog';
import { api } from '../api/client';

// RuleBuilder — write a parsers.d rule by pasting lines instead of
// writing a regex.
//
// The rule system is the part of this project that has no substitute,
// and until now using it meant hand-writing a Go regex with named
// capture groups into a YAML file, from documentation, in a text
// editor, then restarting. That gated the most valuable capability
// behind the exact skill the target user does not have — anyone
// comfortable writing regex against log lines was never blocked in the
// first place.
//
// The whole design rests on the preview being trustworthy. Every
// keystroke round-trips to the backend and runs through the real line
// adapter, so what the table shows is what a load will produce. A
// preview computed here in JavaScript would be faster and would
// eventually disagree with the parser, which is the one thing a preview
// must never do.
const PREVIEW_DEBOUNCE_MS = 250;

export default function RuleBuilder({ initialSample = '', onClose, onSaved }) {
  const [sample, setSample] = useState(initialSample);
  const [rule, setRule] = useState(null);
  const [preview, setPreview] = useState(null);
  const [analysing, setAnalysing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState(null);
  const [saved, setSaved] = useState(null);
  const [existing, setExisting] = useState([]);
  const [showAdvanced, setShowAdvanced] = useState(false);

  useEffect(() => {
    api.getRules().then((d) => setExisting(d.rules || [])).catch(() => {});
  }, []);

  const analyse = useCallback(async (text) => {
    if (!text.trim()) return;
    setAnalysing(true);
    setError(null);
    try {
      const draft = await api.suggestRule(text);
      // Keep whatever name the user has already typed: re-analysing
      // after pasting more lines should not discard it.
      setRule((prev) => ({ ...draft, name: prev?.name || draft.name || '' }));
    } catch (e) {
      setError(e.message);
    } finally {
      setAnalysing(false);
    }
  }, []);

  // Analyse the seeded sample once on open, so arriving from a failed
  // load lands on a proposed rule rather than an empty form.
  const seeded = useRef(false);
  useEffect(() => {
    if (seeded.current || !initialSample.trim()) return;
    seeded.current = true;
    analyse(initialSample);
  }, [initialSample, analyse]);

  // Live preview, debounced. Runs on every edit to the rule or the
  // sample — the point is that a wrong guess is visible immediately
  // rather than after a save and a reload.
  useEffect(() => {
    if (!rule || !rule.parse) { setPreview(null); return; }
    const t = setTimeout(() => {
      api.previewRule(rule, sample)
        .then(setPreview)
        .catch((e) => setPreview({ error: e.message }));
    }, PREVIEW_DEBOUNCE_MS);
    return () => clearTimeout(t);
  }, [rule, sample]);

  const patch = (fields) => setRule((r) => ({ ...(r || {}), ...fields }));

  // Renaming a field is a rename of its capture group, which lives in
  // the regex text. Rewriting the regex rather than tracking names
  // separately keeps one source of truth: the regex the user can see
  // and edit is the regex that runs.
  const renameField = (from, to) => {
    const clean = to.replace(/[^A-Za-z0-9_]/g, '');
    if (!clean || clean === from) return;
    setRule((r) => ({
      ...r,
      parse: r.parse.replace(`(?P<${from}>`, `(?P<${clean}>`),
    }));
  };

  const captureNames = (() => {
    if (!rule?.parse) return [];
    const out = [];
    const re = /\(\?P<([A-Za-z0-9_]+)>/g;
    let m;
    while ((m = re.exec(rule.parse)) !== null) out.push(m[1]);
    return out;
  })();

  const nameTaken = rule?.name
    && existing.some((r) => r.name === rule.name.trim().toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, ''));

  const save = async (overwrite = false) => {
    setSaving(true);
    setError(null);
    try {
      const res = await api.saveRule({ ...rule, sample }, overwrite);
      setSaved(res);
      if (onSaved) onSaved(res);
    } catch (e) {
      if (e.conflict) {
        if (window.confirm(`A rule named "${rule.name}" already exists. Replace it?`)) {
          setSaving(false);
          return save(true);
        }
      } else {
        setError(e.message);
      }
    } finally {
      setSaving(false);
    }
  };

  const canSave = rule?.parse && rule?.ts_layout && rule?.name?.trim() && !preview?.error;

  return (
    <Dialog.Root open onOpenChange={(o) => { if (!o) onClose(); }}>
      <Dialog.Portal>
        <Dialog.Overlay className="rdx-overlay" />
        <Dialog.Content className="rdx-content rule-builder" style={{ width: 'min(1000px, 94vw)' }}>
          <div className="modal-header">
            <Dialog.Title asChild><h3>Build a parser rule</h3></Dialog.Title>
            <Dialog.Close asChild>
              <button className="btn btn-sm" aria-label="Close">&times;</button>
            </Dialog.Close>
          </div>
          <Dialog.Description className="sr-only">
            Paste sample log lines, review the proposed pattern and the rows it
            produces, then save the rule to parsers.d.
          </Dialog.Description>

          {saved ? (
            <div className="rule-saved">
              <div className="rule-saved-title">Rule saved</div>
              <div className="rule-saved-body">
                <div><code>{saved.file}</code></div>
                <p>
                  It is live now — {saved.rules} line rule{saved.rules === 1 ? '' : 's'} loaded.
                  Load the file again and it will be parsed with this rule.
                  Files already loaded keep the shape they were loaded with.
                </p>
              </div>
              <div className="modal-footer">
                <div />
                <button className="btn btn-primary" onClick={onClose}>Done</button>
              </div>
            </div>
          ) : (
            <>
              <div className="rule-builder-body">
                {/* Step 1 — the sample */}
                <section className="rule-step">
                  <div className="rule-step-head">
                    <span className="rule-step-n">1</span>
                    <span>Paste a few representative lines</span>
                    <button
                      className="btn btn-sm"
                      onClick={() => analyse(sample)}
                      disabled={!sample.trim() || analysing}
                      style={{ marginLeft: 'auto' }}
                    >
                      {analysing ? 'Analysing…' : rule ? 'Analyse again' : 'Analyse'}
                    </button>
                  </div>
                  <textarea
                    className="input input-mono rule-sample"
                    rows={5}
                    value={sample}
                    onChange={(e) => setSample(e.target.value)}
                    placeholder={'2026-03-18T06:00:00 gateway[4179]: queue depth 2347\n2026-03-18T06:00:04 gateway[4179]: queue depth 2412'}
                    spellCheck={false}
                  />
                  <div className="rule-hint">
                    Three or four lines that differ from each other work better than
                    twenty that are alike — what varies between them is what becomes
                    a field.
                  </div>
                </section>

                {rule && (
                  <>
                    {/* Step 2 — the pattern */}
                    <section className="rule-step">
                      <div className="rule-step-head">
                        <span className="rule-step-n">2</span>
                        <span>Check the pattern</span>
                      </div>

                      {(rule.warnings || []).map((wtext, i) => (
                        <div className="rule-warning" key={i}>{wtext}</div>
                      ))}

                      <label className="rule-field">
                        <span>Pattern</span>
                        <textarea
                          className="input input-mono"
                          rows={3}
                          value={rule.parse || ''}
                          onChange={(e) => patch({ parse: e.target.value })}
                          spellCheck={false}
                        />
                      </label>

                      <label className="rule-field">
                        <span>
                          Timestamp format
                          <span
                            className="rule-help"
                            title="A Go layout: the reference time 2006-01-02 15:04:05 written in the shape your timestamps use. Not %Y-%m-%d."
                          >?</span>
                        </span>
                        <input
                          className="input input-mono"
                          value={rule.ts_layout || ''}
                          onChange={(e) => patch({ ts_layout: e.target.value })}
                          spellCheck={false}
                        />
                      </label>

                      {captureNames.filter((n) => n !== 'ts' && n !== 'message').length > 0 && (
                        <div className="rule-fields">
                          <div className="rule-fields-label">Field names</div>
                          <div className="rule-fields-list">
                            {captureNames
                              .filter((n) => n !== 'ts' && n !== 'message')
                              .map((n) => (
                                <input
                                  key={n}
                                  className="input input-mono rule-field-name"
                                  defaultValue={n}
                                  onBlur={(e) => renameField(n, e.target.value)}
                                  onKeyDown={(e) => { if (e.key === 'Enter') e.target.blur(); }}
                                  spellCheck={false}
                                  title={`Rename the ${n} column`}
                                />
                              ))}
                          </div>
                        </div>
                      )}

                      <button
                        className="btn btn-sm rule-advanced-toggle"
                        onClick={() => setShowAdvanced((v) => !v)}
                      >
                        {showAdvanced ? '▾' : '▸'} Advanced
                      </button>
                      {showAdvanced && (
                        <div className="rule-advanced">
                          <label className="rule-field rule-field-inline">
                            <span>Priority</span>
                            <input
                              className="input"
                              type="number"
                              value={rule.priority ?? 50}
                              onChange={(e) => patch({ priority: Number(e.target.value) })}
                            />
                            <span className="rule-hint">
                              Higher wins when several rules match one file. Shipped
                              rules use 80–100.
                            </span>
                          </label>
                          <label className="rule-field rule-field-inline">
                            <span>Assume timezone</span>
                            <input
                              className="input input-mono"
                              value={rule.ts_assume_tz || ''}
                              placeholder="UTC"
                              onChange={(e) => patch({ ts_assume_tz: e.target.value })}
                            />
                            <span className="rule-hint">
                              For timestamps that carry no zone. Blank means UTC.
                            </span>
                          </label>
                          <label className="rule-check">
                            <input
                              type="checkbox"
                              checked={!!rule.ts_use_mtime_date}
                              onChange={(e) => patch({
                                ts_use_mtime_date: e.target.checked,
                                ts_use_mtime_year: e.target.checked ? false : rule.ts_use_mtime_year,
                              })}
                            />
                            Take the date from the file (timestamps carry only a time of day)
                          </label>
                          <label className="rule-check">
                            <input
                              type="checkbox"
                              checked={!!rule.ts_use_mtime_year}
                              onChange={(e) => patch({
                                ts_use_mtime_year: e.target.checked,
                                ts_use_mtime_date: e.target.checked ? false : rule.ts_use_mtime_date,
                              })}
                            />
                            Take the year from the file (timestamps carry month and day only)
                          </label>
                          {(rule.ts_regex_subs || []).length > 0 && (
                            <div className="rule-hint">
                              This format needs {rule.ts_regex_subs.length} timestamp
                              rewrite{rule.ts_regex_subs.length === 1 ? '' : 's'} before it can be
                              parsed; they are saved with the rule.
                            </div>
                          )}
                        </div>
                      )}
                    </section>

                    {/* Step 3 — the proof */}
                    <section className="rule-step">
                      <div className="rule-step-head">
                        <span className="rule-step-n">3</span>
                        <span>What it produces</span>
                        {preview && !preview.error && (
                          <span className="rule-tally">
                            {preview.parsed} parsed
                            {preview.continuation > 0 && ` · ${preview.continuation} folded into the line above`}
                            {preview.timestamp_errors > 0 && (
                              <span className="rule-tally-bad">
                                {' '}· {preview.timestamp_errors} without a timestamp
                              </span>
                            )}
                          </span>
                        )}
                      </div>

                      {preview?.error && <div className="rule-error">{preview.error}</div>}

                      {preview && !preview.error && preview.timestamp_errors > 0 && (
                        <div className="rule-warning">
                          The pattern matches, but the timestamp format does not fit what
                          it captured, so these rows would load with no time on them and
                          disappear from every time filter.
                          <div className="rule-ts-errors">
                            {preview.lines.filter((l) => l.ts_error).slice(0, 3).map((l, i) => (
                              <div key={i}>{l.ts_error}</div>
                            ))}
                          </div>
                        </div>
                      )}

                      {preview && !preview.error && preview.rows?.length > 0 && (
                        <div className="rule-preview-table-wrap">
                          <table className="rule-preview-table">
                            <thead>
                              <tr>{preview.fields.map((f) => <th key={f}>{f}</th>)}</tr>
                            </thead>
                            <tbody>
                              {preview.rows.slice(0, 12).map((row, i) => (
                                <tr key={i}>
                                  {preview.fields.map((f) => (
                                    <td key={f} title={String(row[f] ?? '')}>
                                      {String(row[f] ?? '')}
                                    </td>
                                  ))}
                                </tr>
                              ))}
                            </tbody>
                          </table>
                        </div>
                      )}

                      {preview && !preview.error && preview.continuation > 0 && (
                        <details className="rule-unmatched">
                          <summary>
                            {preview.continuation} line{preview.continuation === 1 ? '' : 's'} did
                            not match and were appended to the previous row
                          </summary>
                          <pre>
                            {preview.lines.filter((l) => l.status !== 'parsed').slice(0, 10)
                              .map((l) => l.text).join('\n')}
                          </pre>
                          <div className="rule-hint">
                            That is correct for stack traces and indented blocks. If these
                            are ordinary log lines, the pattern is too narrow.
                          </div>
                        </details>
                      )}
                    </section>

                    {/* Step 4 — name it */}
                    <section className="rule-step">
                      <div className="rule-step-head">
                        <span className="rule-step-n">4</span>
                        <span>Name and save</span>
                      </div>
                      <label className="rule-field rule-field-inline">
                        <span>Rule name</span>
                        <input
                          className="input input-mono"
                          value={rule.name || ''}
                          onChange={(e) => patch({ name: e.target.value })}
                          placeholder="gateway-log"
                          spellCheck={false}
                        />
                      </label>
                      {nameTaken && (
                        <div className="rule-warning">
                          A rule with this name already exists. Saving will ask before
                          replacing it.
                        </div>
                      )}
                      <div className="rule-hint">
                        Saved as a YAML file in parsers.d next to the binary. It applies
                        to files loaded after saving, and it is an ordinary rule file you
                        can edit by hand afterwards.
                      </div>
                    </section>
                  </>
                )}

                {error && <div className="rule-error">{error}</div>}
              </div>

              <div className="modal-footer">
                <div />
                <div style={{ display: 'flex', gap: '8px' }}>
                  <button className="btn" onClick={onClose}>Cancel</button>
                  <button
                    className="btn btn-primary"
                    onClick={() => save(false)}
                    disabled={!canSave || saving}
                  >
                    {saving ? 'Saving…' : 'Save rule'}
                  </button>
                </div>
              </div>
            </>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
