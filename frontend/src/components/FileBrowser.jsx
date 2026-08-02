import React, { useState, useEffect } from 'react';
import { api } from '../api/client';
import ParseDiagnosis from './ParseDiagnosis';

function formatSize(bytes) {
  if (bytes === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB'];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return (bytes / Math.pow(1024, i)).toFixed(i > 0 ? 1 : 0) + ' ' + units[i];
}

export default function FileBrowser({ onClose, onFileLoaded, initialPath, onDirectoryChanged, onBuildRule }) {
  const [currentPath, setCurrentPath] = useState('');
  const [entries, setEntries] = useState([]);
  const [selected, setSelected] = useState(new Set());
  const [loading, setLoading] = useState(false);
  const [loadingFiles, setLoadingFiles] = useState(new Set());
  const [loadingAll, setLoadingAll] = useState(false);
  const [error, setError] = useState(null);
  const [pathInput, setPathInput] = useState('');
  const [drives, setDrives] = useState([]);
  const [filterText, setFilterText] = useState('');
  // Diagnosis of the file that just failed to load. A load error on its
  // own says only that obs-viewer is unhappy; this says which adapters
  // looked, what each objected to, and what the first line actually was
  // — and offers the rule builder as the way out.
  const [diagnosis, setDiagnosis] = useState(null);
  const [diagnosedFile, setDiagnosedFile] = useState('');

  const diagnose = async (path, name) => {
    try {
      const d = await api.explainFile(path);
      setDiagnosis(d);
      setDiagnosedFile(name || path);
    } catch { /* the load error already stands on its own */ }
  };

  const browse = async (path) => {
    setLoading(true);
    setError(null);
    try {
      const data = await api.browse(path);
      setCurrentPath(data.current_path);
      setPathInput(data.current_path);
      setEntries(data.entries || []);
      setDrives(data.drives || []);
      setSelected(new Set());
      setFilterText('');
      if (onDirectoryChanged) onDirectoryChanged(data.current_path);
    } catch (e) {
      setError(e.message);
    } finally {
      setLoading(false);
    }
  };

  // Current drive letter from path (Windows) — matches picker value.
  const currentDrive = (() => {
    const m = /^([A-Za-z]:\\)/.exec(currentPath || '');
    return m ? m[1].toUpperCase() : '';
  })();

  useEffect(() => {
    browse(initialPath || '');
  }, []);

  const handleEntryClick = (entry) => {
    // Zip files are browsable: clicking enters the archive and lists
    // its .ndjson contents. The backend returns `entries` from the
    // zip reader and tags each one with a virtual `<zip>|<inner>` path
    // that /api/files/load knows how to extract.
    const isZip = !entry.is_dir && /\.zip$/i.test(entry.name);
    if (entry.is_dir || isZip) {
      browse(entry.path);
    } else {
      setSelected((prev) => {
        const next = new Set(prev);
        if (next.has(entry.path)) {
          next.delete(entry.path);
        } else {
          next.add(entry.path);
        }
        return next;
      });
    }
  };

  const handleLoad = async () => {
    let failed = false;
    for (const path of selected) {
      setLoadingFiles((prev) => new Set([...prev, path]));
      try {
        await api.loadFile(path);
        onFileLoaded();
      } catch (e) {
        setError(e.message);
        failed = true;
        // Diagnose the first failure only: a directory of files that
        // all fail for the same reason needs one explanation, not
        // twenty stacked panels.
        if (!diagnosis) await diagnose(path, path.split(/[/\\|]/).pop());
      } finally {
        setLoadingFiles((prev) => {
          const next = new Set(prev);
          next.delete(path);
          return next;
        });
      }
    }
    if (!failed) {
      onClose();
    }
  };

  const handlePathSubmit = (e) => {
    e.preventDefault();
    if (pathInput.trim()) {
      browse(pathInput.trim());
    }
  };

  // Apply the filter input case-insensitively to the file/dir name.
  // The ".." parent entry is always shown so the user can navigate out
  // even with a filter active.
  const visibleEntries = entries.filter((e) => {
    if (e.name === '..') return true;
    if (!filterText) return true;
    return e.name.toLowerCase().includes(filterText.toLowerCase());
  });

  const handleSelectAll = () => {
    // Toggle-all only considers what's currently visible — if a filter
    // is active, "Toggle All" means "every file that matches the filter".
    const filePaths = visibleEntries.filter((e) => !e.is_dir).map((e) => e.path);
    if (filePaths.length === selected.size && filePaths.every((p) => selected.has(p))) {
      setSelected(new Set());
    } else {
      setSelected(new Set(filePaths));
    }
  };

  const handleLoadAll = async () => {
    setLoadingAll(true);
    setError(null);
    // Iterate files individually so each one gets its own progress spinner
    // while the backend is ingesting it. A true percentage-accurate progress
    // bar would require streaming progress from DuckDB's read_json_auto,
    // which does not expose an ingest-progress callback — so we use a
    // per-file spinner instead.
    //
    // "Load All" honors the active filter: only the visible non-directory
    // entries are loaded. Without a filter this is every file in the dir
    // or zip; with a filter it's just the matches the operator can see.
    const fileEntries = visibleEntries.filter((e) => !e.is_dir);
    const errs = [];
    let loadedAny = false;
    for (const entry of fileEntries) {
      setLoadingFiles((prev) => new Set([...prev, entry.path]));
      try {
        await api.loadFile(entry.path);
        loadedAny = true;
      } catch (e) {
        errs.push(entry.name + ': ' + e.message);
        if (errs.length === 1) await diagnose(entry.path, entry.name);
      } finally {
        setLoadingFiles((prev) => {
          const next = new Set(prev);
          next.delete(entry.path);
          return next;
        });
      }
    }
    if (loadedAny) onFileLoaded();
    if (errs.length > 0) {
      setError(errs.join('; '));
    } else {
      onClose();
    }
    setLoadingAll(false);
  };

  const fileCount = visibleEntries.filter((e) => !e.is_dir).length;

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal" onClick={(e) => e.stopPropagation()}>
        <div className="modal-header">
          <h3>Open NDJSON Files</h3>
          <button className="btn btn-sm" onClick={onClose}>&times;</button>
        </div>

        {/* Path bar */}
        <form onSubmit={handlePathSubmit} style={{ display: 'flex', padding: '8px 16px', gap: '8px', borderBottom: '1px solid var(--border)' }}>
          {drives.length > 0 && (
            <select
              className="btn btn-sm"
              value={currentDrive}
              onChange={(e) => browse(e.target.value)}
              style={{ fontSize: '12px' }}
              title="Switch drive"
            >
              {!drives.includes(currentDrive) && currentDrive && (
                <option value={currentDrive}>{currentDrive}</option>
              )}
              {drives.map((d) => (
                <option key={d} value={d}>{d}</option>
              ))}
            </select>
          )}
          <input
            className="input input-mono"
            style={{ flex: 1 }}
            value={pathInput}
            onChange={(e) => setPathInput(e.target.value)}
            placeholder="Enter path..."
          />
          <button className="btn btn-sm" type="submit">Go</button>
        </form>

        <div className="browse-path">{currentPath}</div>

        {/* Filter bar */}
        <div style={{ display: 'flex', padding: '6px 16px', gap: '8px', alignItems: 'center', borderBottom: '1px solid var(--border)' }}>
          <input
            className="input"
            type="text"
            value={filterText}
            onChange={(e) => setFilterText(e.target.value)}
            placeholder="Filter by name..."
            style={{ flex: 1, fontSize: '12px' }}
          />
          {filterText && (
            <>
              <span style={{ fontSize: '11px', color: 'var(--text-muted)', whiteSpace: 'nowrap' }}>
                {visibleEntries.filter((e) => e.name !== '..').length} of {entries.filter((e) => e.name !== '..').length}
              </span>
              <button
                className="btn btn-sm"
                onClick={() => setFilterText('')}
                title="Clear filter"
              >
                &times;
              </button>
            </>
          )}
        </div>

        {error && (
          <div style={{ padding: '8px 16px', color: 'var(--danger)', fontSize: '12px' }}>
            {error}
          </div>
        )}

        {diagnosis && (
          <ParseDiagnosis
            diagnosis={diagnosis}
            fileName={diagnosedFile}
            onClose={() => setDiagnosis(null)}
            onBuildRule={onBuildRule ? () => onBuildRule(diagnosis.first_line) : undefined}
          />
        )}

        <div className="modal-body">
          {loading ? (
            <div className="loading"><span className="spinner" /> Browsing...</div>
          ) : (
            visibleEntries.map((entry) => (
              <div
                key={entry.path}
                className={`browse-entry ${entry.is_dir ? 'is-dir' : 'is-file'}`}
                onClick={() => handleEntryClick(entry)}
                style={!entry.is_dir && selected.has(entry.path) ? { background: 'var(--accent-muted)' } : {}}
              >
                {!entry.is_dir && !/\.zip$/i.test(entry.name) && (
                  <input
                    type="checkbox"
                    checked={selected.has(entry.path)}
                    onChange={() => {}}
                    style={{ accentColor: 'var(--accent)', cursor: 'pointer' }}
                  />
                )}
                <span className="icon">
                  {entry.is_dir ? '📁' : /\.zip$/i.test(entry.name) ? '🗜️' : '📄'}
                </span>
                <span style={{ flex: 1 }}>{entry.name}</span>
                {!entry.is_dir && (
                  <span className="entry-size">{formatSize(entry.size)}</span>
                )}
                {loadingFiles.has(entry.path) && <span className="spinner" />}
              </div>
            ))
          )}
        </div>

        <div className="modal-footer">
          <div style={{ display: 'flex', gap: '8px', alignItems: 'center' }}>
            <button className="btn btn-sm" onClick={handleSelectAll}>
              Toggle All
            </button>
            <span style={{ fontSize: '11px', color: 'var(--text-muted)' }}>
              {selected.size} selected
            </span>
          </div>
          <div style={{ display: 'flex', gap: '8px' }}>
            <button className="btn" onClick={onClose}>Cancel</button>
            <button
              className="btn btn-primary"
              onClick={handleLoadAll}
              disabled={fileCount === 0 || loadingAll}
            >
              {loadingAll ? 'Loading...' : `Load All (${fileCount})`}
            </button>
            <button
              className="btn btn-primary"
              onClick={handleLoad}
              disabled={selected.size === 0}
            >
              Load Selected
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
