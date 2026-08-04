import React from 'react';

function formatSize(bytes) {
  if (bytes === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB'];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return (bytes / Math.pow(1024, i)).toFixed(i > 0 ? 1 : 0) + ' ' + units[i];
}

export default function FilePanel({ files, onToggle, onUnload, onToggleAll, onRemoveAll, onOpenBrowser }) {
  const allEnabled = files.length > 0 && files.every((f) => f.enabled);
  const noneEnabled = files.every((f) => !f.enabled);

  return (
    <div className="file-panel">
      <div className="panel-header">
        <span>Files ({files.length})</span>
        <button className="btn btn-sm btn-primary" onClick={onOpenBrowser}>
          + Add
        </button>
      </div>

      {files.length > 0 && (
        <div style={{ display: 'flex', gap: '4px', padding: '6px 12px', borderBottom: '1px solid var(--border)' }}>
          <button
            className="btn btn-sm"
            onClick={() => onToggleAll(true)}
            disabled={allEnabled}
          >
            All
          </button>
          <button
            className="btn btn-sm"
            onClick={() => onToggleAll(false)}
            disabled={noneEnabled}
          >
            None
          </button>
          <button
            className="btn btn-sm btn-danger"
            onClick={onRemoveAll}
            style={{ marginLeft: 'auto' }}
            title="Unload every file from the viewer"
          >
            Remove all
          </button>
        </div>
      )}

      <div className="panel-body">
        {files.length === 0 ? (
          <div style={{ padding: '20px 12px', color: 'var(--text-muted)', fontSize: '12px', textAlign: 'center' }}>
            No files loaded.<br />
            Click <strong>+ Add</strong> to browse.
          </div>
        ) : (
          files.map((file) => (
            <div key={file.id} className="checkbox-row">
              {/* Named explicitly: the filename beside it is a div, not a
                  label, so without this the control announces only its
                  state and a screen reader user cannot tell the rows
                  apart. */}
              <input
                type="checkbox"
                checked={file.enabled}
                onChange={(e) => onToggle(file.id, e.target.checked)}
                aria-label={`Include ${file.name} in queries`}
              />
              <div style={{ flex: 1, overflow: 'hidden' }}>
                <div className="file-name" title={file.path}>
                  {file.name}
                </div>
                <div className="file-meta">
                  {file.records.toLocaleString()} rows &middot; {formatSize(file.size)}
                </div>
              </div>
              <button
                className="btn btn-sm btn-danger"
                onClick={(e) => {
                  e.stopPropagation();
                  onUnload(file.id);
                }}
                title="Unload file"
              >
                &times;
              </button>
            </div>
          ))
        )}
      </div>
    </div>
  );
}
