package server

import (
	"archive/zip"
	"context"
	"crypto/tls"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labmk/kopusha/internal/engine"
	"github.com/labmk/kopusha/internal/logx"
	"github.com/labmk/kopusha/internal/parsers"
	"github.com/labmk/kopusha/internal/settings"
	"github.com/labmk/kopusha/internal/update"

	// Side-effect import: registers the generated swagger spec with the
	// swag runtime so /api/openapi.json can hand it out.
	_ "github.com/labmk/kopusha/internal/server/docs"
	"github.com/swaggo/swag"
)

// zipVirtSep separates zip container from entry in a virtual path.
// e.g. "C:\exports\foo.zip|metrics.ndjson". `|` is illegal in Windows
// filenames and unlikely in Linux NDJSON names.
const zipVirtSep = "|"

// isZipVirtPath reports whether path addresses an entry inside a zip.
func isZipVirtPath(p string) bool {
	return strings.Contains(p, zipVirtSep) && strings.Contains(strings.ToLower(p), ".zip"+zipVirtSep)
}

// splitZipVirt returns (zipPath, innerName) for a virtual zip path.
func splitZipVirt(p string) (string, string) {
	i := strings.Index(p, zipVirtSep)
	if i < 0 {
		return p, ""
	}
	return p[:i], p[i+1:]
}

// ingestableExts is the set of file extensions the file-browser
// surfaces in non-zip directories. It mirrors the formats the ingest
// dispatcher in internal/ingest/ knows how to route:
//
//	.ndjson          → ndjson direct path
//	.parquet .pq     → parquet adapter (also what Export writes)
//	.zip             → browsed as a virtual directory
//	.evtx            → evtx adapter
//	.xml             → xml adapter (autodetected row element)
//	.log .txt .out   → line / block rule adapters
//	.csv             → reserved for a future CSV adapter
//
// Anything else (binaries, archives other than zip, images) is hidden
// even though the dispatcher might still try if you point --files at
// it directly. The browser is conservative on purpose — a folder full
// of irrelevant files becomes unusable otherwise.
//
// This list has to keep pace with the adapters. Parquet was readable
// for a release before it appeared here, which meant an exported file
// could not be browsed back to — the capability existed and was
// unreachable.
var ingestableExts = map[string]struct{}{
	".ndjson":  {},
	".parquet": {},
	".pq":      {},
	".zip":     {},
	".evtx":    {},
	".xml":     {},
	".log":     {},
	".txt":     {},
	".out":     {},
	".csv":     {},
	".json":    {},
}

func isIngestableExt(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	if ext == "" {
		return false
	}
	_, ok := ingestableExts[ext]
	return ok
}

// zipBaseName returns the last component of an entry name inside a
// zip archive. Spec says forward slashes; in practice Windows tools
// (and several log-collector archivers) embed entries with
// backslashes too. Handle both so the basename is the same regardless
// of the host platform.
func zipBaseName(inner string) string {
	for i := len(inner) - 1; i >= 0; i-- {
		if inner[i] == '/' || inner[i] == '\\' {
			return inner[i+1:]
		}
	}
	return inner
}

// Zip-extraction safety caps. Per-entry guards against a single
// crafted file pretending to be small in its header but expanding to
// fill the disk. Aggregate guards against zip-bombs that distribute
// the payload across many small-looking entries (a 1 MiB zip can
// expand to many GiB under DEFLATE). Tune via env if needed; the
// defaults are intentionally generous so legitimate 1 GiB+ EVTX
// files still extract.
const (
	defaultZipEntryCap = 2 << 30  // 2 GiB per file
	defaultZipTotalCap = 10 << 30 // 10 GiB across all files in one load-all
)

// zipDestDir computes <zipdir>/<zipstem>/ for cached extractions and
// makes sure it exists. Returns the path and its absolute form (the
// absolute form is the basis for the path-traversal containment check
// in extractZipFile).
func zipDestDir(zipPath string) (destDir, absDest string, err error) {
	stem := strings.TrimSuffix(filepath.Base(zipPath), filepath.Ext(zipPath))
	destDir = filepath.Join(filepath.Dir(zipPath), stem)
	if err = os.MkdirAll(destDir, 0o755); err != nil {
		return "", "", err
	}
	absDest, err = filepath.Abs(destDir)
	return
}

// extractZipFile extracts one *zip.File into destDir/<basename>. The
// caller must supply destDir and its absolute form (from zipDestDir),
// plus a running total pointer for aggregate-cap enforcement across
// repeated calls (loadAllFromZip iterates).
//
// Containment is checked using `absTarget == absDest ||
// strings.HasPrefix(absTarget, absDest+filepath.Separator)` — plain
// HasPrefix without the separator would accept /tmp/foo-evil as inside
// /tmp/foo. Caps are enforced twice: declared size vs perEntryCap
// before opening (cheap rejection), then io.LimitReader during the
// actual copy (defense against a header that lies about size).
func extractZipFile(f *zip.File, destDir, absDest string, totalExtracted *int64, perEntryCap, aggCap int64) (string, error) {
	declared := int64(f.UncompressedSize64)
	if declared > perEntryCap {
		return "", fmt.Errorf("entry %q declared size %d > per-entry cap %d", f.Name, declared, perEntryCap)
	}
	if totalExtracted != nil && *totalExtracted+declared > aggCap {
		return "", fmt.Errorf("entry %q would exceed aggregate cap (%d/%d)", f.Name, *totalExtracted+declared, aggCap)
	}

	target := filepath.Join(destDir, zipBaseName(f.Name))
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("abs target: %w", err)
	}
	if absTarget != absDest && !strings.HasPrefix(absTarget, absDest+string(filepath.Separator)) {
		return "", fmt.Errorf("refusing unsafe path in zip: %q", f.Name)
	}

	// Fast path: if already extracted with the declared size, reuse.
	// (Imperfect: trusts the header. Acceptable since the cap above
	// already bounds it, and the cache is per-user-machine.)
	if fi, err := os.Stat(target); err == nil && fi.Size() == declared {
		if totalExtracted != nil {
			*totalExtracted += declared
		}
		return target, nil
	}

	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	dst, err := os.Create(target)
	if err != nil {
		return "", err
	}
	defer dst.Close()

	// LimitReader at perEntryCap+1 so we can detect a lying header:
	// if more than perEntryCap bytes were read, the header understated
	// the size and we reject.
	limited := io.LimitReader(rc, perEntryCap+1)
	n, err := io.Copy(dst, limited)
	if err != nil {
		_ = os.Remove(target)
		return "", err
	}
	if n > perEntryCap {
		_ = os.Remove(target)
		return "", fmt.Errorf("entry %q exceeded per-entry cap during decompression (header lied)", f.Name)
	}
	if totalExtracted != nil {
		*totalExtracted += n
	}
	return target, nil
}

// extractZipEntry is the single-entry convenience wrapper used by
// /api/files/load — one inner file at a time, no aggregate accounting
// needed.
func extractZipEntry(zipPath, inner string) (string, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer zr.Close()
	destDir, absDest, err := zipDestDir(zipPath)
	if err != nil {
		return "", err
	}
	for _, f := range zr.File {
		if f.Name != inner {
			continue
		}
		return extractZipFile(f, destDir, absDest, nil, defaultZipEntryCap, defaultZipTotalCap)
	}
	return "", fmt.Errorf("entry %q not in %s", inner, zipPath)
}

// Server is the HTTP server for kopusha.
type Server struct {
	eng            *engine.Engine
	mux            *http.ServeMux
	static         embed.FS
	version        string
	settings       *settings.Store
	lastActivity   time.Time
	activityMu     sync.Mutex
	idleTimeoutSec int // 0 = no auto-shutdown; surfaced via /api/version

	// busyChecks are predicates registered by modules through
	// AddBusyCheck. The inactivity-timeout loop in main.go calls
	// IsBusy() to skip auto-shutdown while any module is mid-work
	// (e.g. holding an open connection to a remote host).
	busyMu     sync.Mutex
	busyChecks []func() bool

	// updates reports whether a newer release exists. Nil when the
	// check is disabled, in which case /api/update says so rather than
	// implying "up to date" from an absence of information.
	updates *update.Checker

	// samplesDir is the shipped samples/ folder, or empty when this
	// install has none.
	samplesDir string

	// update holds the self-update endpoints' state. Zero value is
	// usable and reports that updating is unavailable.
	update updateState

	// rules owns parsers.d — the loader registry, and the write path
	// for rules authored in the UI. See rules.go.
	rules *parsers.Manager
}

// SetUpdateChecker attaches the release-notification checker. Called
// once from main after New, for the same reason as
// SetIdleTimeoutSeconds: the setting lives in kopusha.conf.
func (s *Server) SetUpdateChecker(c *update.Checker) { s.updates = c }

// New creates a new Server.
func New(eng *engine.Engine, staticFS embed.FS, version string, store *settings.Store) *Server {
	s := &Server{
		eng:          eng,
		mux:          http.NewServeMux(),
		static:       staticFS,
		version:      version,
		settings:     store,
		lastActivity: time.Now(),
	}
	s.registerRoutes()
	return s
}

// SetIdleTimeoutSeconds records the configured inactivity timeout so
// /api/version can surface it to the SPA. Pass 0 to indicate no
// auto-shutdown is configured. Called once from main after Server.New
// because the timeout lives in kopusha.conf, not in the server
// constructor's arguments.
func (s *Server) SetIdleTimeoutSeconds(sec int) { s.idleTimeoutSec = sec }

// IsBusy reports whether any registered module is mid-work — replaces
// the previous RemoteConnected() guard. main.go's inactivity-timeout
// loop consults this before auto-shutting-down.
func (s *Server) IsBusy() bool {
	s.busyMu.Lock()
	defer s.busyMu.Unlock()
	for _, f := range s.busyChecks {
		if f() {
			return true
		}
	}
	return false
}

// AddBusyCheck registers a "is this module busy?" predicate. Wired into
// the module registry's RegisterContext.Deps so modules call it once at
// boot.
func (s *Server) AddBusyCheck(f func() bool) {
	s.busyMu.Lock()
	defer s.busyMu.Unlock()
	s.busyChecks = append(s.busyChecks, f)
}

// Mux exposes the underlying ServeMux so the module registry can mount
// optional modules' routes onto the same listener used by core routes.
// Module paths live under /api/<name>/* and /m/<name>/* — disjoint from
// core paths, so longest-prefix matching keeps them separate from the
// "/" SPA catch-all.
func (s *Server) Mux() *http.ServeMux { return s.mux }

// TouchActivity records the current time as the last API activity.
// Exposed for modules that need to ping activity from inside a
// long-running handler (e.g. SSE loops) — wired through the module
// registry as Deps.TouchActivity.
func (s *Server) TouchActivity() { s.touchActivity() }

// touchActivity records the current time as the last API activity.
func (s *Server) touchActivity() {
	s.activityMu.Lock()
	s.lastActivity = time.Now()
	s.activityMu.Unlock()
}

// LastActivity returns the time of the most recent API request.
func (s *Server) LastActivity() time.Time {
	s.activityMu.Lock()
	defer s.activityMu.Unlock()
	return s.lastActivity
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe(addr string) error {
	return http.ListenAndServe(addr, s.mux)
}

// ListenAndServeTLS starts the HTTPS server.
func (s *Server) ListenAndServeTLS(addr, certFile, keyFile string) error {
	srv := &http.Server{
		Addr:    addr,
		Handler: s.mux,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
	return srv.ListenAndServeTLS(certFile, keyFile)
}

// APIHandler wraps an HTTP handler to track activity for the
// inactivity-timeout. Exposed so modules can wrap their own handlers
// when registering routes through the module registry.
func (s *Server) APIHandler(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.touchActivity()
		handler(w, r)
	}
}

func (s *Server) registerRoutes() {
	// API routes (wrapped with activity tracking)
	s.mux.HandleFunc("/api/version", s.APIHandler(s.handleVersion))
	s.mux.HandleFunc("/api/files", s.APIHandler(s.handleFiles))
	s.mux.HandleFunc("/api/files/load", s.APIHandler(s.handleLoadFile))
	s.mux.HandleFunc("/api/files/unload", s.APIHandler(s.handleUnloadFile))
	s.mux.HandleFunc("/api/files/toggle", s.APIHandler(s.handleToggleFile))
	s.mux.HandleFunc("/api/browse", s.APIHandler(s.handleBrowse))
	s.mux.HandleFunc("/api/query", s.APIHandler(s.handleQuery))
	s.mux.HandleFunc("/api/histogram", s.APIHandler(s.handleHistogram))
	s.mux.HandleFunc("/api/profile", s.APIHandler(s.handleProfile))
	s.mux.HandleFunc("/api/profile/values", s.APIHandler(s.handleFieldValues))
	s.mux.HandleFunc("/api/fields", s.APIHandler(s.handleFields))
	s.mux.HandleFunc("/api/timerange", s.APIHandler(s.handleTimeRange))
	s.mux.HandleFunc("/api/timestamp-field", s.APIHandler(s.handleTimestampField))
	s.mux.HandleFunc("/api/export", s.APIHandler(s.handleExport))
	s.mux.HandleFunc("/api/export/self-copy", s.APIHandler(s.handleSelfCopy))
	s.mux.HandleFunc("/api/settings", s.APIHandler(s.handleSettings))
	s.mux.HandleFunc("/api/files/load-dir", s.APIHandler(s.handleLoadDir))
	s.mux.HandleFunc("/api/openapi.json", s.handleOpenAPISpec)
	s.mux.HandleFunc("/api/field-samples", s.APIHandler(s.handleFieldSamples))
	s.mux.HandleFunc("/api/update", s.APIHandler(s.handleUpdate))
	s.mux.HandleFunc("/api/shutdown", s.handleShutdown)

	// Parser-rule routes: /api/files/explain and /api/rules/* — see
	// rules.go.
	s.registerRuleRoutes()
	s.registerUpdateRoutes()
	s.mux.HandleFunc("/api/samples", s.APIHandler(s.handleSamples))

	// Module routes (/api/<name>/*, /m/<name>/*) are mounted by the
	// module registry at boot — see internal/module and docs/MODULES.md.

	// Static files (embedded frontend)
	staticSub, err := fs.Sub(s.static, "static")
	if err != nil {
		log.Fatal("Failed to create static sub-filesystem:", err)
	}
	fileServer := http.FileServer(http.FS(staticSub))

	// SPA fallback: serve index.html for all non-API, non-file routes
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Try to serve static file first
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}
		f, err := staticSub.Open(strings.TrimPrefix(path, "/"))
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA fallback
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}

// --- API Handlers ---

// handleShutdown is called by two paths:
//   - explicit operator click on the "Stop server" status-bar button
//   - browser tab close via navigator.sendBeacon('/api/shutdown') in
//     App.jsx's pagehide listener
//
// Either way we exit the process after a short grace period. The grace
// matters for the beacon case: closing one tab when another is still
// open shouldn't kill the server. The grace gives the other tab's
// 30s healthcheck enough headroom to land an /api/version touch — if
// any activity happens during the grace, we cancel.
//
// Not wrapped in APIHandler (no activity touch) — the whole point is
// to leave, not to extend the timer.
func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	const grace = 2 * time.Second
	writeJSON(w, map[string]interface{}{
		"status":        "shutting_down",
		"grace_seconds": int(grace / time.Second),
	})
	// Flush response before we sleep — sendBeacon doesn't wait for a
	// response anyway but the operator-clicked path expects one.
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	asOf := s.LastActivity()
	go func() {
		time.Sleep(grace)
		// Cancellation check: if another tab pinged /api/version during
		// the grace, LastActivity moved forward — abort the shutdown.
		if s.LastActivity().After(asOf) {
			log.Printf("Shutdown cancelled — activity detected during grace period")
			return
		}
		if s.IsBusy() {
			log.Printf("Shutdown cancelled — a module is busy")
			return
		}
		log.Printf("Shutdown requested via /api/shutdown")
		os.Exit(0)
	}()
}

// handleOpenAPISpec serves the swagger 2.0 spec that swag generated at
// build time. Not annotated itself so it doesn't end up in the spec.
func (s *Server) handleOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	doc, err := swag.ReadDoc()
	if err != nil {
		writeError(w, "openapi spec not registered", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(doc))
}

// @Summary      Server version + platform info
// @Description  Lightweight liveness endpoint. The frontend uses this to drive an adaptive 30s / 5s healthcheck.
// @Tags         core
// @Produce      json
// @Success      200  {object}  VersionResponse
// @Router       /api/version [get]
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]interface{}{
		"version":              s.version,
		"os":                   runtime.GOOS,
		"arch":                 runtime.GOARCH,
		"idle_timeout_seconds": s.idleTimeoutSec,
		"repository":           update.RepoURL,
	})
}

// @Summary      Release update status
// @Description  Reports whether a newer release exists. Read-only — kopusha never downloads or installs anything. Returns enabled=false when the check is switched off, which the UI must distinguish from "up to date".
// @Tags         system
// @Produce      json
// @Success      200  {object}  UpdateResponse
// @Router       /api/update [get]
func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if s.updates == nil {
		writeJSON(w, update.Status{Current: s.version, Enabled: false})
		return
	}
	writeJSON(w, s.updates.Status())
}

// @Summary      List loaded files
// @Description  Returns every file the engine currently has loaded plus the auto-detected timestamp column.
// @Tags         files
// @Produce      json
// @Success      200  {object}  FilesResponse
// @Router       /api/files [get]
func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	files := s.eng.GetFiles()
	writeJSON(w, map[string]interface{}{
		"files":           files,
		"timestamp_field": s.eng.GetTimestampField(),
	})
}

// @Summary      Load a file
// @Description  Loads a single file by absolute path. Supports virtual zip paths in the form `C:\foo.zip|inner.ndjson` — the inner entry is extracted next to the zip and then ingested. Dispatches through the ingest layer so non-NDJSON formats (EVTX, XML, block, line) are handled too.
// @Tags         files
// @Accept       json
// @Produce      json
// @Param        body  body      PathRequest     true  "path to load"
// @Success      200   {object}  StatusResponse
// @Failure      400   {object}  ErrorResponse
// @Router       /api/files/load [post]
func (s *Server) handleLoadFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.Path == "" {
		writeError(w, "Path is required", http.StatusBadRequest)
		return
	}

	// Virtual zip path: extract the inner .ndjson next to the zip
	// (in <zipdir>/<zipstem>/<innerBase>) and load that real file.
	// Previously extracted entries are reused.
	loadPath := req.Path
	if isZipVirtPath(req.Path) {
		zipPath, inner := splitZipVirt(req.Path)
		extracted, err := extractZipEntry(zipPath, inner)
		if err != nil {
			writeError(w, fmt.Sprintf("extract: %v", err), http.StatusBadRequest)
			return
		}
		loadPath = extracted
	}

	if err := s.eng.LoadFileCtx(r.Context(), loadPath); err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Remember directory for next run — use the real extracted file
	// when coming from a zip, otherwise the original request path.
	absPath, _ := filepath.Abs(loadPath)
	_ = s.settings.Update(func(st *settings.Settings) {
		st.LastDirectory = filepath.Dir(absPath)
	})

	writeJSON(w, map[string]string{"status": "ok"})
}

// @Summary      Unload a file
// @Tags         files
// @Accept       json
// @Produce      json
// @Param        body  body      IDRequest       true  "file id"
// @Success      200   {object}  StatusResponse
// @Failure      400   {object}  ErrorResponse
// @Router       /api/files/unload [post]
func (s *Server) handleUnloadFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if err := s.eng.UnloadFile(req.ID); err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// @Summary      Enable / disable a file
// @Description  Toggles whether a loaded file participates in subsequent queries. Cheaper than unload+load — the table stays in DuckDB, just excluded from UNION ALL.
// @Tags         files
// @Accept       json
// @Produce      json
// @Param        body  body      ToggleRequest   true  "file id + enabled flag"
// @Success      200   {object}  StatusResponse
// @Failure      400   {object}  ErrorResponse
// @Router       /api/files/toggle [post]
func (s *Server) handleToggleFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if err := s.eng.SetFileEnabled(req.ID, req.Enabled); err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// @Summary      Backend file browser
// @Description  Lists directory contents (or zip-archive contents when the path points at a .zip file). Browsers cannot access the local filesystem; this endpoint is how the file picker enumerates folders.
// @Tags         files
// @Produce      json
// @Param        path  query     string  false  "absolute path to list; defaults to the user home directory"
// @Success      200   {object}  BrowseResponse
// @Failure      400   {object}  ErrorResponse
// @Router       /api/browse [get]
func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	dirPath := r.URL.Query().Get("path")
	if dirPath == "" {
		// Default to user home or current directory
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		dirPath = home
	}

	// Zip-as-directory: when path points at a .zip file, list its
	// entries so the operator can browse in and select some or all of
	// them. Virtual paths use `<zip>|<inner>`; load-file extracts them
	// on demand. All non-directory entries are listed — the ingest
	// layer (NDJSON, EVTX, XML, block, line) decides at LoadFile time
	// whether the format is supported.
	if lower := strings.ToLower(dirPath); strings.HasSuffix(lower, ".zip") {
		if fi, statErr := os.Stat(dirPath); statErr == nil && !fi.IsDir() {
			zr, zerr := zip.OpenReader(dirPath)
			if zerr != nil {
				writeError(w, fmt.Sprintf("Cannot open zip: %v", zerr), http.StatusBadRequest)
				return
			}
			defer zr.Close()
			type entryOut struct {
				Name  string `json:"name"`
				Path  string `json:"path"`
				IsDir bool   `json:"is_dir"`
				Size  int64  `json:"size"`
			}
			parent := filepath.Dir(dirPath)
			out := []entryOut{{Name: "..", Path: parent, IsDir: true}}
			for _, f := range zr.File {
				if f.FileInfo().IsDir() {
					continue
				}
				out = append(out, entryOut{
					Name:  zipBaseName(f.Name),
					Path:  dirPath + zipVirtSep + f.Name,
					IsDir: false,
					Size:  int64(f.UncompressedSize64),
				})
			}
			var drives []string
			if runtime.GOOS == "windows" {
				for d := 'C'; d <= 'Z'; d++ {
					root := string(d) + `:\`
					if _, err := os.Stat(root); err == nil {
						drives = append(drives, root)
					}
				}
			}
			writeJSON(w, map[string]interface{}{
				"current_path": dirPath,
				"entries":      out,
				"drives":       drives,
				"in_zip":       true,
			})
			return
		}
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		// On Windows, fall back to C:\ when the requested path is
		// missing (e.g. a stale last_directory on a OneDrive/VPN path).
		// Other OSes keep the original error.
		if runtime.GOOS == "windows" {
			fallback := `C:\`
			if entries2, err2 := os.ReadDir(fallback); err2 == nil {
				dirPath = fallback
				entries = entries2
			} else {
				writeError(w, fmt.Sprintf("Cannot read directory: %v", err), http.StatusBadRequest)
				return
			}
		} else {
			writeError(w, fmt.Sprintf("Cannot read directory: %v", err), http.StatusBadRequest)
			return
		}
	}

	type Entry struct {
		Name  string `json:"name"`
		Path  string `json:"path"`
		IsDir bool   `json:"is_dir"`
		Size  int64  `json:"size"`
	}

	var result []Entry

	// Add parent directory entry
	parentPath := filepath.Dir(dirPath)
	if parentPath != dirPath {
		result = append(result, Entry{
			Name:  "..",
			Path:  parentPath,
			IsDir: true,
		})
	}

	for _, e := range entries {
		// Skip hidden files
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}

		info, err := e.Info()
		if err != nil {
			continue
		}

		// Show directories, zip archives, and any file whose extension the
		// ingest layer (NDJSON, EVTX, XML, block/line text logs) can
		// handle. The dispatcher does final detection at load time by
		// sniffing the first 512 bytes; the extension filter here is
		// purely "what should the picker offer". Previously this was
		// hard-coded to .ndjson + .zip, which made extracted-zip
		// payloads (EVTX, XML, .log, .txt) invisible to the operator
		// even though the engine could load them.
		if !e.IsDir() && !isIngestableExt(e.Name()) {
			continue
		}

		var size int64
		if !e.IsDir() {
			size = info.Size()
		}

		result = append(result, Entry{
			Name:  e.Name(),
			Path:  filepath.Join(dirPath, e.Name()),
			IsDir: e.IsDir(),
			Size:  size,
		})
	}

	// On Windows, always advertise available drive letters so the
	// frontend can render a drive picker regardless of where we are.
	var drives []string
	if runtime.GOOS == "windows" {
		for d := 'C'; d <= 'Z'; d++ {
			root := string(d) + `:\`
			if _, err := os.Stat(root); err == nil {
				drives = append(drives, root)
			}
		}
	}

	writeJSON(w, map[string]interface{}{
		"current_path": dirPath,
		"entries":      result,
		"drives":       drives,
	})
}

// @Summary      Query the loaded files
// @Description  Runs the assembled filter set (field clauses, time window, optional free-text search) against the UNION ALL of enabled files. Sort, offset, and limit drive paging. Free-text search uses DuckDB ILIKE on a TRY_CAST-VARCHAR'd row.
// @Tags         query
// @Accept       json
// @Produce      json
// @Param        body  body      QueryRequest    true  "query parameters"
// @Success      200   {object}  QueryResponse
// @Failure      400   {object}  ErrorResponse
// @Failure      500   {object}  ErrorResponse
// @Router       /api/query [post]
func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req engine.QueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	result, err := s.eng.Query(req)
	if err != nil {
		logx.Error("api.query", logx.F{
			"error":       err.Error(),
			"filters":     req.Filters,
			"time_from":   req.TimeFrom,
			"time_to":     req.TimeTo,
			"sort_order":  req.SortOrder,
			"search_text": req.SearchText,
			"offset":      req.Offset,
			"limit":       req.Limit,
			"remote_addr": r.RemoteAddr,
		})
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, result)
}

// @Summary      Record counts over time for the current filter
// @Description  Same filter set as /api/query, aggregated into time buckets. The bucket width is derived from the span of the filtered data so the bar count stays bounded, and is reported back as interval_seconds. Returns an empty buckets array — never an error — when no timestamp column is detected or nothing matches, so a missing histogram can never be the reason a query fails.
// @Tags         query
// @Accept       json
// @Produce      json
// @Param        body  body      QueryRequest       true  "same shape as /api/query; offset and limit are ignored"
// @Success      200   {object}  HistogramResponse
// @Failure      400   {object}  ErrorResponse
// @Router       /api/histogram [post]
func (s *Server) handleHistogram(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req engine.QueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	h, err := s.eng.GetHistogram(req)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, h)
}

// @Summary      Profile every field over the current result
// @Description  Per field: how many rows carry a usable value, an approximate distinct count, and how many of the enabled files declare the field at all. Uses the same filters as /api/query, so the profile describes the rows on screen. One scan covers every field; value distributions are fetched separately per field via /api/profile/values.
// @Tags         query
// @Accept       json
// @Produce      json
// @Param        body  body      ProfileRequest    true  "same filters as /api/query, plus an optional field subset"
// @Success      200   {object}  ProfileResponse
// @Failure      400   {object}  ErrorResponse
// @Failure      500   {object}  ErrorResponse
// @Router       /api/profile [post]
func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		engine.QueryRequest
		Fields []string `json:"fields"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	p, err := s.eng.GetProfile(req.QueryRequest, req.Fields)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, p)
}

// @Summary      Most common values for one field
// @Description  A field's top values with counts, over the rows the current filters select. NULL and empty are excluded — the profile already reports the fill rate, and repeating it here would crowd out the values that carry information. Fetched per field, on demand, because each one needs its own GROUP BY.
// @Tags         query
// @Accept       json
// @Produce      json
// @Param        body  body      FieldValuesRequest  true  "same filters as /api/query, plus the field"
// @Success      200   {object}  FieldValuesResponse
// @Failure      400   {object}  ErrorResponse
// @Failure      500   {object}  ErrorResponse
// @Router       /api/profile/values [post]
func (s *Server) handleFieldValues(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		engine.QueryRequest
		Field string `json:"field"`
		Top   int    `json:"top"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.Field == "" {
		writeError(w, "Field is required", http.StatusBadRequest)
		return
	}
	v, err := s.eng.GetFieldValues(req.QueryRequest, req.Field, req.Top)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, v)
}

// @Summary      Available filter fields
// @Description  Union of column names across every enabled file.
// @Tags         query
// @Produce      json
// @Success      200  {object}  FieldsResponse
// @Failure      500  {object}  ErrorResponse
// @Router       /api/fields [get]
// defaultSampleFields is the field list /api/field-samples returns
// when no `fields` query param is supplied. These are conventional
// low-cardinality classifiers (OpenTelemetry / ECS naming) that are
// useful as a value list in a filter UI. Fields absent from the loaded
// data are simply omitted from the response, so an unrelated schema
// costs nothing. The SPA always passes an explicit ?fields=, so this
// list only serves direct API callers. Override via ?fields=a,b,c.
var defaultSampleFields = []string{
	"service.name",
	"host.name",
	"SeverityText",
	"log.level",
	"event.category",
	"_source_format",
}

// @Summary      Distinct sample values for low-cardinality fields
// @Description  Returns the DISTINCT value set per requested field (default list when `fields` query param is absent). Fields whose distinct count exceeds the cap (30) are returned with an empty array — signal to the caller that the field is too high-cardinality to be useful as a literal value list. Feeds the query builder's value-suggestion datalist.
// @Tags         query
// @Produce      json
// @Param        fields  query     string  false  "comma-separated list of field names; default: service.name,host.name,SeverityText,log.level,event.category,_source_format"
// @Param        cap     query     int     false  "max values per field (default 30)"
// @Success      200     {object}  map[string][]string
// @Failure      500     {object}  ErrorResponse
// @Router       /api/field-samples [get]
func (s *Server) handleFieldSamples(w http.ResponseWriter, r *http.Request) {
	fields := defaultSampleFields
	if q := r.URL.Query().Get("fields"); q != "" {
		fields = nil
		for _, f := range strings.Split(q, ",") {
			f = strings.TrimSpace(f)
			if f != "" {
				fields = append(fields, f)
			}
		}
	}
	cap := 30
	if q := r.URL.Query().Get("cap"); q != "" {
		if v, err := strconv.Atoi(q); err == nil && v > 0 && v <= 500 {
			cap = v
		}
	}
	samples, err := s.eng.FieldSamples(fields, cap)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, samples)
}

func (s *Server) handleFields(w http.ResponseWriter, r *http.Request) {
	fields, err := s.eng.GetFields()
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]interface{}{"fields": fields})
}

// @Summary      Time bounds of loaded data
// @Description  MIN/MAX of the active timestamp column across the UNION ALL, plus the list of timestamp-like columns the engine detected.
// @Tags         query
// @Produce      json
// @Success      200  {object}  TimeRangeResponse
// @Failure      500  {object}  ErrorResponse
// @Router       /api/timerange [get]
func (s *Server) handleTimeRange(w http.ResponseWriter, r *http.Request) {
	tr, err := s.eng.GetTimeRange()
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, tr)
}

// @Summary      Get or set the active timestamp column
// @Description  GET returns the column the engine currently sorts by. POST overrides it; the engine silently falls back to the default if the requested field is absent from the loaded files.
// @Tags         query
// @Accept       json
// @Produce      json
// @Param        body  body      TimestampFieldRequest  false  "POST only: requested column"
// @Success      200   {object}  TimestampFieldResponse
// @Failure      400   {object}  ErrorResponse
// @Router       /api/timestamp-field [get]
// @Router       /api/timestamp-field [post]
func (s *Server) handleTimestampField(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, map[string]string{"field": s.eng.GetTimestampField()})
		return
	}
	if r.Method == http.MethodPost {
		var req struct {
			Field string `json:"field"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		s.eng.SetTimestampField(req.Field)
		writeJSON(w, map[string]string{"status": "ok", "field": req.Field})
		return
	}
	http.Error(w, "GET or POST required", http.StatusMethodNotAllowed)
}

// @Summary      Export filtered rows to NDJSON
// @Description  Reruns the query against DuckDB and writes the result to output_path via COPY ... (FORMAT JSON, ARRAY false).
// @Tags         export
// @Accept       json
// @Produce      json
// @Param        body  body      ExportRequest   true  "query + output path"
// @Success      200   {object}  ExportResponse
// @Failure      400   {object}  ErrorResponse
// @Failure      500   {object}  ErrorResponse
// @Router       /api/export [post]
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Query      engine.QueryRequest `json:"query"`
		OutputPath string              `json:"output_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.OutputPath == "" {
		writeError(w, "output_path is required", http.StatusBadRequest)
		return
	}

	count, err := s.eng.ExportFiltered(req.Query, req.OutputPath)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]interface{}{
		"status":  "ok",
		"records": count,
		"path":    req.OutputPath,
	})
}

// @Summary      Copy the binary alongside exported data
// @Description  F13 — drops the running binary into target_dir so a single zip can carry both data and viewer.
// @Tags         export
// @Accept       json
// @Produce      json
// @Param        body  body      SelfCopyRequest  true  "destination directory"
// @Success      200   {object}  SelfCopyResponse
// @Failure      400   {object}  ErrorResponse
// @Failure      500   {object}  ErrorResponse
// @Router       /api/export/self-copy [post]
func (s *Server) handleSelfCopy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		TargetDir string `json:"target_dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Get path to current executable
	execPath, err := os.Executable()
	if err != nil {
		writeError(w, fmt.Sprintf("Cannot determine executable path: %v", err), http.StatusInternalServerError)
		return
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		writeError(w, fmt.Sprintf("Cannot resolve executable path: %v", err), http.StatusInternalServerError)
		return
	}

	destPath := filepath.Join(req.TargetDir, filepath.Base(execPath))

	// Copy the binary
	src, err := os.ReadFile(execPath)
	if err != nil {
		writeError(w, fmt.Sprintf("Cannot read executable: %v", err), http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(destPath, src, 0755); err != nil {
		writeError(w, fmt.Sprintf("Cannot write executable copy: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]string{
		"status": "ok",
		"path":   destPath,
	})
}

// @Summary      Get or persist user settings
// @Description  Persisted to kopusha_settings.json next to the binary. Only the fields present in the POST body are updated.
// @Tags         core
// @Accept       json
// @Produce      json
// @Param        body  body      SettingsBody    false  "POST only: partial settings patch"
// @Success      200   {object}  SettingsBody
// @Failure      400   {object}  ErrorResponse
// @Failure      500   {object}  ErrorResponse
// @Router       /api/settings [get]
// @Router       /api/settings [post]
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, s.settings.Get())
		return
	}
	if r.Method == http.MethodPost {
		var incoming map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
			writeError(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		if err := s.settings.Update(func(st *settings.Settings) {
			if raw, ok := incoming["last_directory"]; ok {
				var v string
				if json.Unmarshal(raw, &v) == nil && v != "" {
					st.LastDirectory = v
				}
			}
			if raw, ok := incoming["auto_load_previous"]; ok {
				var v bool
				if json.Unmarshal(raw, &v) == nil {
					st.AutoLoadPrevious = v
				}
			}
		}); err != nil {
			writeError(w, fmt.Sprintf("Failed to save settings: %v", err), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
		return
	}
	http.Error(w, "GET or POST required", http.StatusMethodNotAllowed)
}

// @Summary      Load every supported file in a directory or zip
// @Description  Bulk load. Each file is ingested through the dispatch layer; unsupported entries land in `errors` rather than aborting the load.
// @Tags         files
// @Accept       json
// @Produce      json
// @Param        body  body      PathRequest      true  "directory or zip path"
// @Success      200   {object}  LoadDirResponse
// @Failure      400   {object}  ErrorResponse
// @Router       /api/files/load-dir [post]
func (s *Server) handleLoadDir(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.Path == "" {
		writeError(w, "Path is required", http.StatusBadRequest)
		return
	}

	// .zip "directory": extract every non-directory entry next to the
	// zip and feed each one through LoadFile. The ingest dispatcher
	// decides per-file whether it can be handled; unsupported entries
	// land in `errors` rather than blocking the rest.
	if strings.HasSuffix(strings.ToLower(req.Path), ".zip") {
		if fi, statErr := os.Stat(req.Path); statErr == nil && !fi.IsDir() {
			loaded, errors := s.loadAllFromZip(r.Context(), req.Path)
			_ = s.settings.Update(func(st *settings.Settings) {
				st.LastDirectory = filepath.Dir(req.Path)
			})
			writeJSON(w, map[string]interface{}{
				"status": "ok",
				"loaded": loaded,
				"errors": errors,
			})
			return
		}
	}

	entries, err := os.ReadDir(req.Path)
	if err != nil {
		writeError(w, fmt.Sprintf("Cannot read directory: %v", err), http.StatusBadRequest)
		return
	}

	var loaded []string
	var errors []string
	for _, e := range entries {
		if err := r.Context().Err(); err != nil {
			errors = append(errors, fmt.Sprintf("cancelled: %s", err.Error()))
			break
		}
		if e.IsDir() {
			continue
		}
		fullPath := filepath.Join(req.Path, e.Name())
		if err := s.eng.LoadFileCtx(r.Context(), fullPath); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %s", e.Name(), err.Error()))
		} else {
			loaded = append(loaded, e.Name())
		}
	}

	// Save directory as last used
	_ = s.settings.Update(func(st *settings.Settings) {
		st.LastDirectory = req.Path
	})

	writeJSON(w, map[string]interface{}{
		"status": "ok",
		"loaded": loaded,
		"errors": errors,
	})
}

// loadAllFromZip extracts every non-directory entry in zipPath next to
// the zip and runs LoadFile on each. Returns parallel slices of
// basenames that loaded vs. error strings — same shape handleLoadDir
// uses for directory inputs.
//
// Opens the zip exactly once and reuses both the reader and the
// destDir resolution across all entries. A previous implementation
// called extractZipEntry per entry, which re-opened the zip and
// re-walked its central directory on every iteration — O(N²) for an
// N-entry archive (1k entries → ~1k directory scans).
//
// Aggregate size cap is enforced via a running total so a 10k-entry
// archive can't quietly dump 100 GiB even if each individual entry is
// under the per-entry cap.
func (s *Server) loadAllFromZip(ctx context.Context, zipPath string) ([]string, []string) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, []string{fmt.Sprintf("open zip: %v", err)}
	}
	defer zr.Close()
	destDir, absDest, err := zipDestDir(zipPath)
	if err != nil {
		return nil, []string{fmt.Sprintf("dest dir: %v", err)}
	}

	var loaded, errors []string
	var totalExtracted int64
	for _, f := range zr.File {
		if err := ctx.Err(); err != nil {
			errors = append(errors, fmt.Sprintf("cancelled: %v", err))
			break
		}
		if f.FileInfo().IsDir() {
			continue
		}
		name := zipBaseName(f.Name)
		extracted, eerr := extractZipFile(f, destDir, absDest, &totalExtracted, defaultZipEntryCap, defaultZipTotalCap)
		if eerr != nil {
			errors = append(errors, fmt.Sprintf("%s: extract: %v", name, eerr))
			continue
		}
		if lerr := s.eng.LoadFileCtx(ctx, extracted); lerr != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", name, lerr))
			continue
		}
		loaded = append(loaded, name)
	}
	return loaded, errors
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
