// Package logx is a tiny structured JSON logger that writes newline-
// delimited JSON objects to logs/obs_viewer_<date>.log next to the exe.
// No external deps. Thread-safe.
package logx

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Logger struct {
	mu      sync.Mutex
	file    *os.File
	path    string
	dir     string
	day     string
	enc     *json.Encoder
	fallbck bool // true => writing to stderr only
}

var global *Logger

// Init opens (or creates) logs/obs_viewer_YYYY-MM-DD.log under baseDir.
// On any error it falls back to stderr so logging never blocks startup.
func Init(baseDir string) *Logger {
	l := &Logger{dir: filepath.Join(baseDir, "logs")}
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "logx: cannot create %s: %v (falling back to stderr)\n", l.dir, err)
		l.fallbck = true
		l.enc = json.NewEncoder(os.Stderr)
		global = l
		return l
	}
	l.rotate()
	global = l
	return l
}

// Default returns the global logger, initializing a stderr-only one
// on the fly if Init was never called.
func Default() *Logger {
	if global == nil {
		l := &Logger{fallbck: true, enc: json.NewEncoder(os.Stderr)}
		global = l
	}
	return global
}

func (l *Logger) rotate() {
	today := time.Now().Format("2006-01-02")
	if today == l.day && l.file != nil {
		return
	}
	if l.file != nil {
		_ = l.file.Close()
	}
	l.day = today
	l.path = filepath.Join(l.dir, "obs_viewer_"+today+".log")
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logx: cannot open %s: %v (falling back to stderr)\n", l.path, err)
		l.fallbck = true
		l.enc = json.NewEncoder(os.Stderr)
		return
	}
	l.file = f
	l.enc = json.NewEncoder(f)
}

// Path returns the current log file path ("" when falling back to stderr).
func (l *Logger) Path() string {
	if l.fallbck {
		return ""
	}
	return l.path
}

// Close flushes and closes the underlying file.
func (l *Logger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		_ = l.file.Sync()
		_ = l.file.Close()
		l.file = nil
	}
}

// Log writes a single JSON record. Fields is an optional key/value map.
func (l *Logger) Log(level, event string, fields map[string]interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.fallbck {
		l.rotate()
	}
	rec := map[string]interface{}{
		"ts":    time.Now().UTC().Format(time.RFC3339Nano),
		"level": level,
		"event": event,
	}
	for k, v := range fields {
		rec[k] = v
	}
	if err := l.enc.Encode(rec); err != nil {
		fmt.Fprintf(os.Stderr, "logx: encode failed: %v\n", err)
	}
}

// Convenience wrappers on the global logger.
func Info(event string, f map[string]interface{})  { Default().Log("info", event, f) }
func Warn(event string, f map[string]interface{})  { Default().Log("warn", event, f) }
func Error(event string, f map[string]interface{}) { Default().Log("error", event, f) }
func Debug(event string, f map[string]interface{}) { Default().Log("debug", event, f) }

// F is a shorthand for building field maps inline: logx.Info("x", logx.F{"a": 1}).
type F = map[string]interface{}
