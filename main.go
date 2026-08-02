package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/labmk/obs-viewer/internal/config"
	"github.com/labmk/obs-viewer/internal/engine"
	"github.com/labmk/obs-viewer/internal/logx"
	"github.com/labmk/obs-viewer/internal/manifest"
	"github.com/labmk/obs-viewer/internal/module"
	"github.com/labmk/obs-viewer/internal/parsers"
	"github.com/labmk/obs-viewer/internal/server"
	"github.com/labmk/obs-viewer/internal/settings"
	"github.com/labmk/obs-viewer/internal/update"
)

//go:embed parsers.d.sha256
var parsersManifestData []byte

//go:embed static/*
var staticFS embed.FS

var version = "0.2.1"

// @title           obs-viewer API
// @version         0.2.1
// @description     Local viewer for log, metric and trace files: NDJSON, EVTX,
// @description     XML, and rule-driven text logs, queried through DuckDB.
// @description     The OpenAPI spec is the source of truth for the generated
// @description     TypeScript client used by the React frontend.
// @BasePath        /
// @schemes         http https

func main() {
	// Windows GUI-subsystem binaries (`-H windowsgui`) start without a
	// console, so launching from Explorer doesn't flash a black window.
	// Re-attach to the parent's console (cmd.exe / PowerShell) when one
	// exists so `--version`, `--help`, and runtime log output still
	// reach the operator who launched us from a terminal. No-op on
	// non-Windows builds.
	attachParentConsole()

	port := flag.Int("port", 0, "Port to listen on (overrides config file)")
	certFile := flag.String("cert", "", "TLS certificate file (enables HTTPS)")
	keyFile := flag.String("key", "", "TLS private key file")
	filesGlob := flag.String("files", "", "Glob pattern for NDJSON files to pre-load")
	dir := flag.String("dir", "", "Directory to scan for NDJSON files to pre-load")
	noBrowser := flag.Bool("no-browser", false, "Do not open browser automatically")
	showVersion := flag.Bool("version", false, "Show version and exit")
	verbose := flag.Bool("verbose", false, "Verbose startup logging with phase timings")
	noUpdateCheck := flag.Bool("no-update-check", false, "Do not check GitHub for a newer release at startup")
	flag.Parse()

	if *showVersion {
		fmt.Printf("obs_viewer v%s\n", version)
		os.Exit(0)
	}

	startTime := time.Now()
	lastPhase := startTime
	vlog := func(format string, args ...interface{}) {
		if !*verbose {
			return
		}
		now := time.Now()
		fmt.Fprintf(os.Stderr, "[+%7.3fs  Δ%6.3fs] %s\n",
			now.Sub(startTime).Seconds(),
			now.Sub(lastPhase).Seconds(),
			fmt.Sprintf(format, args...))
		lastPhase = now
	}
	vlog("main() entered, Go %s %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	engine.Verbose = *verbose

	if (*certFile != "" && *keyFile == "") || (*certFile == "" && *keyFile != "") {
		log.Fatal("Both --cert and --key must be provided for TLS")
	}

	// Settings and config file live next to the executable
	exePath, err := os.Executable()
	if err != nil {
		log.Fatalf("Cannot determine executable path: %v", err)
	}
	exeDir := filepath.Dir(exePath)
	vlog("resolved exe dir: %s", exeDir)

	lg := logx.Init(exeDir)
	defer lg.Close()
	logx.Info("startup", logx.F{
		"version": version,
		"go":      runtime.Version(),
		"os":      runtime.GOOS,
		"arch":    runtime.GOARCH,
		"exe":     exePath,
	})
	if p := lg.Path(); p != "" {
		vlog("logs -> %s", p)
	}

	// Load config files (all optional). Core settings live in
	// obs_viewer.conf; each module may ship its own
	// obs_viewer_<name>.conf sibling. They're globbed and merged in
	// sorted order (obs_viewer.conf wins alphabetical first, so
	// module-specific overrides slot on top). `.example` suffixes
	// deliberately don't match — they ship as reference templates that
	// operators rename to `.conf` to activate.
	//
	// Default bind = loopback only. A non-loopback `listen` is honored
	// ONLY when a TLS cert is provided; otherwise we fall back to
	// loopback and print a warning.
	confListen := "127.0.0.1"
	confPaths, _ := filepath.Glob(filepath.Join(exeDir, "obs_viewer*.conf"))
	sort.Strings(confPaths)
	cfg, err := config.LoadAll(confPaths)
	if err != nil {
		log.Fatalf("Failed to load configs (%v): %v", confPaths, err)
	}
	vlog("loaded config from %d file(s): %v (%d flat keys, %d sections)",
		len(confPaths), confPaths, cfg.FlatKeys(), len(cfg.Sections()))
	if len(confPaths) > 0 {
		for _, p := range confPaths {
			log.Printf("Config loaded: %s", p)
		}
	} else {
		log.Printf("No config file found in %s — running with defaults", exeDir)
	}
	confPort := cfg.GetInt("port", 9200)
	// 180s default: short enough that a closed browser tab cleans up the
	// process within ~3 minutes; long enough that the 30s TanStack Query
	// healthcheck poll (which touches /api/version through APIHandler →
	// touchActivity) keeps the timer fresh with ~6× safety margin. A
	// module with long-running work pauses this loop via AddBusyCheck.
	confTimeout := cfg.GetInt("timeout", 180)
	rawListen := strings.TrimSpace(cfg.Get("listen"))

	// CLI --port overrides config file; 0 means "not set by user"
	if *port == 0 {
		*port = confPort
	}

	vlog("initializing DuckDB engine (first call triggers CGO load)...")
	eng, err := engine.New()
	if err != nil {
		log.Fatalf("Failed to initialize engine: %v", err)
	}
	defer eng.Close()
	vlog("DuckDB engine ready")

	// Loader registry. Adapters are added once and matched per-file by
	// content sniff at LoadFile time. parsers.d/ next to the binary feeds
	// the rule-driven adapters (block/line/xml); NDJSON and EVTX need no
	// rules and self-detect from extension or magic bytes.
	parsersDir := cfg.GetDefault("parsers_dir", filepath.Join(exeDir, "parsers.d"))
	// Compare the rules on disk against the set this binary shipped with,
	// BEFORE loading them. A malformed rule makes LoadRules fatal, and
	// that is exactly the moment "which rules did you edit?" is worth
	// answering — reporting it afterwards would mean never reporting it
	// in the case that matters most. Recorded in the log file too, since
	// that is what a bug report tends to carry.
	if shipped, err := manifest.Parse(parsersManifestData); err != nil {
		vlog("parser manifest unreadable: %v", err)
	} else if diff, err := shipped.Compare(parsersDir); err != nil {
		vlog("parser manifest comparison failed: %v", err)
	} else if diff.Clean() {
		vlog("parser rules: %d, all as shipped", len(diff.Unchanged))
	} else {
		vlog("parser rules differ from shipped: %s", diff.Summary())
		logx.Info("startup.parsers_modified", logx.F{
			"modified": diff.Modified,
			"removed":  diff.Removed,
			"added":    diff.Added,
		})
	}

	// The rule manager owns the registry from here on, so a rule saved
	// through the UI can rebuild it in place — see internal/parsers.
	ruleMgr := parsers.NewManager(parsersDir, eng.SetLoaders)
	ruleStats, err := ruleMgr.Reload()
	if err != nil {
		log.Fatalf("Failed to load parser rules from %s: %v", parsersDir, err)
	}
	vlog("loaders: %d registered (ndjson, parquet, evtx, block, line, xml); rules from %s (block=%d line=%d xml=%d other=%d)",
		len(ruleMgr.Registry().Loaders()), parsersDir,
		ruleStats.Block, ruleStats.Line, ruleStats.XML, len(ruleStats.Unknown))
	for _, r := range ruleStats.Unknown {
		log.Printf("Warning: parser rule with unknown family %q in %s (rule name=%q ignored)", r.Family, r.Source, r.Name)
	}
	store := settings.NewStore(exeDir)
	if err := store.Load(); err != nil {
		log.Printf("Warning: failed to load settings: %v", err)
	} else {
		log.Printf("Settings: %s", store.Path())
	}
	vlog("settings loaded")

	// Pre-load from CLI flags, or fall back to last directory from settings
	// (only when auto_load_previous=true in settings).
	preloadFiles := collectPreloadFiles(*filesGlob, *dir)
	if len(preloadFiles) == 0 {
		saved := store.Get()
		if saved.AutoLoadPrevious && saved.LastDirectory != "" {
			preloadFiles = collectPreloadFiles("", saved.LastDirectory)
			if len(preloadFiles) > 0 {
				log.Printf("Restoring last directory: %s", saved.LastDirectory)
				vlog("auto-restoring last directory %s (%d files)", saved.LastDirectory, len(preloadFiles))
			}
		} else if saved.LastDirectory != "" {
			vlog("auto_load_previous disabled; last directory %s not restored", saved.LastDirectory)
		}
	}
	if len(preloadFiles) > 0 {
		vlog("pre-loading %d file(s)", len(preloadFiles))
	}
	for _, f := range preloadFiles {
		var sz int64
		if st, serr := os.Stat(f); serr == nil {
			sz = st.Size()
		}
		fileStart := time.Now()
		if err := eng.LoadFile(f); err != nil {
			log.Printf("Warning: failed to load %s: %v", f, err)
		} else {
			log.Printf("Pre-loaded: %s", f)
			vlog("loaded %s (%.1f MB) in %.2fs",
				filepath.Base(f), float64(sz)/(1<<20), time.Since(fileStart).Seconds())
		}
	}

	scheme := "http"
	if *certFile != "" {
		scheme = "https"
	}

	// Resolve listen host with TLS-gating. Empty / loopback values keep the
	// default 127.0.0.1. Anything else requires a TLS cert — otherwise we
	// refuse to expose the UI to the network and fall back to loopback with
	// a warning printed to the console so the operator sees it.
	isLoopback := func(h string) bool {
		h = strings.ToLower(strings.TrimSpace(h))
		return h == "" || h == "localhost" || h == "127.0.0.1" || h == "::1"
	}
	if rawListen != "" && !isLoopback(rawListen) {
		if *certFile == "" {
			fmt.Fprintf(os.Stderr,
				"WARNING: obs_viewer.conf sets listen=%s but no TLS certificate was provided.\n"+
					"         For security, falling back to localhost-only (127.0.0.1).\n"+
					"         Provide --cert and --key to enable the configured listen address.\n",
				rawListen)
			logx.Warn("startup.listen_fallback", logx.F{
				"requested": rawListen,
				"effective": "127.0.0.1",
				"reason":    "no TLS certificate",
			})
		} else {
			confListen = rawListen
		}
	}

	addr := fmt.Sprintf("%s:%d", confListen, *port)
	url := fmt.Sprintf("%s://localhost:%d", scheme, *port)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("Port %d is not available: %v", *port, err)
	}
	ln.Close()
	vlog("port %d available", *port)

	srv := server.New(eng, staticFS, version, store)
	srv.SetIdleTimeoutSeconds(confTimeout)
	srv.SetRules(ruleMgr)

	// Release notification. On by default; `update_check = false` in
	// obs_viewer.conf or --no-update-check turns it off, and the flag
	// wins so a one-off run can stay offline without editing config.
	//
	// This is the only outbound request obs-viewer ever makes. It reads
	// the GitHub releases API and does nothing else — no download, no
	// install, no telemetry. It runs in the background so startup never
	// waits on it, and it fails silently, because on an air-gapped host
	// failing is the expected outcome of every single launch.
	updateEnabled := cfg.GetDefault("update_check", "true") != "false" && !*noUpdateCheck
	updater := update.New(version, updateEnabled)
	srv.SetUpdateChecker(updater)
	updater.Start(context.Background(), vlog)
	vlog("update check: enabled=%v", updateEnabled)

	// Module registry. Modules are mounted only when their config
	// section is present in obs_viewer.conf — see /api/modules for
	// the runtime view the SPA reads. No modules ship by default;
	// add yours with modreg.Add(<pkg>.New()) — see docs/MODULES.md.
	modreg := module.NewRegistry(cfg, module.Deps{
		Engine:        eng,
		Settings:      store,
		APIHandler:    srv.APIHandler,
		TouchActivity: srv.TouchActivity,
		AddBusyCheck:  srv.AddBusyCheck,
	})
	if err := modreg.Boot(srv.Mux()); err != nil {
		log.Fatalf("module registry boot: %v", err)
	}
	vlog("server constructed, ready to serve (total startup: %.2fs)", time.Since(startTime).Seconds())

	fmt.Printf("obs_viewer v%s\n", version)
	fmt.Printf("Listening on %s\n", url)
	if confTimeout > 0 {
		fmt.Printf("Inactivity timeout: %ds\n", confTimeout)
	}
	fmt.Println("Press Ctrl+C to stop")

	if !*noBrowser {
		go openBrowser(url)
	}

	// Inactivity timeout: shut down if no API requests for confTimeout seconds.
	// While any module reports itself busy we skip the check entirely — a
	// module's long-running work can outlast a normal viewer session, and an
	// operator watching it shouldn't be ambushed by the auto-shutdown.
	if confTimeout > 0 {
		go func() {
			timeout := time.Duration(confTimeout) * time.Second
			for {
				time.Sleep(30 * time.Second)
				if srv.IsBusy() {
					continue
				}
				last := srv.LastActivity()
				if time.Since(last) > timeout {
					log.Printf("Inactivity timeout (%ds) reached, shutting down", confTimeout)
					os.Exit(0)
				}
			}
		}()
	}

	if *certFile != "" {
		log.Fatal(srv.ListenAndServeTLS(addr, *certFile, *keyFile))
	} else {
		log.Fatal(srv.ListenAndServe(addr))
	}
}

func collectPreloadFiles(glob, dir string) []string {
	var files []string
	if glob != "" {
		matches, err := filepath.Glob(glob)
		if err != nil {
			log.Printf("Warning: invalid glob pattern: %v", err)
		}
		files = append(files, matches...)
	}
	if dir != "" {
		entries, err := os.ReadDir(dir)
		if err != nil {
			log.Printf("Warning: cannot read directory %s: %v", dir, err)
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".ndjson") {
				files = append(files, filepath.Join(dir, e.Name()))
			}
		}
	}
	return files
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
