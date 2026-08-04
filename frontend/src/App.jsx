import React, { useState, useEffect, useCallback, useMemo, useRef } from 'react';
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
  useHistogram,
  useProfile,
  useUpdateStatus,
} from './hooks/useApiQueries';
import { formatTimeout } from './utils/datetime';
import { decodeState, writeState } from './utils/urlState';
import FilePanel from './components/FilePanel';
import FileBrowser from './components/FileBrowser';
import TimeFilter from './components/TimeFilter';
import QueryBuilder from './components/QueryBuilder';
import LogTable from './components/LogTable';
import ExportDialog from './components/ExportDialog';
import PaginationBar from './components/PaginationBar';
import RuleBuilder from './components/RuleBuilder';
import TimeHistogram from './components/TimeHistogram';
import UpdateNotice from './components/UpdateNotice';
import FieldProfile from './components/FieldProfile';
import { moduleComponents } from './moduleRegistry';

// Fixed UTC offsets rather than IANA zone names: logs are read across
// machines whose local zone is rarely the one the data came from, and a
// numeric offset is unambiguous where "Europe/Berlin" is a question about
// which side of a DST boundary a row falls on.
const TIMEZONES = [
  ...Array.from({ length: 12 }, (_, i) => [`Etc/GMT+${12 - i}`, `UTC-${String(12 - i).padStart(2, '0')}`]),
  ['UTC', 'UTC'],
  ...Array.from({ length: 12 }, (_, i) => [`Etc/GMT-${i + 1}`, `UTC+${String(i + 1).padStart(2, '0')}`]),
];

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
  // The backend names its own repository so there is one source of
  // truth — a fork changes a Go const, not a string in the SPA.
  const repoURL = versionQ.data?.repository || '';
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
  const [hourFormat, setHourFormat] = useLocalStorage('kopusha_hour_format', '24');
  const [hideNulls, setHideNulls] = useLocalStorage('kopusha_hide_nulls', true);
  const [autoFilter, setAutoFilter] = useLocalStorage('kopusha_auto_filter', false);

  // Filter / query state, seeded from the URL fragment so a shared link
  // or a reload restores the view. Read once, synchronously, before the
  // first render — seeding from an effect would fire one query with the
  // defaults and a second with the link's actual filters.
  //
  // Loaded files are deliberately not part of it: a path means
  // something only on the machine that produced it. See
  // utils/urlState.js.
  const initial = useMemo(() => decodeState(window.location.hash), []);

  const [filters, setFilters] = useState(initial.filters);
  const [timeFrom, setTimeFrom] = useState(initial.timeFrom);
  const [timeTo, setTimeTo] = useState(initial.timeTo);
  const [searchText, setSearchText] = useState(initial.searchText);
  const [sortOrder, setSortOrder] = useState(initial.sortOrder || 'asc');
  // A link's sort wins over the stored preference, and then becomes it —
  // the view you are looking at is the one you keep.
  const [sortField, setSortField] = useState(
    () => initial.sortField || localStorage.getItem('kopusha_sort_field') || '@timestamp'
  );
  useEffect(() => {
    try { localStorage.setItem('kopusha_sort_field', sortField); } catch { /* private mode */ }
  }, [sortField]);
  const [hiddenColumns, setHiddenColumns] = useState(() => new Set(initial.hiddenColumns));

  // Pending vs. active filter set. When Auto Apply is off, the live form
  // state (filters/timeFrom/timeTo/searchText) is staged; only `applied*`
  // feeds the query key. Clicking Apply copies pending → applied.
  // When Auto Apply is on, applied tracks pending automatically via the
  // effect below.
  // Seeded from the link too: a shared view has already been applied by
  // whoever shared it, so it must render as its results, not as a form
  // waiting for Apply to be pressed.
  const [appliedFilters, setAppliedFilters] = useState(initial.filters);
  const [appliedTimeFrom, setAppliedTimeFrom] = useState(initial.timeFrom);
  const [appliedTimeTo, setAppliedTimeTo] = useState(initial.timeTo);
  const [appliedSearchText, setAppliedSearchText] = useState(initial.searchText);

  const [offset, setOffset] = useState(0);
  const [limit, setLimit] = useState(initial.limit || 200);
  const [error, setError] = useState(null);
  // `notice` is a soft, non-blocking message channel (e.g. parser
  // auto-corrections like ".X." → ".*X.*"). Renders in the same status
  // row as `error` but with a neutral accent.
  const [notice, setNotice] = useState(null);

  // UI shell state
  const [showBrowser, setShowBrowser] = useState(false);
  const [showExport, setShowExport] = useState(false);
  // Rule builder. `null` is closed; a string is the sample it opens
  // with — normally the line from a failed load's diagnosis, which is
  // what turns the worst moment in the product into the entry point for
  // its most useful feature.
  const [ruleSample, setRuleSample] = useState(null);
  const [showSettings, setShowSettings] = useState(false);
  const [showProfile, setShowProfile] = useLocalStorage('kopusha_show_profile', false);
  const [theme, setTheme] = useLocalStorage('kopusha_theme', 'dark');
  const [activeTab, setActiveTab] = useLocalStorage('kopusha_active_tab', 'viewer');

  const tabModules = modules.filter(m => m.tab && moduleComponents[m.id]);
  // A module with the id 'branding' may supply a stylesheet and header
  // metadata (company, logo, title). None ships by default, so these
  // stay empty and the header renders as plain "kopusha" with the
  // neutral slate accent from app.css. See docs/MODULES.md.
  const brandingMod = modules.find(m => m.id === 'branding');
  const brand = brandingMod?.config || {};

  // Settings hydration — last-directory only. Portable queries live in
  // the pipeline-style text export from the Query Builder (copy → paste
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
    const title = brand.document_title || brand.header_text || 'kopusha';
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
    if (!window.confirm('Stop the kopusha server? You will need to re-run kopusha.exe to use it again.')) return;
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
      document.title = 'kopusha — stopped';
      document.body.innerHTML = `
        <div style="display:flex;align-items:center;justify-content:center;height:100vh;font-family:system-ui,sans-serif;background:#1a1d21;color:#cbd5e1;">
          <div style="text-align:center;max-width:520px;padding:24px;">
            <div style="font-size:48px;margin-bottom:16px;">&#9209;</div>
            <h1 style="font-size:18px;font-weight:500;margin:0 0 8px;color:#e2e8f0;">kopusha server stopped</h1>
            <p style="font-size:13px;color:#94a3b8;margin:0 0 16px;">You can close this tab.</p>
            <p style="font-size:11px;color:#64748b;">To use kopusha again, re-run kopusha.exe.</p>
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

  // The strip runs alongside the table on the same filter set. It is
  // keyed without offset/limit, so paging does not re-run it.
  const histogramQ = useHistogram(queryParams, filesEnabled);
  const profileQ = useProfile(queryParams, filesEnabled && showProfile);

  // Surface query errors to the toast bar. Manual error state still
  // exists for non-query errors (file ops below).
  useEffect(() => {
    if (logQ.error) setError(logQ.error.message);
  }, [logQ.error]);

  // Invalidate cached query results when the enabled file set changes —
  // file membership isn't part of the query key.
  useEffect(() => {
    queryClient.invalidateQueries({ queryKey: ['query'] });
    queryClient.invalidateQueries({ queryKey: ['histogram'] });
  }, [files.map(f => `${f.id}:${f.enabled ? 1 : 0}`).join(','), queryClient]);

  // Clicking a value in the profile narrows to it. Applies at once, for
  // the same reason a histogram drag does: the click *is* the decision,
  // and profiling is only navigation if acting on what you see takes
  // one step.
  const handleProfileFilter = useCallback((field, value) => {
    const next = [...filters, { field, operator: 'is', value, logic: 'AND' }];
    setFilters(next);
    setAppliedFilters(next);
    setAppliedTimeFrom(timeFrom);
    setAppliedTimeTo(timeTo);
    setAppliedSearchText(searchText);
    setOffset(0);
  }, [filters, timeFrom, timeTo, searchText]);

  // Keep the fragment in step with the applied view.
  //
  // Applied, not pending: the URL should describe the results on screen.
  // Writing the pending form state would put a half-typed filter into
  // the address bar and, worse, into any link copied while typing.
  const shareState = useMemo(() => ({
    filters: appliedFilters,
    timeFrom: appliedTimeFrom,
    timeTo: appliedTimeTo,
    searchText: appliedSearchText,
    sortField,
    sortOrder,
    hiddenColumns: [...hiddenColumns],
    limit,
  }), [appliedFilters, appliedTimeFrom, appliedTimeTo, appliedSearchText,
    sortField, sortOrder, hiddenColumns, limit]);

  useEffect(() => { writeState(shareState); }, [shareState]);

  // A popover that only closes by pressing the same button again is a
  // popover people leave open by accident. Escape and an outside click
  // are what everything else on the desktop does.
  useEffect(() => {
    if (!showSettings) return;
    const onKey = (e) => { if (e.key === 'Escape') setShowSettings(false); };
    const onDown = (e) => {
      if (!e.target.closest('.settings-wrap')) setShowSettings(false);
    };
    document.addEventListener('keydown', onKey);
    document.addEventListener('mousedown', onDown);
    return () => {
      document.removeEventListener('keydown', onKey);
      document.removeEventListener('mousedown', onDown);
    };
  }, [showSettings]);


  // A drag on the histogram applies immediately, whatever Auto Apply is
  // set to. Auto Apply exists to stop a half-typed filter from firing a
  // query on every keystroke; a drag is a completed, deliberate gesture,
  // and making the operator confirm it afterwards would undo the point
  // of dragging instead of typing.
  const handleHistogramRange = useCallback((from, to) => {
    setTimeFrom(from);
    setTimeTo(to);
    setAppliedTimeFrom(from);
    setAppliedTimeTo(to);
    setAppliedFilters(filters);
    setAppliedSearchText(searchText);
    setOffset(0);
  }, [filters, searchText]);

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
          {/* The product label opens the project page. A branding
              module's own logo and company link, above, are separate
              and unchanged — this one always points at the source of
              the software itself. */}
          {repoURL ? (
            <a
              className="logo-link"
              href={repoURL}
              target="_blank"
              rel="noreferrer noopener"
              title="Open the kopusha project page"
            >
              <span> {brand.header_text || 'kopusha'} </span><span>v{version}</span>
            </a>
          ) : (
            <span> {brand.header_text || 'kopusha'} <span>v{version}</span></span>
          )}
        </div>
        {/* The tab strip only earns its space once something can be
            switched to. With no tab modules enabled it was a control with
            one option, centred like a title — so it is rendered only when
            a module actually provides a tab. */}
        {tabModules.length > 0 && (
          <div className="tab-switcher">
            <button
              className={`tab-btn ${activeTab === 'viewer' ? 'active' : ''}`}
              onClick={() => setActiveTab('viewer')}
            >
              Parse
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
        )}
        <div className="header-actions">
          {/* Timezone, time format and theme are set once and then left
              alone. They were sharing the header with actions pressed many
              times a session, which is what made the bar feel crowded. */}
          <div className="settings-wrap">
            <button
              className={`icon-btn${showSettings ? ' active' : ''}`}
              onClick={() => setShowSettings(v => !v)}
              aria-label="Display settings"
              aria-expanded={showSettings}
              title="Timezone, time format and theme"
            >
              &#9881;
            </button>
            {showSettings && (
              <div className="settings-popover" role="dialog" aria-label="Display settings">
                <label className="settings-row">
                  <span>Timezone</span>
                  <select value={timezone} onChange={(e) => setTimezone(e.target.value)}>
                    {TIMEZONES.map(([value, label]) => (
                      <option key={value} value={value}>{label}</option>
                    ))}
                  </select>
                </label>
                <label className="settings-row">
                  <span>Time format</span>
                  <select value={hourFormat} onChange={(e) => setHourFormat(e.target.value)}>
                    <option value="24">24-hour</option>
                    <option value="12">12-hour</option>
                  </select>
                </label>
                <label className="settings-row">
                  <span>Theme</span>
                  <select value={theme} onChange={(e) => { if (e.target.value !== theme) toggleTheme(); }}>
                    <option value="dark">Dark</option>
                    <option value="light">Light</option>
                  </select>
                </label>
              </div>
            )}
          </div>
          <button
            className={`btn${showProfile ? ' btn-primary' : ''}`}
            onClick={() => setShowProfile(v => !v)}
            title="Show what is in the data: which fields exist, how often they are populated, and their most common values"
          >
            Fields
          </button>
          <button
            className="btn"
            onClick={() => setRuleSample('')}
            title="Build a parser rule for a log format kopusha does not recognize yet"
          >
            Parser rules
          </button>
          <button
            className="btn btn-primary"
            onClick={() => setShowExport(true)}
            title="Write the current result to a file — NDJSON or Parquet"
          >
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
        <div className="top-bar-sep" />
        <label
          className="check-inline"
          title="Primary sort/time field. ObservedTimestamp falls back to @timestamp if not present in the loaded files."
        >
          Sort:
          <select value={sortField} onChange={(e) => setSortField(e.target.value)}>
            <option value="@timestamp">@timestamp</option>
            <option value="ObservedTimestamp">ObservedTimestamp</option>
          </select>
        </label>
        <div className="top-bar-sep" />
        {/* Both of these change what the query returns, so they belong
            beside the filters rather than in the header with the display
            preferences they used to sit among. */}
        <label className="check-inline" title="Hide null and empty fields when a row is expanded">
          <input
            type="checkbox"
            checked={hideNulls}
            onChange={(e) => setHideNulls(e.target.checked)}
            aria-label="Hide null and empty fields"
          />
          Hide null
        </label>
        <label className="check-inline" title="Re-run the query automatically when filters, time or search change">
          <input
            type="checkbox"
            checked={autoFilter}
            onChange={(e) => setAutoFilter(e.target.checked)}
            aria-label="Automatically re-run the query"
          />
          Auto Apply
        </label>
        <div className="top-bar-sep" />
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

        {filesEnabled && (
          <TimeHistogram
            data={histogramQ.data}
            timezone={timezone}
            hourFormat={hourFormat}
            loading={histogramQ.isFetching}
            onRangeSelect={handleHistogramRange}
          />
        )}

        {/* The profile sits beside the results rather than over them:
            it is read while looking at the rows it describes, and a
            panel that covered them would break the loop it exists to
            support. */}
        <div className="main-split">
          <div className="main-results">
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
                hiddenColumns={hiddenColumns}
                onHiddenColumnsChange={setHiddenColumns}
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

          {showProfile && filesEnabled && (
            <FieldProfile
              profile={profileQ.data}
              loading={profileQ.isFetching}
              queryParams={queryParams}
              onAddFilter={handleProfileFilter}
              onClose={() => setShowProfile(false)}
            />
          )}
        </div>
      </div>

      <div className="status-bar">
        <span>
          {enabledCount}/{files.length} files active &middot;{' '}
          {totalRecords.toLocaleString()} records
          {timestampField ? ` · ts: ${timestampField}` : ''}
        </span>
        <span style={{ display: 'flex', alignItems: 'center', gap: '12px' }}>
          {modules.length > 0 && (
            <span title="Enabled modules (from kopusha.conf sections)" style={{ color: 'var(--text-secondary)' }}>
              modules: {modules.map(m => m.id).join(', ')}
            </span>
          )}
          {updateQ.data?.available && (
            <a
              href={updateQ.data.url}
              target="_blank"
              rel="noopener noreferrer"
              className="status-update"
              title={`You are running ${updateQ.data.current}. Opens the release page. To install it, use the Update button in the notice.`}
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
            title="Stop the kopusha server immediately"
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

      <UpdateNotice status={updateQ.data} />

      {showBrowser && (
        <FileBrowser
          onClose={() => setShowBrowser(false)}
          onFileLoaded={handleFileLoaded}
          initialPath={lastDirectory}
          onDirectoryChanged={setLastDirectory}
          onBuildRule={(sample) => setRuleSample(sample || '')}
        />
      )}

      {ruleSample !== null && (
        <RuleBuilder
          initialSample={ruleSample}
          onClose={() => setRuleSample(null)}
          onSaved={() => setNotice('Parser rule saved. Load the file again to parse it with the new rule.')}
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
