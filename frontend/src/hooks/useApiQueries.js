// Centralised TanStack Query hooks for the core obs-viewer API surface.
// Components import these instead of calling `api.*` directly when they
// need cache participation (results, files, fields, time range, version,
// modules, settings). One-shot mutations (load/unload/toggle) still call
// `api.*` and use `queryClient.invalidateQueries` to refresh.
import { useQuery } from '@tanstack/react-query';
import { api } from '../api/client';

export const qk = {
  version: ['version'],
  modules: ['modules'],
  settings: ['settings'],
  files: ['files'],
  fields: ['fields'],
  timeRange: ['timeRange'],
  query: (params) => ['query', params],
  histogram: (params) => ['histogram', params],
  profile: (params) => ['profile', params],
  update: ['update'],
};

// Adaptive cadence: 30s while healthy, 5s while broken. TanStack's
// refetchInterval callback receives the Query object; we look at its
// most-recent error state to decide.
export function useVersion() {
  return useQuery({
    queryKey: qk.version,
    queryFn: api.getVersion,
    refetchInterval: (query) => (query.state.error ? 5000 : 30000),
    refetchIntervalInBackground: true,
    staleTime: 0,
    retry: 0,
  });
}

// Release notification. The backend answers from a cached background
// check, so this is a cheap local read — but it can report "not checked
// yet" for the first few seconds after start, hence a retry and a slow
// refetch rather than a single fire-and-forget fetch.
export function useUpdateStatus() {
  return useQuery({
    queryKey: qk.update,
    queryFn: async () => {
      const r = await fetch('/api/update');
      if (!r.ok) return { enabled: false, checked: false, available: false };
      return r.json();
    },
    refetchInterval: (query) => (query.state.data?.checked ? false : 10000),
    staleTime: 60000,
    retry: 0,
  });
}

export function useModules() {
  return useQuery({
    queryKey: qk.modules,
    queryFn: async () => {
      const r = await fetch('/api/modules');
      if (!r.ok) return { modules: [] };
      return r.json();
    },
    staleTime: Infinity,
  });
}

export function useSettings() {
  return useQuery({
    queryKey: qk.settings,
    queryFn: api.getSettings,
    staleTime: Infinity,
  });
}

export function useFiles() {
  return useQuery({
    queryKey: qk.files,
    queryFn: api.getFiles,
    staleTime: 0,
  });
}

export function useFields(enabled) {
  return useQuery({
    queryKey: qk.fields,
    queryFn: api.getFields,
    enabled: !!enabled,
  });
}

export function useTimeRange(enabled) {
  return useQuery({
    queryKey: qk.timeRange,
    queryFn: api.getTimeRange,
    enabled: !!enabled,
  });
}

// Query results — the headline benefit. staleTime: 5min from the global
// default means paging back/forward over the same filter set hits cache.
// `enabled` gates execution while no files are selected.
export function useLogQuery(params, enabled) {
  return useQuery({
    queryKey: qk.query(params),
    queryFn: () => api.query(params),
    enabled: !!enabled,
    placeholderData: (previous) => previous,
  });
}

// Field profile for the same filter set.
//
// `enabled` is the panel's open state, so nothing is computed until the
// operator asks for it — this is a full scan with two aggregates per
// column, which is cheap enough on demand and wasteful on every query.
// While the panel stays open it tracks the filters, because a profile
// that described a result the operator had already narrowed away would
// be worse than none.
export function useProfile(params, enabled) {
  const { offset, limit, ...rest } = params;
  return useQuery({
    queryKey: qk.profile(rest),
    queryFn: () => api.profile(rest),
    enabled: !!enabled,
    placeholderData: (previous) => previous,
    staleTime: 30000,
    retry: 0,
  });
}

// Counts over time for the same filter set. Keyed without offset or
// limit: the strip describes the whole result, so paging through it
// must not re-run the aggregate or make the bars jump.
//
// The backend answers with an empty bucket list rather than an error
// when there is no timestamp column, so a failure here only ever
// removes the strip — it never disturbs the table.
export function useHistogram(params, enabled) {
  const { offset, limit, ...rest } = params;
  return useQuery({
    queryKey: qk.histogram(rest),
    queryFn: () => api.histogram(rest),
    enabled: !!enabled,
    placeholderData: (previous) => previous,
    retry: 0,
  });
}
