import React, { useState } from 'react';
import { useLocalStorage } from '../hooks/useLocalStorage';
import { api } from '../api/client';

// UpdateNotice — a new release exists, and can be installed from here.
//
// The check has always worked and reported through a small link in the
// status bar, which is a fine place for a fact and a poor place for
// news: nobody reads the bottom of a window they are not looking at.
// This is the same fact, once, where it will be seen.
//
// Deliberately not a modal. kopusha is opened to answer a question,
// and interrupting that to announce a version would be the wrong trade
// — the release will still be there when the operator is finished.
//
// Dismissal is remembered per version, so declining 0.3.0 stays
// declined through every restart but says nothing about 0.4.0. The
// status-bar link stays either way: dismissing the interruption should
// not throw the information away.
//
// Installing is two steps, and the middle screen is the point of the
// feature rather than a formality: preparing downloads and verifies but
// writes nothing, so the operator agrees to a change they have already
// been shown — including which of their own parser rules will be kept.
export default function UpdateNotice({ status }) {
  const [dismissed, setDismissed] = useLocalStorage('kopusha_update_dismissed', '');
  const [phase, setPhase] = useState('idle');
  const [plan, setPlan] = useState(null);
  const [result, setResult] = useState(null);
  const [error, setError] = useState('');

  if (!status?.available || !status.latest) return null;
  if (dismissed === status.latest && phase === 'idle') return null;

  async function prepare() {
    setPhase('preparing');
    setError('');
    try {
      setPlan(await api.prepareUpdate());
      setPhase('planned');
    } catch (e) {
      setError(e.message);
      setPhase('idle');
    }
  }

  async function apply() {
    setPhase('applying');
    setError('');
    try {
      setResult(await api.applyUpdate());
      setPhase('applied');
    } catch (e) {
      setError(e.message);
      setPhase('planned');
    }
  }

  const kept = plan?.rules?.changes?.filter((c) => c.action === 'keep') ?? [];
  const writes = plan?.rules?.changes?.filter((c) => c.action !== 'keep') ?? [];

  return (
    <div className="update-notice" role="status">
      <div className="update-notice-head">
        <span className="update-notice-title">
          {phase === 'applied'
            ? `Updated to ${result?.to}`
            : `kopusha ${status.latest} is available`}
        </span>
        {phase === 'idle' && (
          <button
            className="btn btn-sm"
            onClick={() => setDismissed(status.latest)}
            title="Dismiss until the next release"
            aria-label="Dismiss"
          >
            &times;
          </button>
        )}
      </div>

      {phase === 'applied' ? (
        <>
          <div className="update-notice-body">
            {/* The restart is already in flight when this renders. Saying
                so beats a page that silently stops responding. */}
            Restarting into {result?.to}. This page reconnects on its own.
          </div>
          {result?.samples_written?.length > 0 && (
            <div className="update-notice-kept">
              {result.samples_written.length} sample log
              {result.samples_written.length === 1 ? '' : 's'} updated.
            </div>
          )}
          {result?.rules_kept?.length > 0 && (
            <div className="update-notice-kept">
              <strong>{result.rules_kept.length} parser rule
              {result.rules_kept.length === 1 ? '' : 's'} kept:</strong>
              <ul>
                {result.rules_kept.map((c) => (
                  <li key={c.name}>
                    <code>{c.name}</code> — {c.reason}
                  </li>
                ))}
              </ul>
            </div>
          )}
        </>
      ) : (
        <>
          <div className="update-notice-body">You are running {status.current}.</div>

          {phase === 'planned' && plan && (
            <div className="update-notice-plan">
              <div className="update-notice-verified">
                Built from commit <code>{plan.attestation?.commit?.slice(0, 12)}</code> by
                the release workflow, verified against these exact bytes.
              </div>
              {writes.length > 0 && (
                <div>
                  {writes.length} parser rule{writes.length === 1 ? '' : 's'} will change.
                </div>
              )}
              {kept.length > 0 && (
                <div className="update-notice-kept">
                  {kept.length} will be kept as {kept.length === 1 ? 'it is' : 'they are'}:
                  <ul>
                    {kept.map((c) => (
                      <li key={c.name}>
                        <code>{c.name}</code> — {c.reason}
                      </li>
                    ))}
                  </ul>
                </div>
              )}
              {plan.samples?.write?.length > 0 && (
                <div>
                  {plan.samples.write.length} sample log
                  {plan.samples.write.length === 1 ? '' : 's'}{' '}
                  {plan.samples.created ? 'will be added' : 'will be refreshed'}.
                </div>
              )}
              <div>Your config files are never replaced.</div>
            </div>
          )}

          {error && <div className="update-notice-error">{error}</div>}

          <div className="update-notice-actions">
            {phase === 'planned' ? (
              <button className="btn btn-sm btn-primary" onClick={apply}>
                Install and restart
              </button>
            ) : (
              <button
                className="btn btn-sm btn-primary"
                onClick={prepare}
                disabled={phase !== 'idle'}
              >
                {phase === 'preparing' ? 'Downloading and verifying…' : 'Update'}
              </button>
            )}
            <a
              className="btn btn-sm"
              href={status.url}
              target="_blank"
              rel="noreferrer noopener"
            >
              What&rsquo;s new
            </a>
          </div>

          <div className="update-notice-foot">
            {phase === 'planned'
              ? 'Nothing has been written yet.'
              : 'Downloads the release and checks its build provenance before writing anything.'}
          </div>
        </>
      )}
    </div>
  );
}
