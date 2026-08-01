// QueryClient setup for TanStack Query. Single instance, app-wide.
//
// Replaces three pieces of hand-rolled machinery in App.jsx:
//   1. The 20-entry Map LRU around api.query.
//   2. The adaptive 30s/5s healthcheck `setTimeout` chain for /api/version.
//   3. The `executeQueryRef` indirection that worked around `useCallback`
//      identity churn re-firing the auto-query effect on every keystroke.
//
// Tuning notes:
//   - staleTime 5min on /api/query: matches the original LRU's "page back
//     over the same filter set is instant" intent.
//   - gcTime 10min: keeps cached pages around long enough for back/forward
//     navigation but not so long they leak after the user moves on.
//   - retry: 0 — DuckDB query failures are deterministic (SQL/data shape),
//     not transient. Re-running on its own would just hide the error.
//   - refetchOnWindowFocus: false — this is a desktop tool, not a dashboard.
import { QueryClient } from '@tanstack/react-query';

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 5 * 60 * 1000,
      gcTime: 10 * 60 * 1000,
      retry: 0,
      refetchOnWindowFocus: false,
    },
    mutations: {
      retry: 0,
    },
  },
});
