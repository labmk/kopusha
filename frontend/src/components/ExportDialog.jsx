import React, { useState, useEffect } from 'react';
import * as Dialog from '@radix-ui/react-dialog';
import { api } from '../api/client';

function formatSize(bytes) {
  if (bytes === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB'];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return (bytes / Math.pow(1024, i)).toFixed(i > 0 ? 1 : 0) + ' ' + units[i];
}

export default function ExportDialog({ query, totalRecords, onClose }) {
  const [outputPath, setOutputPath] = useState('');
  const [includeSelf, setIncludeSelf] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [result, setResult] = useState(null);
  const [error, setError] = useState(null);

  // Browse state for picking output location
  const [browsing, setBrowsing] = useState(false);
  const [browseEntries, setBrowseEntries] = useState([]);
  const [browsePath, setBrowsePath] = useState('');
  const [browseLoading, setBrowseLoading] = useState(false);

  // Set a sensible default output path
  useEffect(() => {
    api.browse('').then((data) => {
      const ts = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
      setOutputPath(`${data.current_path}/obs_viewer_export_${ts}.ndjson`);
    }).catch(() => {});
  }, []);

  const browseDir = async (path) => {
    setBrowseLoading(true);
    try {
      const data = await api.browse(path);
      setBrowsePath(data.current_path);
      setBrowseEntries(data.entries || []);
    } catch (e) {
      setError(e.message);
    } finally {
      setBrowseLoading(false);
    }
  };

  const openBrowser = () => {
    setBrowsing(true);
    // Extract directory from current outputPath
    const dir = outputPath.substring(0, outputPath.lastIndexOf('/')) || outputPath.substring(0, outputPath.lastIndexOf('\\')) || '';
    browseDir(dir || '');
  };

  const selectDir = (dirPath) => {
    const filename = outputPath.split('/').pop().split('\\').pop() || 'export.ndjson';
    setOutputPath(`${dirPath}/${filename}`);
    setBrowsing(false);
  };

  const handleExport = async () => {
    if (!outputPath.trim()) {
      setError('Output path is required');
      return;
    }

    setExporting(true);
    setError(null);
    setResult(null);

    try {
      const exportResult = await api.exportData(query, outputPath.trim());

      // Self-copy if requested
      let selfCopyPath = null;
      if (includeSelf) {
        const targetDir = outputPath.substring(0, outputPath.lastIndexOf('/')) || outputPath.substring(0, outputPath.lastIndexOf('\\')) || '.';
        const copyResult = await api.selfCopy(targetDir);
        selfCopyPath = copyResult.path;
      }

      setResult({
        records: exportResult.records,
        path: exportResult.path,
        selfCopyPath,
      });
    } catch (e) {
      setError(e.message);
    } finally {
      setExporting(false);
    }
  };

  return (
    <Dialog.Root open onOpenChange={(o) => { if (!o) onClose(); }}>
      <Dialog.Portal>
        <Dialog.Overlay className="rdx-overlay" />
        <Dialog.Content className="rdx-content" style={{ width: '520px' }}>
          <div className="modal-header">
            <Dialog.Title asChild><h3>Export Data</h3></Dialog.Title>
            <Dialog.Close asChild>
              <button className="btn btn-sm" aria-label="Close">&times;</button>
            </Dialog.Close>
          </div>
          <Dialog.Description className="sr-only">
            Configure the output location and optional binary copy, then export the current filter set to NDJSON.
          </Dialog.Description>

          <div className="export-options">
          {/* Summary */}
          <div style={{ fontSize: '12px', color: 'var(--text-secondary)', padding: '8px 12px', background: 'var(--bg-tertiary)', borderRadius: 'var(--radius)' }}>
            <strong>{totalRecords.toLocaleString()}</strong> records match current filters.
            {query.filters?.length > 0 && (
              <span> ({query.filters.length} filter{query.filters.length > 1 ? 's' : ''} active)</span>
            )}
            {query.time_from && <span> From: {new Date(query.time_from).toLocaleString()}</span>}
            {query.time_to && <span> To: {new Date(query.time_to).toLocaleString()}</span>}
          </div>

          {/* Output path */}
          <div>
            <label style={{ fontSize: '12px', color: 'var(--text-secondary)', marginBottom: '4px', display: 'block' }}>
              Output file path
            </label>
            <div style={{ display: 'flex', gap: '6px' }}>
              <input
                className="input input-mono"
                style={{ flex: 1, fontSize: '12px' }}
                value={outputPath}
                onChange={(e) => setOutputPath(e.target.value)}
                placeholder="/path/to/output.ndjson"
              />
              <button className="btn btn-sm" onClick={openBrowser}>Browse</button>
            </div>
          </div>

          {/* Self-copy checkbox */}
          <label style={{ display: 'flex', alignItems: 'center', gap: '10px', cursor: 'pointer' }}>
            <input
              type="checkbox"
              checked={includeSelf}
              onChange={(e) => setIncludeSelf(e.target.checked)}
              style={{ accentColor: 'var(--accent)', width: '16px', height: '16px' }}
            />
            <div>
              <div style={{ fontSize: '13px' }}>Include obs_viewer binary</div>
              <div style={{ fontSize: '11px', color: 'var(--text-muted)' }}>
                Copies the executable alongside exported data so recipients can view it immediately
              </div>
            </div>
          </label>

          {/* Error */}
          {error && (
            <div style={{ padding: '8px 12px', background: 'rgba(248,113,113,0.1)', color: 'var(--danger)', fontSize: '12px', borderRadius: 'var(--radius)' }}>
              {error}
            </div>
          )}

          {/* Result */}
          {result && (
            <div style={{ padding: '12px', background: 'rgba(52,211,153,0.1)', borderRadius: 'var(--radius)', fontSize: '12px' }}>
              <div style={{ color: 'var(--success)', fontWeight: 500, marginBottom: '6px' }}>
                Export complete
              </div>
              <div style={{ fontFamily: 'var(--font-mono)', color: 'var(--text-secondary)', fontSize: '11px' }}>
                <div>{result.records.toLocaleString()} records written</div>
                <div style={{ wordBreak: 'break-all' }}>{result.path}</div>
                {result.selfCopyPath && (
                  <div style={{ marginTop: '4px' }}>Binary copied: {result.selfCopyPath}</div>
                )}
              </div>
            </div>
          )}
        </div>

        {/* Directory browser inline */}
        {browsing && (
          <div style={{ borderTop: '1px solid var(--border)', maxHeight: '250px', display: 'flex', flexDirection: 'column' }}>
            <div className="browse-path" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <span>{browsePath}</span>
              <button className="btn btn-sm btn-primary" onClick={() => selectDir(browsePath)}>
                Select this folder
              </button>
            </div>
            <div style={{ flex: 1, overflowY: 'auto' }}>
              {browseLoading ? (
                <div className="loading"><span className="spinner" /> Loading...</div>
              ) : (
                browseEntries.filter((e) => e.is_dir).map((entry) => (
                  <div
                    key={entry.path}
                    className="browse-entry is-dir"
                    onClick={() => browseDir(entry.path)}
                  >
                    <span className="icon">📁</span>
                    <span>{entry.name}</span>
                  </div>
                ))
              )}
            </div>
          </div>
        )}

          <div className="modal-footer">
            <div />
            <div style={{ display: 'flex', gap: '8px' }}>
              <button className="btn" onClick={onClose}>
                {result ? 'Done' : 'Cancel'}
              </button>
              {!result && (
                <button
                  className="btn btn-primary"
                  onClick={handleExport}
                  disabled={exporting || !outputPath.trim()}
                >
                  {exporting ? (
                    <><span className="spinner" /> Exporting...</>
                  ) : (
                    `Export ${totalRecords.toLocaleString()} records`
                  )}
                </button>
              )}
            </div>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
