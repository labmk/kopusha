import React from 'react';
import { useLocalStorage } from '../hooks/useLocalStorage';

// UpdateNotice — a new release exists.
//
// The check has always worked and reported through a small link in the
// status bar, which is a fine place for a fact and a poor place for
// news: nobody reads the bottom of a window they are not looking at.
// This is the same fact, once, where it will be seen.
//
// Deliberately not a modal. obs-viewer is opened to answer a question,
// and interrupting that to announce a version would be the wrong trade
// — the release will still be there when the operator is finished.
//
// Dismissal is remembered per version, so declining 0.3.0 stays
// declined through every restart but says nothing about 0.4.0. The
// status-bar link stays either way: dismissing the interruption should
// not throw the information away.
export default function UpdateNotice({ status }) {
  const [dismissed, setDismissed] = useLocalStorage('obs_viewer_update_dismissed', '');

  if (!status?.available || !status.latest) return null;
  if (dismissed === status.latest) return null;

  return (
    <div className="update-notice" role="status">
      <div className="update-notice-head">
        <span className="update-notice-title">obs-viewer {status.latest} is available</span>
        <button
          className="btn btn-sm"
          onClick={() => setDismissed(status.latest)}
          title="Dismiss until the next release"
          aria-label="Dismiss"
        >
          &times;
        </button>
      </div>
      <div className="update-notice-body">
        You are running {status.current}.
      </div>
      <div className="update-notice-actions">
        <a
          className="btn btn-sm btn-primary"
          href={status.url}
          target="_blank"
          rel="noreferrer noopener"
        >
          What&rsquo;s new &amp; download
        </a>
      </div>
      {/* obs-viewer never downloads or replaces anything by itself, and
          saying so is the point: an operator who expects a self-update
          would otherwise wait for one that is never coming. */}
      <div className="update-notice-foot">
        Opens the release page. obs-viewer does not download or install
        anything itself.
      </div>
    </div>
  );
}
