import React, { useState, useEffect, useCallback } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { api } from './api/client';
import { useLocalStorage } from './hooks/useLocalStorage';
import {
  qk,
  useVersion,
  useModules,
  useSettings,
  useFiles,
  useFields,
  useLogQuery,
  useUpdateStatus,
} from './hooks/useApiQueries';
import { formatTimeout } from './utils/datetime';
import FilePanel from './components/FilePanel';
import FileBrowser from './components/FileBrowser';
import TimeFilter from './components/TimeFilter';
import QueryBuilder from './components/QueryBuilder';
import LogTable from './components/LogTable';
import ExportDialog from './components/ExportDialog';
import PaginationBar from './components/PaginationBar';
import { moduleComponents } from './moduleRegistry';

export default function App() {
  const queryClient = useQueryClient();

  // Server-state via TanStack Query. /api/version drives the adaptive
  // healthcheck (30s healthy, 5s broken) directly via refetchInterval.
  const versionQ = useVersion();
  const updateQ = useUpdateStatus();
  const modulesQ = useModules();
  const settingsQ = useSettings();
  const filesQ = useFiles();

  const version = versionQ.data?.version || '';
  const backendOk = !versionQ.isError;
  const files = filesQ.data?.files || [];
  const timestampField = filesQ.data?.timestamp_field || '@timestamp';
  const enabledCount = files.filter((f) => f.enabled).length;
  const filesEnabled = enabledCount > 0;
  const modules = modulesQ.data?.modules || [];

  // Fields only matter once a file is loaded.
  const fieldsQ = useFields(files.length > 0);
  const fields = fieldsQ.data?.fields || [];

  // Local UI state
  const [timezone, setTimezone] = useState('UTC');
  const [hourFormat, setHourFormat] = useLocalStorage('obs_viewer_hour_format', '24');
  const [hideNulls, setHideNulls] = useLocalStorage('obs_viewer_hide_nulls', true);
  const [autoFilter, setAutoFilter] = useLocalStorage('obs_viewer_auto_filter', false);

  // Filter / query state — always starts empty (no per-session persistence).
  // The portable artefact is the text-form export from the Query Builder's
  // "Text" button; users paste that into notes/chat and back here to restore.
  const [filters, setFilters] = useState([]);
  const [timeFrom, setTimeFrom] = useState('');
  const [timeTo, setTimeTo] = useState('');
  const [searchText, setSearchText] = useState('');
  const [sortOrder, setSortOrder] = useState('asc');
  const [sortField, setSortField] = useLocalStorage('obs_viewer_sort_field', '@timestamp');

  // Pending vs. active filter set. When Auto Apply is off, the live form
  // state (filters/timeFrom/timeTo/searchText) is staged; only `applied*`
  // feeds the query key. Clicking Apply copies pending → applied.
  // When Auto Apply is on, applied tracks pending automatically via the
  // effect below.
  const [appliedFilters, setAppliedFilters] = useState([]);
  const [appliedTimeFrom, setAppliedTimeFrom] = useState('');
  const [appliedTimeTo, setAppliedTimeTo] = useState('');
  const [appliedSearchText, setAppliedSearchText] = useState('');

  const [offset, setOffset] = useState(0);
  const [limit, setLimit] = useState(200);
  const [error, setError] = useState(null);
  // `notice` is a soft, non-blocking message channel (e.g. parser
  // auto-corrections like ".X." → ".*X.*"). Renders in the same status
  // row as `error` but with a neutral accent.
  const [notice, setNotice] = useState(null);

  // UI shell state
  const [showBrowser, setShowBrowser] = useState(false);
  const [showExport, setShowExport] = useState(false);
  const [theme, setTheme] = useLocalStorage('obs_viewer_theme', 'dark');
  const [activeTab, setActiveTab] = useLocalStorage('obs_viewer_active_tab', 'viewer');

  const tabModules = modules.filter(m => m.tab && moduleComponents[m.id]);
  // A module with the id 'branding' may supply a stylesheet and header
  // metadata (company, logo, title). None ships by default, so these
  // stay empty and the header renders as plain "obs-viewer" with the
  // neutral slate accent from app.css. See docs/MODULES.md.
  const brandingMod = modules.find(m => m.id === 'branding');
  const brand = brandingMod?.config || {};

  // Settings hydration — last-directory only. Portable queries live in
  // the LogQL-shaped text export from the Query Builder (copy → paste
  // anywhere → paste back to restore), not in persisted settings.
  const [lastDirectory, setLastDirectory] = useState('');
  useEffect(() => {
    const s = settingsQ.data;
    if (!s) return;
    if (s.last_directory) setLastDirectory(s.last_directory);
  }, [settingsQ.data]);

  // If the last-active tab is no longer present (its module's section was
  // removed, or the registry doesn't carry its component), drop back to
  // the viewer.
  useEffect(() => {
    if (!modulesQ.data) return;
    const valid = new Set([
      'viewer',
      ...tabModules.map(m => m.id),
    ]);
    if (!valid.has(activeTab)) setActiveTab('viewer');
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [modulesQ.data]);

  // Theme + branding side effects.
  useEffect(() => {
    document.documentElement.setAttribute('data-theme', theme);
  }, [theme]);

  useEffect(() => {
    const style = brandingMod?.style;
    if (!style) return;
    const link = document.createElement('link');
    link.rel = 'stylesheet';
    link.href = style;
    link.dataset.brandLink = '1';
    document.head.appendChild(link);
    return () => link.remove();
  }, [brandingMod?.style]);

  useEffect(() => {
    const title = brand.document_title || brand.header_text || 'obs-viewer';
    document.title = title;
  }, [brand.document_title, brand.header_text]);

  useEffect(() => {
    document.querySelectorAll('link[data-brand-icon]').forEach(el => el.remove());
    const list = brand.favicons || [];
    for (const icon of list) {
      const link = document.createElement('link');
      link.rel = 'icon';
      link.href = icon.href;
      if (icon.type) link.type = icon.type;
      if (icon.sizes) link.setAttribute('sizes', icon.sizes);
      link.dataset.brandIcon = '1';
      document.head.appendChild(link);
    }
  }, [brand.favicons]);

  const toggleTheme = () => setTheme(t => t === 'dark' ? 'light' : 'dark');

  // Tab-close beacon: when the operator closes their last tab, fire a
  // best-effort shutdown signal so the server exits within ~2s instead
  // of waiting for the 180s inactivity timeout. The backend's grace
  // period cancels the shutdown if another tab keeps polling.
  // pagehide is the right event (works on close + back/forward cache);
  // beforeunload is unreliable on mobile and sometimes blocks tab close.
  useEffect(() => {
    const onPageHide = () => {
      try {
        navigator.sendBeacon('/api/shutdown', new Blob([''], { type: 'application/json' }));
      } catch { /* sendBeacon is best-effort; ignore failures */ }
    };
    window.addEventListener('pagehide', onPageHide);
    return () => window.removeEventListener('pagehide', onPageHide);
  }, []);

  const handleStopServer = async () => {
    if (!window.confirm('Stop the obs-viewer server? You will need to re-run obs_viewer.exe to use it again.')) return;
    try {
      await fetch('/api/shutdown', { method: 'POST' });
    } catch { /* connection will drop as the server exits; expected */ }
    // Try to close the tab. Most browsers refuse window.close() on
    // user-opened tabs (security: only script-opened tabs can be
    // script-closed). When that fails — which is the common case —
    // we replace the document with a clear "server stopped" message
    // so the operator knows what happened and can close the tab.
    try { window.close(); } catch { /* ignore */ }
    // Allow a tick for window.close to take effect when it does work.
    setTimeout(() => {
      // If we're still here, window.close was blocked. Show a final
      // screen and stop the React tree to free everything cleanly.
      document.title = 'obs-viewer — stopped';
      document.body.innerHTML = `
        <div style="display:flex;align-items:center;justify-content:center;height:100vh;font-family:system-ui,sans-serif;background:#1a1d21;color:#cbd5e1;">
          <div style="text-align:center;max-width:520px;padding:24px;">
            <div style="font-size:48px;margin-bottom:16px;">&#9209;</div>
            <h1 style="font-size:18px;font-weight:500;margin:0 0 8px;color:#e2e8f0;">obs-viewer server stopped</h1>
            <p style="font-size:13px;color:#94a3b8;margin:0 0 16px;">You can close this tab.</p>
            <p style="font-size:11px;color:#64748b;">To use obs-viewer again, re-run obs_viewer.exe.</p>
          </div>
        </div>`;
    }, 200);
  };

  // Auto Apply: copy pending form state → applied (which feeds the query
  // key) after a 100ms debounce. Sort/file-set changes always apply.
  useEffect(() => {
    if (!autoFilter) return;
    const t = setTimeout(() => {
      setAppliedFilters(filters);
      setAppliedTimeFrom(timeFrom);
      setAppliedTimeTo(timeTo);
      setAppliedSearchText(searchText);
      setOffset(0);
    }, 100);
    return () => clearTimeout(t);
  }, [autoFilter, filters, timeFrom, timeTo, searchText]);

  // Reset pagination on sort change.
  useEffect(() => { setOffset(0); }, [sortOrder, sortField]);

  // The actual query — TanStack Query handles caching, dedup, in-flight
  // cancellation, and the "previousData" placeholder so the table doesn't
  // flash empty on a parameter change.
  const queryParams = {
    filters: appliedFilters,
    time_from: appliedTimeFrom,
    time_to: appliedTimeTo,
    sort_order: sortOrder,
    sort_field: sortField,
    search_text: appliedSearchText,
    offset,
    limit,
  };
  const logQ = useLogQuery(queryParams, filesEnabled);
  const queryResult = logQ.data;
  const loading = logQ.isFetching;

  // Surface query errors to the toast bar. Manual error state still
  // exists for non-query errors (file ops below).
  useEffect(() => {
    if (logQ.error) setError(logQ.error.message);
  }, [logQ.error]);

  // Invalidate cached query results when the enabled file set changes —
  // file membership isn't part of the query key.
  useEffect(() => {
    queryClient.invalidateQueries({ queryKey: ['query'] });
  }, [files.map(f => `${f.id}:${f.enabled ? 1 : 0}`).join(','), queryClient]);

  const applyFilter = useCallback(() => {
    setAppliedFilters(filters);
    setAppliedTimeFrom(timeFrom);
    setAppliedTimeTo(timeTo);
    setAppliedSearchText(searchText);
    setOffset(0);
  }, [filters, timeFrom, timeTo, searchText]);

  // File operations — mutations are still ad-hoc `api.*` calls;
  // each invalidates the relevant query keys to trigger refetch.
  const invalidateFileSet = () => {
    queryClient.invalidateQueries({ queryKey: qk.files });
    queryClient.invalidateQueries({ queryKey: qk.fields });
    queryClient.invalidateQueries({ queryKey: qk.timeRange });
  };

  const handleFileLoaded = () => invalidateFileSet();

  const handleToggleFile = async (id, enabled) => {
    try {
      await api.toggleFile(id, enabled);
      invalidateFileSet();
    } catch (e) { setError(e.message); }
  };

  const handleUnloadFile = async (id) => {
    try {
      await api.unloadFile(id);
      invalidateFileSet();
    } catch (e) { setError(e.message); }
  };

  const handleToggleAll = async (enabled) => {
    try {
      await Promise.all(files.map((f) => api.toggleFile(f.id, enabled)));
      invalidateFileSet();
    } catch (e) { setError(e.message); }
  };

  const handleRemoveAll = async () => {
    if (files.length === 0) return;
    if (!window.confirm(`Unload all ${files.length} files from the viewer?`)) return;
    try {
      for (const f of files) {
        await api.unloadFile(f.id);
      }
      invalidateFileSet();
    } catch (e) { setError(e.message); }
  };

  // Single setter the QueryBuilder's text dialog calls on Apply: updates
  // both From and To at once so a single text-paste round-trip doesn't
  // trigger two intermediate query re-runs.
  const handleTimeChange = (from, to) => {
    setTimeFrom(from || '');
    setTimeTo(to || '');
  };

  const totalRecords = queryResult?.total_count ?? 0;

  return (
    <div className="app-layout">
      {/* Header */}
      <header className="app-header">
        <div className="logo" style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
          {(brand.logo || brand.company) && (
            brand.link ? (
              <a
                href={brand.link}
                target="_blank"
                rel="noreferrer noopener"
                style={{ display: 'flex', alignItems: 'center', gap: '8px', color: 'inherit', textDecoration: 'none' }}
                title={brand.company || ''}
              >
                {brand.logo && <img src={brand.logo} alt="" width="22" height="22" style={{ display: 'block' }} />}
                {brand.company && <span className="logo-brand">{brand.company}</span>}
              </a>
            ) : (
              <span style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                {brand.logo && <img src={brand.logo} alt="" width="22" height="22" style={{ display: 'block' }} />}
                {brand.company && <span className="logo-brand">{brand.company}</span>}
              </span>
            )
          )}
          <span> {brand.header_text || 'obs-viewer'} </span><span>v{version}</span>
        </div>
        <div className="tab-switcher">
          <button
            className={`tab-btn ${activeTab === 'viewer' ? 'active' : ''}`}
            onClick={() => setActiveTab('viewer')}
          >
            OBS Viewer
          </button>
          {tabModules.map(m => (
            <button
              key={m.id}
              className={`tab-btn ${activeTab === m.id ? 'active' : ''}`}
              onClick={() => setActiveTab(m.id)}
            >
              {m.tab.label}
            </button>
          ))}
        </div>
        <div style={{ display: 'flex', gap: '8px', alignItems: 'center' }}>
          <div className="tz-picker">
            <span>Timezone:</span>
            <select value={timezone} onChange={(e) => setTimezone(e.target.value)}>
              <option value="Etc/GMT+12">UTC-12</option>
              <option value="Etc/GMT+11">UTC-11</option>
              <option value="Etc/GMT+10">UTC-10</option>
              <option value="Etc/GMT+9">UTC-09</option>
              <option value="Etc/GMT+8">UTC-08</option>
              <option value="Etc/GMT+7">UTC-07</option>
              <option value="Etc/GMT+6">UTC-06</option>
              <option value="Etc/GMT+5">UTC-05</option>
              <option value="Etc/GMT+4">UTC-04</option>
              <option value="Etc/GMT+3">UTC-03</option>
              <option value="Etc/GMT+2">UTC-02</option>
              <option value="Etc/GMT+1">UTC-01</option>
              <option value="UTC">UTC</option>
              <option value="Etc/GMT-1">UTC+01</option>
              <option value="Etc/GMT-2">UTC+02</option>
              <option value="Etc/GMT-3">UTC+03</option>
              <option value="Etc/GMT-4">UTC+04</option>
              <option value="Etc/GMT-5">UTC+05</option>
              <option value="Etc/GMT-6">UTC+06</option>
              <option value="Etc/GMT-7">UTC+07</option>
              <option value="Etc/GMT-8">UTC+08</option>
              <option value="Etc/GMT-9">UTC+09</option>
              <option value="Etc/GMT-10">UTC+10</option>
              <option value="Etc/GMT-11">UTC+11</option>
              <option value="Etc/GMT-12">UTC+12</option>
            </select>
            <span style={{ marginLeft: '6px' }}>Time format:</span>
            <select value={hourFormat} onChange={(e) => setHourFormat(e.target.value)}>
              <option value="24">24H</option>
              <option value="12">12H</option>
            </select>
            <label style={{ marginLeft: '8px', display: 'flex', alignItems: 'center', gap: '4px', cursor: 'pointer' }} title="Hide null/empty fields in expanded row view">
              <input
                type="checkbox"
                checked={hideNulls}
                onChange={(e) => setHideNulls(e.target.checked)}
                style={{ accentColor: 'var(--accent)' }}
              />
              Hide null
            </label>
            <label style={{ marginLeft: '8px', display: 'flex', alignItems: 'center', gap: '4px', cursor: 'pointer' }} title="Automatically re-run the query when filters/time/search change">
              <input
                type="checkbox"
                checked={autoFilter}
                onChange={(e) => setAutoFilter(e.target.checked)}
                style={{ accentColor: 'var(--accent)' }}
              />
              Auto Apply
            </label>
          </div>
          <button
            className="theme-toggle"
            onClick={toggleTheme}
            title={theme === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'}
          >
            {theme === 'dark' ? '☀' : '☾'}
          </button>
          <button className="btn btn-primary" onClick={() => setShowExport(true)}>
            Export
          </button>
        </div>
      </header>

      {/* Top bar - time filter + query builder */}
      <div className="top-bar" style={{ display: activeTab === 'viewer' ? undefined : 'none' }}>
        <TimeFilter
          timeFrom={timeFrom}
          timeTo={timeTo}
          onTimeFromChange={setTimeFrom}
          onTimeToChange={setTimeTo}
        />
        <div style={{ width: '1px', height: '24px', background: 'var(--border)' }} />
        <label
          style={{ display: 'flex', alignItems: 'center', gap: '4px', fontSize: '12px', color: 'var(--text-secondary)' }}
          title="Primary sort/time field. ObservedTimestamp falls back to @timestamp if not present in the loaded files."
        >
          Sort:
          <select value={sortField} onChange={(e) => setSortField(e.target.value)}>
            <option value="@timestamp">@timestamp</option>
            <option value="ObservedTimestamp">ObservedTimestamp</option>
          </select>
        </label>
        <div style={{ width: '1px', height: '24px', background: 'var(--border)' }} />
        <QueryBuilder
          fields={fields}
          filters={filters}
          onFiltersChange={setFilters}
          searchText={searchText}
          onSearchTextChange={setSearchText}
          timeFrom={timeFrom}
          timeTo={timeTo}
          onTimeChange={handleTimeChange}
          onApply={applyFilter}
          onNotice={setNotice}
        />
      </div>

      {activeTab === 'viewer' && (
        <FilePanel
          files={files}
          onToggle={handleToggleFile}
          onUnload={handleUnloadFile}
          onToggleAll={handleToggleAll}
          onRemoveAll={handleRemoveAll}
          onOpenBrowser={() => setShowBrowser(true)}
        />
      )}

      {tabModules.map(m => {
        if (activeTab !== m.id) return null;
        const Component = moduleComponents[m.id];
        return (
          <div key={m.id} className={`${m.id}-main module-tab-content`}>
            <Component
              onIngested={handleFileLoaded}
              onSwitchToViewer={() => setActiveTab('viewer')}
              onBrowseExtractDir={(path) => {
                setLastDirectory(path);
                setShowBrowser(true);
              }}
              moduleConfig={m.config || {}}
            />
          </div>
        );
      })}

      <div className="main-area" style={{ display: activeTab === 'viewer' ? undefined : 'none' }}>
        {error && (
          <div style={{ padding: '8px 16px', background: 'rgba(248,113,113,0.1)', color: 'var(--danger)', fontSize: '12px', borderBottom: '1px solid var(--border)' }}>
            {error}
            <button onClick={() => setError(null)} style={{ marginLeft: '12px', cursor: 'pointer', background: 'none', border: 'none', color: 'var(--danger)' }}>
              dismiss
            </button>
          </div>
        )}
        {notice && (
          <div style={{ padding: '8px 16px', background: 'rgba(0,150,150,0.08)', color: 'var(--text-secondary)', fontSize: '12px', borderBottom: '1px solid var(--border)' }}>
            {notice}
            <button onClick={() => setNotice(null)} style={{ marginLeft: '12px', cursor: 'pointer', background: 'none', border: 'none', color: 'var(--text-secondary)' }}>
              dismiss
            </button>
          </div>
        )}

        {files.length === 0 ? (
          <div className="empty-state">
            <div className="icon-large">&#128194;</div>
            <div>No files loaded</div>
            <button className="btn btn-primary" onClick={() => setShowBrowser(true)}>
              Open Files
            </button>
          </div>
        ) : enabledCount === 0 ? (
          <div className="empty-state">
            <div className="icon-large">&#128065;</div>
            <div>No files selected - enable files in the left panel</div>
          </div>
        ) : loading && !queryResult ? (
          <div className="loading">
            <span className="spinner" />
            Querying...
          </div>
        ) : (
          <LogTable
            result={queryResult}
            sortOrder={sortOrder}
            onSortOrderChange={setSortOrder}
            timestampField={timestampField}
            timezone={timezone}
            hourFormat={hourFormat}
            hideNulls={hideNulls}
          />
        )}

        {queryResult && totalRecords > 0 && (
          <PaginationBar
            offset={offset}
            limit={limit}
            total={totalRecords}
            loading={loading}
            onJump={(newOffset) => setOffset(newOffset)}
            onLimitChange={(n) => { setLimit(n); setOffset(0); }}
          />
        )}
      </div>

      <div className="status-bar">
        <span>
          {enabledCount}/{files.length} files active &middot;{' '}
          {totalRecords.toLocaleString()} records
          {timestampField ? ` · ts: ${timestampField}` : ''}
        </span>
        <span style={{ display: 'flex', alignItems: 'center', gap: '12px' }}>
          {modules.length > 0 && (
            <span title="Enabled modules (from obs_viewer.conf sections)" style={{ color: 'var(--text-secondary)' }}>
              modules: {modules.map(m => m.id).join(', ')}
            </span>
          )}
          {updateQ.data?.available && (
            <a
              href={updateQ.data.url}
              target="_blank"
              rel="noopener noreferrer"
              className="status-update"
              title={`You are running ${updateQ.data.current}. Opens the release page — obs-viewer does not download or install anything itself.`}
            >
              v{updateQ.data.latest} available
            </a>
          )}
          {versionQ.data?.idle_timeout_seconds > 0 && (
            <span title="Server shuts down after this many seconds with no API activity" style={{ color: 'var(--text-secondary)' }}>
              idle timeout: {formatTimeout(versionQ.data.idle_timeout_seconds)}
            </span>
          )}
          {!backendOk && <span className="status-offline">no connection to the backend</span>}
          <button
            onClick={handleStopServer}
            title="Stop the obs-viewer server immediately"
            style={{
              fontSize: '11px',
              padding: '2px 8px',
              background: 'transparent',
              color: 'var(--danger)',
              border: '1px solid var(--danger)',
              borderRadius: 'var(--radius)',
              cursor: 'pointer',
            }}
          >
            Stop server
          </button>
        </span>
      </div>

      {showBrowser && (
        <FileBrowser
          onClose={() => setShowBrowser(false)}
          onFileLoaded={handleFileLoaded}
          initialPath={lastDirectory}
          onDirectoryChanged={setLastDirectory}
        />
      )}

      {showExport && (
        <ExportDialog
          query={{ filters: appliedFilters, time_from: appliedTimeFrom, time_to: appliedTimeTo, sort_order: sortOrder, search_text: appliedSearchText }}
          totalRecords={totalRecords}
          onClose={() => setShowExport(false)}
        />
      )}
    </div>
  );
}
