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
