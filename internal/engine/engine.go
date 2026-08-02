package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/labmk/obs-viewer/internal/ingest"
	"github.com/labmk/obs-viewer/internal/logx"
)

// FileInfo holds metadata about a loaded NDJSON file.
type FileInfo struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Records   int64  `json:"records"`
	Enabled   bool   `json:"enabled"`
	LoadedAt  string `json:"loaded_at"`
	TableName string `json:"-"`
}

// QueryRequest represents a query from the frontend.
type QueryRequest struct {
	Filters    []Filter `json:"filters"`
	TimeFrom   *string  `json:"time_from"`
	TimeTo     *string  `json:"time_to"`
	SortOrder  string   `json:"sort_order"` // "asc" or "desc"
	SortField  string   `json:"sort_field"` // optional override; falls back to engine timestampField when empty or missing from the union
	Offset     int      `json:"offset"`
	Limit      int      `json:"limit"`
	SearchText string   `json:"search_text"` // free-text across all fields
}

// Filter represents a single field-level filter condition.
type Filter struct {
	Field    string `json:"field"`
	Operator string `json:"operator"` // is, is_not, contains, not_contains, wildcard, not_wildcard, exists, does_not_exist
	Value    string `json:"value"`
	Logic    string `json:"logic"` // "and" or "or" - how this connects to next filter
}

// QueryResult is the paginated result of a query.
type QueryResult struct {
	Rows       []map[string]interface{} `json:"rows"`
	TotalCount int64                    `json:"total_count"`
	Fields     []string                 `json:"fields"`
	Offset     int                      `json:"offset"`
	Limit      int                      `json:"limit"`
}

// TimeRange holds the min and max timestamps across all loaded data.
type TimeRange struct {
	Min    *string  `json:"min"`
	Max    *string  `json:"max"`
	Fields []string `json:"timestamp_fields"`
}

// Engine manages DuckDB and loaded NDJSON files.
type Engine struct {
	db             *sql.DB
	mu             sync.RWMutex
	files          map[string]*FileInfo
	fileOrder      []string
	timestampField string
	nextID         int
	// tableCols caches the column list for each loaded table. Populated on
	// LoadFile, cleared on UnloadFile. Avoids hitting information_schema on
	// every Query/GetTimeRange/GetFields call.
	tableCols map[string][]string
	// tableStructPaths caches the dotted paths INTO every STRUCT column
	// of each loaded table (e.g. "nodeinfo.type", "agent.id.major").
	// Populated by probing typeof + struct_keys at LoadFile time. A field
	// reference like `nodeinfo.type` from a user filter resolves through
	// this map; if matched, it's emitted as struct path notation
	// (`"nodeinfo"."type"`) rather than a single quoted ident
	// (`"nodeinfo.type"`) which would look for a literal-dotted column
	// name that doesn't exist. See quoteFieldRef.
	tableStructPaths map[string][]string
	// pathIndex maps absolute paths to file IDs for O(1) duplicate detection.
	pathIndex map[string]string
	// loaders dispatches file loading to a format-specific adapter. When
	// nil, LoadFile falls back to the legacy direct NDJSON path — keeps
	// behavior unchanged for callers that haven't migrated.
	loaders *ingest.Registry
}

// SetLoaders attaches a loader Registry. When set, LoadFile sniffs each
// input and picks the highest-confidence loader. Direct ingesters (NDJSON)
// keep the read_json_auto fast path; streaming adapters convert through
// a temp NDJSON file. Passing nil restores the legacy behavior.
func (e *Engine) SetLoaders(r *ingest.Registry) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.loaders = r
}

// Verbose enables sub-phase timing logs inside engine initialization.
// Set by main when --verbose is passed.
var Verbose bool

func vphase(label string, start time.Time) {
	if !Verbose {
		return
	}
	fmt.Fprintf(os.Stderr, "[engine] %-30s %7.3fs\n", label, time.Since(start).Seconds())
}

// New creates a new Engine with an in-memory DuckDB instance.
func New() (*Engine, error) {
	t := time.Now()
	db, err := sql.Open("duckdb", "")
	vphase("sql.Open(\"duckdb\", \"\")", t)
	if err != nil {
		return nil, fmt.Errorf("failed to open DuckDB: %w", err)
	}

	t = time.Now()
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("duckdb Ping failed: %w", err)
	}
	vphase("db.Ping (first real conn)", t)

	// NOTE: do NOT call "INSTALL json". The JSON extension is statically
	// linked into go-duckdb and is always available. INSTALL would try to
	// download it from extensions.duckdb.org, which on air-gapped / locked-
	// down VMs blocks on socket timeouts for ~4 minutes on every startup.

	// Prove the JSON reader is usable now, with a tiny query. If this fails
	// we want a real error instead of a mysterious hang later in LoadFile.
	t = time.Now()
	if _, err := db.Exec("SELECT json_valid('{}')"); err != nil {
		return nil, fmt.Errorf("duckdb JSON extension not available: %w", err)
	}
	vphase("SELECT json_valid('{}')", t)

	e := &Engine{
		db:               db,
		files:            make(map[string]*FileInfo),
		timestampField:   "@timestamp", // default, auto-detected on first load
		tableCols:        make(map[string][]string),
		tableStructPaths: make(map[string][]string),
		pathIndex:        make(map[string]string),
	}
	return e, nil
}

// Close shuts down the engine.
func (e *Engine) Close() error {
	return e.db.Close()
}

// LoadFile is the legacy entry point; equivalent to LoadFileCtx with
// a background context. Callers that need to cancel mid-ingest (HTTP
// request cancellation, shutdown) should use LoadFileCtx instead.
func (e *Engine) LoadFile(path string) error {
	return e.LoadFileCtx(context.Background(), path)
}

// LoadFileCtx reads a file into DuckDB, with cancellation. The source
// format is decided by the registered Loader (NDJSON is read directly;
// everything else is streamed through a temp NDJSON file). When no
// Registry has been attached via SetLoaders, falls back to the legacy
// direct-NDJSON path so older call sites still work.
//
// Honoring ctx matters for multi-GB ingests: a user clicking Cancel
// in the UI cancels the HTTP request, which cancels ctx, which
// propagates into the adapter's Stream loop and stops it before it
// fills the temp NDJSON with another gigabyte of records.
func (e *Engine) LoadFileCtx(ctx context.Context, path string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}

	if _, ok := e.pathIndex[absPath]; ok {
		return fmt.Errorf("file already loaded: %s", absPath)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("cannot stat file: %w", err)
	}

	// Resolve the path DuckDB will actually read. Either the original
	// (NDJSON direct path) or a temp NDJSON produced by an adapter.
	loadPath := absPath
	loaderName := "ndjson-legacy"
	var tmpPath string
	// readExpr stays nil for everything that DuckDB reads as NDJSON —
	// the direct NDJSON path and every streaming adapter, which convert
	// to a temp NDJSON file first. Only a DirectSQLIngester sets it.
	var readExpr readExprFunc
	defer func() {
		if tmpPath != "" {
			_ = os.Remove(tmpPath)
		}
	}()

	if e.loaders != nil {
		hint, herr := ingest.HintForFile(absPath)
		if herr != nil {
			return fmt.Errorf("sniff: %w", herr)
		}
		loader := e.loaders.Pick(hint)
		if loader == nil {
			return fmt.Errorf("no loader matched file: %s", absPath)
		}
		loaderName = loader.Name()
		if di, ok := loader.(ingest.DirectIngester); ok && di.UseDirectPath() {
			// Direct path — DuckDB reads the original file. NDJSON goes
			// through read_json_auto; a loader that needs a different
			// reader (Parquet) supplies it via DirectSQLIngester.
			if ds, ok := loader.(ingest.DirectSQLIngester); ok {
				readExpr = ds.ReadExpr
			}
		} else {
			streamer, ok := loader.(ingest.RecordStreamer)
			if !ok {
				return fmt.Errorf("loader %s is neither direct nor streaming", loaderName)
			}
			tp, sErr := streamToTempNDJSON(ctx, streamer, hint)
			if sErr != nil {
				return fmt.Errorf("loader %s: %w", loaderName, sErr)
			}
			tmpPath = tp
			loadPath = tp
		}
	}

	e.nextID++
	id := fmt.Sprintf("f%d", e.nextID)
	tableName := fmt.Sprintf("file_%s", id)

	overall := time.Now()
	phaseLoad := time.Now()
	if err := e.duckdbLoadDirect(tableName, loadPath, absPath, loaderName, info.Size(), readExpr); err != nil {
		return err
	}
	durLoad := time.Since(phaseLoad)

	phaseCount := time.Now()
	var count int64
	row := e.db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", tableName))
	if err := row.Scan(&count); err != nil {
		count = 0
	}
	durCount := time.Since(phaseCount)

	phaseCols := time.Now()
	cols, _ := e.fetchTableColumns(tableName)
	e.tableCols[tableName] = cols
	durCols := time.Since(phaseCols)

	// Probe each top-level column. STRUCT columns expand into dotted
	// paths so filters like `nodeinfo.type = "worker"` resolve to real
	// struct sub-field access instead of a literal-named column that
	// doesn't exist. Failures are non-fatal — an unprobable column
	// (all-NULL, empty table) just yields no sub-paths.
	phaseStruct := time.Now()
	var structPaths []string
	for _, c := range cols {
		sub, _ := e.discoverStructSubPaths(tableName, []string{c})
		structPaths = append(structPaths, sub...)
	}
	e.tableStructPaths[tableName] = structPaths
	durStruct := time.Since(phaseStruct)

	// Per-phase timing visible to operators. Critical for diagnosing
	// "why is this load slow?" on multi-hundred-MB NDJSONs where the
	// JSON-schema-inference scan in read_json_auto dominates total
	// time, vs. our metadata work (column fetch, struct probes).
	sizeMB := float64(info.Size()) / (1 << 20)
	logx.Info("engine.LoadFile.timing", logx.F{
		"file":           filepath.Base(absPath),
		"size_mb":        sizeMB,
		"rows":           count,
		"total_s":        time.Since(overall).Seconds(),
		"load_s":         durLoad.Seconds(),
		"count_s":        durCount.Seconds(),
		"cols_s":         durCols.Seconds(),
		"struct_probe_s": durStruct.Seconds(),
		"top_level_cols": len(cols),
		"struct_paths":   len(structPaths),
	})
	fmt.Fprintf(os.Stderr, "[engine] LoadFile %s (%.1f MB, %d rows): total %.2fs = load %.2fs + count %.2fs + cols %.2fs + struct-probe %.2fs (%d top-level cols, %d struct paths)\n",
		filepath.Base(absPath), sizeMB, count, time.Since(overall).Seconds(),
		durLoad.Seconds(), durCount.Seconds(), durCols.Seconds(), durStruct.Seconds(),
		len(cols), len(structPaths))

	if len(e.files) == 0 {
		e.timestampField = pickTimestampField(cols)
	}

	fi := &FileInfo{
		ID:        id,
		Path:      absPath,
		Name:      filepath.Base(absPath),
		Size:      info.Size(),
		Records:   count,
		Enabled:   true,
		LoadedAt:  time.Now().UTC().Format(time.RFC3339),
		TableName: tableName,
	}
	e.files[id] = fi
	e.fileOrder = append(e.fileOrder, id)
	e.pathIndex[absPath] = id

	return nil
}

// duckdbLoadJSON runs the CREATE TABLE … FROM read_json_auto step.
// loadPath is what DuckDB reads (may be a temp file); reportPath is
// the original absolute path the user asked for (used only for logs).
//
// ignore_errors=true: skip individual malformed records instead of
// failing the whole load. Also papers over case-insensitive struct key
// collisions (e.g. TaskID vs taskID) that DuckDB's JSON auto-detection
// would otherwise reject with "Duplicate name in struct".
//
// sample_size=50000: DuckDB's JSON schema inference scans the first N
// rows to decide column types. With -1 (scan-all) a 200 MB filebeat
// NDJSON took ~4.5s; with a fixed sample it drops to ~1.5s — the
// remaining time is the actual data load. Trade: fields that appear
// ONLY in rows beyond row 50000 won't be in the inferred schema and
// would be silently dropped. For machine-generated NDJSON the schema
// per file is stable (filebeat emits uniform records), so 50000 is
// safely above the "we've seen every field" threshold. Override at
// build time by editing this constant; per-file override could be
// added later if real data violates the assumption.
const jsonSampleRows = 50000

// readExprFunc builds the DuckDB table expression for a load. Nil means
// the NDJSON default.
type readExprFunc func(escapedPath string) string

// jsonReadExpr is the default: DuckDB's newline-delimited JSON reader,
// which every streaming adapter also targets because they convert to a
// temp NDJSON file first.
func jsonReadExpr(escapedPath string) string {
	return fmt.Sprintf(
		`read_json_auto('%s', format='newline_delimited', maximum_object_size=16777216, ignore_errors=true, sample_size=%d)`,
		escapedPath, jsonSampleRows,
	)
}

func (e *Engine) duckdbLoadDirect(tableName, loadPath, reportPath, loaderName string, sizeBytes int64, expr readExprFunc) error {
	if expr == nil {
		expr = jsonReadExpr
	}
	query := fmt.Sprintf(
		`CREATE TABLE %s AS SELECT * FROM %s`,
		tableName, expr(escapeSQLString(loadPath)),
	)
	loadStart := time.Now()
	if _, err := e.db.Exec(query); err != nil {
		logx.Error("engine.load_file", logx.F{
			"path":   reportPath,
			"loader": loaderName,
			"table":  tableName,
			"error":  err.Error(),
			"sql":    query,
		})
		return fmt.Errorf("failed to load file into DuckDB: %w", err)
	}
	logx.Info("engine.load_file", logx.F{
		"path":        reportPath,
		"loader":      loaderName,
		"table":       tableName,
		"size_bytes":  sizeBytes,
		"duration_ms": time.Since(loadStart).Milliseconds(),
	})
	return nil
}

// streamToTempNDJSON drains a RecordStreamer into a freshly-created
// NDJSON temp file and returns its path. Caller is responsible for
// deleting the file once DuckDB has loaded it.
//
// Records are written one per line using encoding/json — DuckDB's
// read_json_auto with format='newline_delimited' picks them up the
// same way it picks up the regular NDJSON fast path.
//
// ctx is threaded into the adapter's Stream so a cancelled HTTP
// request can abort a multi-GB ingest mid-flight instead of running
// to completion and then discarding the result.
func streamToTempNDJSON(ctx context.Context, s ingest.RecordStreamer, h ingest.LoadHint) (string, error) {
	tmp, err := os.CreateTemp("", "obs_viewer_*.ndjson")
	if err != nil {
		return "", fmt.Errorf("create temp ndjson: %w", err)
	}
	tmpPath := tmp.Name()

	enc := json.NewEncoder(tmp)
	enc.SetEscapeHTML(false)

	streamErr := s.Stream(ctx, h, func(rec ingest.Record) error {
		// Cheap belt-and-suspenders: also check ctx on every emit so
		// adapters that don't poll ctx themselves still stop quickly.
		if err := ctx.Err(); err != nil {
			return err
		}
		return enc.Encode(rec)
	})
	closeErr := tmp.Close()

	if streamErr != nil {
		_ = os.Remove(tmpPath)
		return "", streamErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("close temp ndjson: %w", closeErr)
	}
	return tmpPath, nil
}

// UnloadFile removes a file from the engine.
func (e *Engine) UnloadFile(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	fi, ok := e.files[id]
	if !ok {
		return fmt.Errorf("file not found: %s", id)
	}

	if _, err := e.db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s", fi.TableName)); err != nil {
		return fmt.Errorf("failed to drop table: %w", err)
	}

	delete(e.tableCols, fi.TableName)
	delete(e.tableStructPaths, fi.TableName)
	delete(e.pathIndex, fi.Path)
	delete(e.files, id)
	for i, fid := range e.fileOrder {
		if fid == id {
			e.fileOrder = append(e.fileOrder[:i], e.fileOrder[i+1:]...)
			break
		}
	}
	return nil
}

// SetFileEnabled toggles whether a file is included in queries.
func (e *Engine) SetFileEnabled(id string, enabled bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	fi, ok := e.files[id]
	if !ok {
		return fmt.Errorf("file not found: %s", id)
	}
	fi.Enabled = enabled
	return nil
}

// GetFiles returns all loaded file metadata.
func (e *Engine) GetFiles() []*FileInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]*FileInfo, 0, len(e.fileOrder))
	for _, id := range e.fileOrder {
		result = append(result, e.files[id])
	}
	return result
}

// GetTimestampField returns the detected/configured timestamp field name.
func (e *Engine) GetTimestampField() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.timestampField
}

// SetTimestampField allows overriding the timestamp field.
func (e *Engine) SetTimestampField(field string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.timestampField = field
}

// GetTimeRange returns the min/max timestamps across all enabled files.
func (e *Engine) GetTimeRange() (*TimeRange, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	tables := e.enabledTables()
	if len(tables) == 0 {
		return &TimeRange{}, nil
	}

	_, canonByLower := e.canonicalColumns(tables)
	tsLower := strings.ToLower(e.timestampField)

	// Only include tables that actually have the timestamp column — cached
	// schema makes this a pure map lookup, no SQL roundtrip per table.
	// Match case-insensitively so a file whose timestamp field is cased
	// differently from the engine's canonical name still contributes its
	// MIN/MAX (the column lives in this table under its raw casing).
	var parts []string
	seenTSFields := make(map[string]bool)
	var tsFields []string
	for _, t := range tables {
		cols := e.tableCols[t]
		for _, c := range cols {
			if strings.ToLower(c) == tsLower {
				actualQuoted := quoteIdent(c)
				parts = append(parts, fmt.Sprintf("SELECT MIN(%s) as ts_min, MAX(%s) as ts_max FROM %s", actualQuoted, actualQuoted, t))
				break
			}
		}
		for _, c := range cols {
			canon := canonByLower[strings.ToLower(c)]
			if isTimestampLike(canon) && !seenTSFields[canon] {
				seenTSFields[canon] = true
				tsFields = append(tsFields, canon)
			}
		}
	}
	tr := &TimeRange{Fields: tsFields}
	if len(parts) == 0 {
		return tr, nil
	}
	query := fmt.Sprintf("SELECT MIN(ts_min)::VARCHAR, MAX(ts_max)::VARCHAR FROM (%s)", strings.Join(parts, " UNION ALL "))

	var minTS, maxTS sql.NullString
	if err := e.db.QueryRow(query).Scan(&minTS, &maxTS); err != nil {
		return tr, nil
	}
	if minTS.Valid {
		tr.Min = &minTS.String
	}
	if maxTS.Valid {
		tr.Max = &maxTS.String
	}
	return tr, nil
}

// Query executes a filtered, paginated query across enabled files.
func (e *Engine) Query(req QueryRequest) (*QueryResult, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	tables := e.enabledTables()
	if len(tables) == 0 {
		return &QueryResult{Rows: []map[string]interface{}{}, Fields: []string{}}, nil
	}

	if req.Limit <= 0 {
		req.Limit = 100
	}
	if req.SortOrder == "" {
		req.SortOrder = "asc"
	}

	// Build UNION ALL across enabled tables with aligned columns
	baseQuery, allCols := e.buildUnionQuery(tables, true)

	// Resolve the ORDER BY column. Per-request override lets the UI
	// flip between @timestamp and ObservedTimestamp without touching
	// engine state; unknown/absent fields fall back to the engine's
	// auto-detected timestamp so the query never fails on a missing
	// column.
	tsField := e.resolveSortField(req.SortField, allCols)

	// Build WHERE clause
	whereClauses := e.buildWhereClause(req, allCols)
	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = "WHERE " + strings.Join(whereClauses, " ")
	}

	// Count total
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM (%s) AS combined %s", baseQuery, whereSQL)
	var totalCount int64
	queryStart := time.Now()
	if err := e.db.QueryRow(countQuery).Scan(&totalCount); err != nil {
		logx.Error("engine.query_count", logx.F{
			"error":  err.Error(),
			"sql":    countQuery,
			"tables": tables,
		})
		return nil, fmt.Errorf("count query failed: %w", err)
	}

	// Fetch rows
	dataQuery := fmt.Sprintf(
		"SELECT * FROM (%s) AS combined %s ORDER BY %s %s LIMIT %d OFFSET %d",
		baseQuery, whereSQL, tsField, req.SortOrder, req.Limit, req.Offset,
	)
	rows, err := e.db.Query(dataQuery)
	if err != nil {
		logx.Error("engine.query_data", logx.F{
			"error":       err.Error(),
			"sql":         dataQuery,
			"tables":      tables,
			"total_count": totalCount,
			"duration_ms": time.Since(queryStart).Milliseconds(),
		})
		return nil, fmt.Errorf("data query failed: %w", err)
	}
	logx.Info("engine.query_data", logx.F{
		"tables":      tables,
		"total_count": totalCount,
		"limit":       req.Limit,
		"offset":      req.Offset,
		"duration_ms": time.Since(queryStart).Milliseconds(),
	})
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}

	// Use the known column set (allCols) as visible columns
	visibleCols := allCols
	_ = columns // columns includes _source_table; allCols already excludes it

	var resultRows []map[string]interface{}
	for rows.Next() {
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}
		if err := rows.Scan(valuePtrs...); err != nil {
			continue
		}
		row := make(map[string]interface{})
		for i, col := range columns {
			if col == "_source_table" {
				continue
			}
			row[col] = formatValue(values[i])
		}
		resultRows = append(resultRows, row)
	}

	if resultRows == nil {
		resultRows = []map[string]interface{}{}
	}

	return &QueryResult{
		Rows:       resultRows,
		TotalCount: totalCount,
		Fields:     visibleCols,
		Offset:     req.Offset,
		Limit:      req.Limit,
	}, nil
}

// HistogramBucket is one bar: the bucket's start instant and how many
// records fell into it.
type HistogramBucket struct {
	Start string `json:"start"`
	Count int64  `json:"count"`
}

// Histogram is a count of matching records over time.
type Histogram struct {
	Buckets []HistogramBucket `json:"buckets"`
	// IntervalSeconds is the bucket width actually used. The UI labels
	// the strip with it, because "23 per bar" means nothing without it.
	IntervalSeconds int64 `json:"interval_seconds"`
	// Min and Max are the span the buckets cover, so a drag-select can
	// map a pixel back to an instant without re-deriving the range.
	Min string `json:"min,omitempty"`
	Max string `json:"max,omitempty"`
	// Total is the sum of all buckets. Equal to the query's total_count
	// except for records whose timestamp is NULL, which no bucket can
	// hold — the difference is worth surfacing rather than hiding.
	Total int64 `json:"total"`
	// Field is the timestamp column the buckets were built on.
	Field string `json:"field,omitempty"`
}

// maxHistogramBuckets caps the bar count.
//
// This runs on every query, so it has to stay cheap, and the cap is
// what keeps it cheap: bucket width is derived from the span rather
// than fixed, so a one-minute range and a one-year range both produce
// the same amount of work and the same amount of DOM.
const maxHistogramBuckets = 120

// histogramIntervals are the bucket widths the strip will choose from,
// in seconds. Round, human-legible steps — a bar is a unit of time
// somebody has to reason about, and 37 seconds is not one.
var histogramIntervals = []int64{
	1, 2, 5, 10, 15, 30,
	60, 120, 300, 600, 900, 1800,
	3600, 7200, 10800, 21600, 43200,
	86400, 172800, 604800,
	2592000, 7776000, 31536000,
}

// GetHistogram returns counts of matching records bucketed over time,
// using the same filters as Query so the strip always describes the
// result the operator is looking at.
//
// The bucket width is chosen from the span of the *filtered* data, not
// the loaded data: after narrowing to a five-minute window the strip
// has to resolve to seconds, or it collapses into one bar and stops
// answering anything.
//
// Returns an empty histogram rather than an error when there is no
// timestamp column or no data. A missing histogram is a strip that
// does not render; it must never be the reason a query fails.
func (e *Engine) GetHistogram(req QueryRequest) (*Histogram, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	out := &Histogram{Buckets: []HistogramBucket{}}
	tables := e.enabledTables()
	if len(tables) == 0 {
		return out, nil
	}

	baseQuery, allCols := e.buildUnionQuery(tables, false)
	tsField := e.resolveSortField(req.SortField, allCols)
	if tsField == "" {
		return out, nil
	}
	out.Field = strings.Trim(tsField, `"`)

	whereClauses := e.buildWhereClause(req, allCols)
	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = "WHERE " + strings.Join(whereClauses, " ")
	}

	// The timestamps are VARCHAR in the union — every adapter writes
	// ISO-8601 UTC — so they are cast once here rather than compared as
	// strings. TRY_CAST because a heterogeneous union can carry a column
	// that is timestamp-like in one file and free text in another, and
	// one such row must not fail the whole strip.
	tsExpr := fmt.Sprintf("TRY_CAST(%s AS TIMESTAMP)", tsField)
	spanQuery := fmt.Sprintf(
		"SELECT MIN(%s), MAX(%s), COUNT(%s) FROM (%s) AS combined %s",
		tsExpr, tsExpr, tsExpr, baseQuery, whereSQL,
	)

	var minTS, maxTS sql.NullTime
	var counted int64
	if err := e.db.QueryRow(spanQuery).Scan(&minTS, &maxTS, &counted); err != nil {
		logx.Warn("engine.histogram_span", logx.F{"error": err.Error(), "sql": spanQuery})
		return out, nil
	}
	if !minTS.Valid || !maxTS.Valid || counted == 0 {
		return out, nil
	}

	span := maxTS.Time.Sub(minTS.Time)
	interval := pickHistogramInterval(span)
	out.IntervalSeconds = interval
	out.Min = minTS.Time.UTC().Format(time.RFC3339Nano)
	out.Max = maxTS.Time.UTC().Format(time.RFC3339Nano)

	bucketQuery := fmt.Sprintf(
		"SELECT TIME_BUCKET(INTERVAL %d SECOND, %s) AS b, COUNT(*) FROM (%s) AS combined %s "+
			"GROUP BY b HAVING b IS NOT NULL ORDER BY b",
		interval, tsExpr, baseQuery, whereSQL,
	)
	start := time.Now()
	rows, err := e.db.Query(bucketQuery)
	if err != nil {
		logx.Warn("engine.histogram", logx.F{"error": err.Error(), "sql": bucketQuery})
		return out, nil
	}
	defer rows.Close()

	for rows.Next() {
		var b time.Time
		var n int64
		if err := rows.Scan(&b, &n); err != nil {
			continue
		}
		out.Buckets = append(out.Buckets, HistogramBucket{
			Start: b.UTC().Format(time.RFC3339Nano),
			Count: n,
		})
		out.Total += n
	}
	logx.Info("engine.histogram", logx.F{
		"buckets":     len(out.Buckets),
		"interval_s":  interval,
		"total":       out.Total,
		"duration_ms": time.Since(start).Milliseconds(),
	})
	return out, nil
}

// pickHistogramInterval returns the smallest listed bucket width that
// keeps the bar count within the cap.
func pickHistogramInterval(span time.Duration) int64 {
	seconds := int64(span.Seconds())
	if seconds <= 0 {
		return histogramIntervals[0]
	}
	for _, iv := range histogramIntervals {
		if seconds/iv <= maxHistogramBuckets {
			return iv
		}
	}
	// Span longer than the largest listed width divided across the cap:
	// fall back to whatever fits, rounded to a whole day.
	iv := seconds / maxHistogramBuckets
	return (iv/86400 + 1) * 86400
}

// GetFields returns the union of all field names across all enabled tables.
// Columns are ordered by first appearance: fields from earlier tables come
// first, then new fields from subsequent tables are appended.
func (e *Engine) GetFields() ([]string, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	tables := e.enabledTables()
	if len(tables) == 0 {
		return []string{}, nil
	}

	// Union of top-level columns + every discovered STRUCT sub-path
	// across enabled tables. Sub-paths are appended after the top-level
	// names so the QueryBuilder's FieldPicker shows e.g. both
	// `nodeinfo` (the struct) and `nodeinfo.type` (filter-able).
	// Dedup is case-insensitive — a sub-path that collides with a
	// literal column wins to literal; we don't reorder.
	out := e.allColumns(tables)
	seen := make(map[string]bool, len(out))
	for _, c := range out {
		seen[strings.ToLower(c)] = true
	}
	for _, t := range tables {
		for _, p := range e.tableStructPaths[t] {
			low := strings.ToLower(p)
			if seen[low] {
				continue
			}
			seen[low] = true
			out = append(out, p)
		}
	}
	return out, nil
}

// FieldSamples returns DISTINCT values for each requested field across
// every enabled table, capped at `cap` per field. Fields whose distinct
// count exceeds `cap` are returned with an empty slice (signalling
// "too high-cardinality to be useful as a literal value list" — the
// caller should drop them from a prompt rather than render a partial
// list that misleads the model). Fields that aren't present in any
// enabled table are simply omitted from the result.
//
// Used by /api/field-samples to feed the AI prompt assembler. Each
// field is queried independently against the UNION ALL so the result
// is the union across the file set.
func (e *Engine) FieldSamples(fields []string, cap int) (map[string][]string, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	tables := e.enabledTables()
	out := map[string][]string{}
	if len(tables) == 0 || cap <= 0 {
		return out, nil
	}
	baseQuery, allCols := e.buildUnionQuery(tables, false)
	if baseQuery == "" {
		return out, nil
	}
	// A field is queryable if it's either a literal top-level column or
	// a discovered STRUCT sub-path. Build a single set of accepted names
	// across all tables so callers can ask about anything they've seen
	// in /api/fields.
	present := map[string]bool{}
	for _, c := range allCols {
		present[strings.ToLower(c)] = true
	}
	for _, paths := range e.tableStructPaths {
		for _, p := range paths {
			present[strings.ToLower(p)] = true
		}
	}
	for _, f := range fields {
		if !present[strings.ToLower(f)] {
			continue
		}
		// Route through the same struct-aware quoting buildFilterCondition
		// uses, so requesting "nodeinfo.type" yields the struct sub-path
		// expression instead of a literal-named column.
		col := e.quoteFieldRef(f)
		// `cap+1` lets us tell "exactly cap distinct values" apart from
		// "more than cap and we should drop the field." LOWER avoids
		// case-fragmented duplicates polluting the list.
		q := fmt.Sprintf(
			"SELECT DISTINCT TRY_CAST(%s AS VARCHAR) AS v FROM (%s) WHERE %s IS NOT NULL AND TRY_CAST(%s AS VARCHAR) != '' ORDER BY v LIMIT %d",
			col, baseQuery, col, col, cap+1,
		)
		rows, err := e.db.Query(q)
		if err != nil {
			// Per-field failures shouldn't poison the whole response;
			// just skip the field.
			continue
		}
		var values []string
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				continue
			}
			values = append(values, v)
		}
		rows.Close()
		if len(values) > cap {
			// Too high-cardinality — return empty so the caller
			// suppresses this field from the prompt.
			out[f] = []string{}
			continue
		}
		out[f] = values
	}
	return out, nil
}

// ExportFiltered exports the current filtered view to an NDJSON file.
func (e *Engine) ExportFiltered(req QueryRequest, outputPath string) (int64, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	tables := e.enabledTables()
	if len(tables) == 0 {
		return 0, fmt.Errorf("no enabled files")
	}

	tsField := e.quotedTimestampField()
	baseQuery, allCols := e.buildUnionQuery(tables, false)

	whereClauses := e.buildWhereClause(req, allCols)
	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = "WHERE " + strings.Join(whereClauses, " ")
	}

	// Format is chosen by the output extension rather than a separate
	// parameter: the user already states their intent by naming the
	// file, and a mismatch between an explicit format and the extension
	// is a bug waiting to happen.
	//
	// Both writers are DuckDB COPY, and the parquet extension is
	// statically linked, so neither path downloads anything.
	copyOptions := "FORMAT JSON, ARRAY false"
	if ExportFormatFor(outputPath) == ExportParquet {
		// zstd over the default snappy: log data is highly repetitive
		// columnwise and zstd is materially smaller at a decompression
		// cost nothing here is sensitive to.
		copyOptions = "FORMAT PARQUET, COMPRESSION zstd"
	}

	exportQuery := fmt.Sprintf(
		`COPY (SELECT * FROM (%s) AS combined %s ORDER BY %s ASC) TO '%s' (%s)`,
		baseQuery, whereSQL, tsField, escapeSQLString(outputPath), copyOptions,
	)
	result, err := e.db.Exec(exportQuery)
	if err != nil {
		return 0, fmt.Errorf("export failed: %w", err)
	}
	count, _ := result.RowsAffected()
	return count, nil
}

// ExportFormat identifies how ExportFiltered will write a file.
type ExportFormat string

const (
	// ExportNDJSON writes one JSON object per line. Human-readable and
	// greppable; every field name repeats on every row.
	ExportNDJSON ExportFormat = "ndjson"
	// ExportParquet writes a columnar file with the schema in a footer.
	// Types survive the round trip, and it is typically several times
	// smaller because each column compresses against itself.
	ExportParquet ExportFormat = "parquet"
)

// ExportFormatFor picks the export format from the output filename.
// Anything that is not a Parquet extension writes NDJSON, which keeps
// the previous behaviour for every path that existed before.
func ExportFormatFor(path string) ExportFormat {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".parquet", ".pq":
		return ExportParquet
	default:
		return ExportNDJSON
	}
}

// --- Internal helpers ---

func (e *Engine) enabledTables() []string {
	var tables []string
	for _, id := range e.fileOrder {
		fi := e.files[id]
		if fi.Enabled {
			tables = append(tables, fi.TableName)
		}
	}
	return tables
}

// fetchTableColumns queries information_schema directly — called only on
// LoadFile to populate the cache. Runtime callers use e.tableCols.
func (e *Engine) fetchTableColumns(tableName string) ([]string, error) {
	rows, err := e.db.Query(fmt.Sprintf(
		"SELECT column_name FROM information_schema.columns WHERE table_name = '%s' ORDER BY ordinal_position",
		tableName))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			cols = append(cols, name)
		}
	}
	return cols, nil
}

// discoverStructSubPaths probes a column (given as a path of segments,
// usually just one to start) and returns the dotted sub-paths beneath it
// if it's a STRUCT, recursing into nested STRUCTs. Returns nil + nil for
// scalar columns, all-NULL columns, or empty tables — all are fine and
// just mean "nothing to add."
//
// Implementation note: we ask DuckDB at runtime via typeof + struct_keys
// rather than parsing the type string from DESCRIBE. This is robust to
// nested types and case-sensitivity quirks of DuckDB's information_schema.
func (e *Engine) discoverStructSubPaths(tableName string, segments []string) ([]string, error) {
	expr := pathExprFromSegments(segments)
	// Use a non-NULL row so all-NULL columns don't poison the probe.
	typeQ := fmt.Sprintf(
		`SELECT typeof(%s) FROM %s WHERE %s IS NOT NULL LIMIT 1`,
		expr, quoteIdent(tableName), expr,
	)
	var typeStr string
	if err := e.db.QueryRow(typeQ).Scan(&typeStr); err != nil {
		return nil, nil
	}
	if !strings.HasPrefix(strings.ToUpper(typeStr), "STRUCT") {
		return nil, nil
	}
	// struct_keys returns LIST<VARCHAR>. The driver maps that to
	// []interface{} which sql.Scan can't unmarshal into a string —
	// CAST to VARCHAR explicitly so we get a literal `[a, b, c]`
	// representation we can parse. to_json wraps in JSON type which
	// the driver also unwraps to []interface{}; CAST to VARCHAR
	// after to_json is the most reliable round-trip.
	keysQ := fmt.Sprintf(
		`SELECT CAST(to_json(struct_keys(%s)) AS VARCHAR) FROM %s WHERE %s IS NOT NULL LIMIT 1`,
		expr, quoteIdent(tableName), expr,
	)
	var keysJSON string
	if err := e.db.QueryRow(keysQ).Scan(&keysJSON); err != nil {
		return nil, nil
	}
	var keys []string
	if err := json.Unmarshal([]byte(keysJSON), &keys); err != nil {
		return nil, nil
	}
	var paths []string
	for _, k := range keys {
		childSegs := append(append([]string{}, segments...), k)
		path := strings.Join(childSegs, ".")
		paths = append(paths, path)
		// Recurse — nested STRUCTs get their leaves emitted too. Cap at
		// 6 levels to avoid pathological self-referential JSON.
		if len(childSegs) <= 6 {
			nested, _ := e.discoverStructSubPaths(tableName, childSegs)
			paths = append(paths, nested...)
		}
	}
	return paths, nil
}

// pathExprFromSegments quotes each segment and joins with dots — the
// DuckDB syntax for nested struct access. ["nodeinfo", "type"] →
// `"nodeinfo"."type"`. Single segment: ["@timestamp"] → `"@timestamp"`.
func pathExprFromSegments(segments []string) string {
	out := make([]string, len(segments))
	for i, s := range segments {
		out[i] = quoteIdent(s)
	}
	return strings.Join(out, ".")
}

// quoteFieldRef resolves a user-supplied field name to the correct
// DuckDB expression for use INSIDE the UNION ALL view. Order matters:
//
//  1. If the name matches a top-level column exactly (case-insensitive),
//     quote as a single identifier. Preserves legitimate dotted column
//     names like "app.name" that aren't STRUCT paths.
//  2. Else if it matches a discovered STRUCT sub-path, emit a
//     json_extract_string expression against the parent column.
//     buildUnionSelect projects struct columns through to_json() so
//     the column at this point is valid JSON text — json_extract_string
//     parses it on the fly and returns the requested sub-value.
//  3. Otherwise fall back to single-ident quoting. The downstream
//     query will then surface a DuckDB error referencing the missing
//     column, which is more informative than silently dropping the
//     filter.
//
// Looks across ALL loaded tables — the engine's UNION ALL means a
// field that exists in one table is queryable everywhere (other
// tables just contribute NULL for that column).
func (e *Engine) quoteFieldRef(name string) string {
	lower := strings.ToLower(name)
	// 1. Literal top-level column match.
	for _, cols := range e.tableCols {
		for _, c := range cols {
			if strings.EqualFold(c, lower) {
				return quoteIdent(c)
			}
		}
	}
	// 2. STRUCT sub-path match. Find the longest matching prefix that's
	// a real path (case-insensitive) and emit a JSON-path extraction.
	for _, paths := range e.tableStructPaths {
		for _, p := range paths {
			if strings.EqualFold(p, lower) {
				parts := strings.Split(p, ".")
				root := parts[0]
				jsonPath := "$." + strings.Join(parts[1:], ".")
				return fmt.Sprintf("json_extract_string(%s, '%s')", quoteIdent(root), jsonPath)
			}
		}
	}
	// 3. Unknown — fall through. DuckDB will error helpfully.
	return quoteIdent(name)
}

// canonicalColumns returns the merged set of column names across tables,
// folding case so that files which differ only in capitalization (e.g.
// `Message` from the block adapter, `message` from the line adapter) end
// up sharing a single canonical column in the union.
//
// Canonical name = the first casing seen across the tables in iteration
// order. This means whichever file loaded first dictates the user-visible
// name — predictable and stable, without surprising the operator with a
// lowercased rewrite of their data.
//
// Returns the canonical names in first-appearance order plus a
// lowercase→canonical lookup that buildUnionSelect uses to map each
// table's actual columns onto the canonical projection.
func (e *Engine) canonicalColumns(tables []string) ([]string, map[string]string) {
	byLower := make(map[string]string)
	var order []string
	for _, t := range tables {
		for _, c := range e.tableCols[t] {
			lower := strings.ToLower(c)
			if _, ok := byLower[lower]; !ok {
				byLower[lower] = c
				order = append(order, c)
			}
		}
	}
	return order, byLower
}

// allColumns returns the canonical column list across the given tables.
// Thin wrapper that drops the lookup map for callers that only need the
// names (GetFields).
func (e *Engine) allColumns(tables []string) []string {
	order, _ := e.canonicalColumns(tables)
	return order
}

// buildUnionSelect builds a SELECT clause for a table against the
// canonical column set. Each canonical column is projected from this
// table's matching column(s), case-insensitively. The result is always
// labelled AS the canonical name so the UNION ALL stays aligned.
//
// Missing columns are replaced with NULL (cast to VARCHAR). When a
// single table happens to have multiple columns that fold to the same
// canonical name (e.g. both `Message` AND `message` survived from JSON
// auto-detection), they're COALESCE'd so the first non-null value wins
// — losing nothing.
//
// Every selected column is cast to VARCHAR. Without this, UNION ALL
// across heterogeneous files forces DuckDB to pick one unified type
// per column, and mismatches (e.g. BIGINT in file A vs JSON-wrapping-
// a-string "0001" in file B for app.threadid) explode at query time
// with a JSON-cast error. Values are displayed as text in the UI
// anyway, so VARCHAR is lossless for the viewer's purposes. Structs
// and lists become their JSON text form via DuckDB's built-in
// formatter.
func (e *Engine) buildUnionSelect(tableName string, canonOrder []string, canonByLower map[string]string, addSourceTable bool) string {
	// Group this table's columns by their canonical form so we can spot
	// in-table case duplicates and COALESCE them.
	grouped := make(map[string][]string, len(e.tableCols[tableName]))
	for _, c := range e.tableCols[tableName] {
		canon := canonByLower[strings.ToLower(c)]
		grouped[canon] = append(grouped[canon], c)
	}
	// Top-level columns whose type is STRUCT in THIS table. Those need
	// to_json() instead of CAST AS VARCHAR — DuckDB's STRUCT→VARCHAR
	// cast emits non-JSON text (`{'k': 'v'}`, single-quoted strings)
	// which json_extract_string can't parse. to_json emits valid JSON
	// the WHERE-clause path-access expressions in quoteFieldRef can
	// then index into via json_extract_string(col, '$.path').
	structRoots := make(map[string]bool)
	for _, p := range e.tableStructPaths[tableName] {
		if i := strings.IndexByte(p, '.'); i > 0 {
			structRoots[strings.ToLower(p[:i])] = true
		}
	}
	textProjection := func(actual string) string {
		if structRoots[strings.ToLower(actual)] {
			return fmt.Sprintf("to_json(%s)", quoteIdent(actual))
		}
		return fmt.Sprintf("CAST(%s AS VARCHAR)", quoteIdent(actual))
	}

	parts := make([]string, 0, len(canonOrder)+1)
	for _, canon := range canonOrder {
		quotedCanon := quoteIdent(canon)
		actuals := grouped[canon]
		switch len(actuals) {
		case 0:
			parts = append(parts, fmt.Sprintf("CAST(NULL AS VARCHAR) AS %s", quotedCanon))
		case 1:
			parts = append(parts, fmt.Sprintf("%s AS %s", textProjection(actuals[0]), quotedCanon))
		default:
			args := make([]string, len(actuals))
			for i, a := range actuals {
				args[i] = textProjection(a)
			}
			parts = append(parts, fmt.Sprintf("COALESCE(%s) AS %s", strings.Join(args, ", "), quotedCanon))
		}
	}
	if addSourceTable {
		parts = append(parts, fmt.Sprintf("'%s' AS _source_table", tableName))
	}
	return fmt.Sprintf("SELECT %s FROM %s", strings.Join(parts, ", "), tableName)
}

// buildUnionQuery builds a UNION ALL query across enabled tables,
// aligning columns by canonical (case-insensitive) name so that tables
// with fewer fields get NULLs for the missing ones and tables with
// differently-cased equivalents merge into one column. Uses the
// cached schema — no SQL roundtrip.
func (e *Engine) buildUnionQuery(tables []string, addSourceTable bool) (string, []string) {
	canonOrder, canonByLower := e.canonicalColumns(tables)

	unionParts := make([]string, 0, len(tables))
	for _, t := range tables {
		unionParts = append(unionParts, e.buildUnionSelect(t, canonOrder, canonByLower, addSourceTable))
	}
	return strings.Join(unionParts, " UNION ALL "), canonOrder
}

func (e *Engine) quotedTimestampField() string {
	return quoteIdent(e.timestampField)
}

// resolveSortField returns the quoted SQL identifier for the ORDER BY
// column. Preference order: explicit request override if present in the
// union column set (case-insensitive — request may use a different
// casing than the canonical column) → engine-wide default → "@timestamp"
// literal. The columns in allCols are already VARCHAR-cast by
// buildUnionSelect, so lexicographic sort on ISO-8601 timestamp strings
// yields the right chronological order for both candidates.
func (e *Engine) resolveSortField(requested string, allCols []string) string {
	if requested != "" {
		for _, c := range allCols {
			if strings.EqualFold(c, requested) {
				return quoteIdent(c)
			}
		}
	}
	return e.quotedTimestampField()
}

// pickTimestampField picks the best timestamp column from a set of column
// names. Pure function — called from LoadFile with cached columns.
func pickTimestampField(columns []string) string {
	candidates := []string{"@timestamp", "timestamp", "time", "datetime", "date", "ts", "event_time", "created_at"}
	for _, candidate := range candidates {
		for _, col := range columns {
			if strings.EqualFold(col, candidate) {
				return col
			}
		}
	}
	for _, col := range columns {
		if isTimestampLike(col) {
			return col
		}
	}
	if len(columns) > 0 {
		return columns[0]
	}
	return "@timestamp"
}

// isTimestampLike returns true if a column name looks timestamp-ish.
func isTimestampLike(col string) bool {
	lower := strings.ToLower(col)
	return strings.Contains(lower, "time") || strings.Contains(lower, "date") || lower == "ts"
}

func (e *Engine) buildWhereClause(req QueryRequest, allCols []string) []string {
	var clauses []string
	tsField := e.quotedTimestampField()

	// Time range filter.
	//
	// Compared as timestamps, not as text. Every column in the union is
	// CAST to VARCHAR, so a bare string comparison here is lexicographic
	// against the rendered form `2026-05-20 10:00:00` — which means the
	// same instant written `2026-05-20T10:00:00Z` sorts *after* it (the
	// 'T' is above ' ') and the filter silently matches nothing. That
	// held together only while the bounds came from one input widget
	// emitting one shape; a bound can now arrive from a shared URL, a
	// pasted ISO-8601 value, or another tool.
	//
	// TRY_CAST rather than CAST: a heterogeneous union can carry a
	// column that is a timestamp in one file and free text in another,
	// and one such row must not fail the whole query. Those rows drop
	// out of a time range, which is correct — a row with no usable
	// instant is not inside any window.
	if req.TimeFrom != nil && *req.TimeFrom != "" {
		clauses = append(clauses, fmt.Sprintf("TRY_CAST(%s AS TIMESTAMP) >= TRY_CAST('%s' AS TIMESTAMP)",
			tsField, escapeSQLString(*req.TimeFrom)))
	}
	if req.TimeTo != nil && *req.TimeTo != "" {
		if len(clauses) > 0 {
			clauses = append(clauses, "AND")
		}
		clauses = append(clauses, fmt.Sprintf("TRY_CAST(%s AS TIMESTAMP) <= TRY_CAST('%s' AS TIMESTAMP)",
			tsField, escapeSQLString(*req.TimeTo)))
	}

	// Field filters
	for i, f := range req.Filters {
		if f.Field == "" {
			continue
		}
		// exists / does_not_exist take no value; every other operator
		// is silently dropped on empty value to keep half-typed filter
		// rows from poisoning the WHERE clause.
		if f.Value == "" && f.Operator != "exists" && f.Operator != "does_not_exist" {
			continue
		}
		condition := e.buildFilterCondition(f)
		if condition == "" {
			continue
		}
		if len(clauses) > 0 || i > 0 {
			logic := "AND"
			if f.Logic == "or" {
				logic = "OR"
			}
			clauses = append(clauses, logic)
		}
		clauses = append(clauses, condition)
	}

	// Free-text search (case-insensitive across all string-castable fields).
	// DuckDB's `combined.*` in a scalar context expands to an AND across columns,
	// so we emit an explicit OR across every column instead. Structs/arrays cast
	// to their text form, so nested values are searchable.
	if req.SearchText != "" && len(allCols) > 0 {
		if len(clauses) > 0 {
			clauses = append(clauses, "AND")
		}
		escaped := escapeSQLString(req.SearchText)
		parts := make([]string, 0, len(allCols))
		for _, c := range allCols {
			parts = append(parts, fmt.Sprintf("TRY_CAST(%s AS VARCHAR) ILIKE '%%%s%%'", quoteIdent(c), escaped))
		}
		clauses = append(clauses, "("+strings.Join(parts, " OR ")+")")
	}

	return clauses
}

// buildFilterCondition is a method (not a free function) so it can route
// the field name through quoteFieldRef — which knows whether the name
// should be quoted as one identifier or split into struct sub-path
// access. This is what makes `nodeinfo.type` resolve to the STRUCT
// sub-field instead of looking for a literal-named column.
func (e *Engine) buildFilterCondition(f Filter) string {
	field := e.quoteFieldRef(f.Field)
	value := escapeSQLString(f.Value)

	switch f.Operator {
	case "is":
		return fmt.Sprintf("LOWER(TRY_CAST(%s AS VARCHAR)) = LOWER('%s')", field, value)
	case "is_not":
		return fmt.Sprintf("LOWER(TRY_CAST(%s AS VARCHAR)) != LOWER('%s')", field, value)
	case "contains":
		return fmt.Sprintf("TRY_CAST(%s AS VARCHAR) ILIKE '%%%s%%'", field, value)
	case "not_contains":
		return fmt.Sprintf("TRY_CAST(%s AS VARCHAR) NOT ILIKE '%%%s%%'", field, value)
	case "wildcard":
		// Convert * to % for SQL LIKE, case insensitive
		pattern := strings.ReplaceAll(value, "*", "%")
		return fmt.Sprintf("TRY_CAST(%s AS VARCHAR) ILIKE '%s'", field, pattern)
	case "not_wildcard":
		pattern := strings.ReplaceAll(value, "*", "%")
		return fmt.Sprintf("TRY_CAST(%s AS VARCHAR) NOT ILIKE '%s'", field, pattern)
	case "exists":
		// "exists": the field is present AND non-empty.
		// Heterogeneous schemas mean missing columns are NULL after the
		// UNION ALL (engine fills them in buildUnionQuery). Also reject
		// the empty-string case so an explicit "" doesn't count as
		// present — matches Dashboards' behaviour for keyword fields.
		return fmt.Sprintf("(%s IS NOT NULL AND TRY_CAST(%s AS VARCHAR) != '')", field, field)
	case "does_not_exist":
		return fmt.Sprintf("(%s IS NULL OR TRY_CAST(%s AS VARCHAR) = '')", field, field)
	case "is_one_of":
		// "is one of": comma-separated value list.
		// Empty values are dropped; case-insensitive match.
		quoted := quotedValueList(f.Value)
		if quoted == "" {
			return ""
		}
		return fmt.Sprintf("LOWER(TRY_CAST(%s AS VARCHAR)) IN (%s)", field, quoted)
	case "is_not_one_of":
		quoted := quotedValueList(f.Value)
		if quoted == "" {
			return ""
		}
		return fmt.Sprintf("(LOWER(TRY_CAST(%s AS VARCHAR)) NOT IN (%s) OR %s IS NULL)", field, quoted, field)
	default:
		return ""
	}
}

// quotedValueList splits a comma-separated value list, trims each, drops
// empties, and emits a comma-joined list of escaped, lowercased SQL
// string literals for use in a `LOWER(field) IN (...)` clause.
// Returns "" when the list is empty (caller skips the filter).
func quotedValueList(raw string) string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, "LOWER('"+escapeSQLString(p)+"')")
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, ", ")
}

func escapeSQLString(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// quoteIdent wraps a DuckDB identifier (column or table name) in
// double quotes, doubling any embedded `"` so a JSON key like
// `a"; DROP TABLE x;--` can't terminate the quoted identifier and
// inject SQL. Always use this when interpolating a column name
// extracted from user data (every key in a loaded NDJSON, every
// field name on a user-supplied filter).
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func formatValue(v interface{}) interface{} {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case []byte:
		// Try to parse as JSON for nested objects
		var parsed interface{}
		if err := json.Unmarshal(val, &parsed); err == nil {
			return parsed
		}
		return string(val)
	case time.Time:
		return val.Format(time.RFC3339Nano)
	default:
		return val
	}
}
