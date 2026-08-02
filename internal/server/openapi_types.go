package server

// Wire types referenced by swag annotations so the generated OpenAPI
// spec carries real schemas instead of `object{}`. None of these are
// used directly in the handler bodies — they only exist so swag has
// something to point at.

// VersionResponse is the body of GET /api/version.
type VersionResponse struct {
	Version            string `json:"version"`
	OS                 string `json:"os"`
	Arch               string `json:"arch"`
	IdleTimeoutSeconds int    `json:"idle_timeout_seconds"`
	// Repository is the project's page. A destination for a person to
	// click, not something the binary fetches.
	Repository string `json:"repository" example:"https://github.com/labmk/obs-viewer"`
}

// UpdateResponse is the body of GET /api/update.
//
// Purely informational. obs-viewer never downloads or installs a
// release; url points at a page for a human to visit.
type UpdateResponse struct {
	// Current is the running version.
	Current string `json:"current" example:"0.1.0"`
	// Latest is the newest published release, absent if unknown.
	Latest string `json:"latest,omitempty" example:"0.2.0"`
	// Available is true only when latest is confidently newer.
	Available bool `json:"available"`
	// URL is the release page.
	URL string `json:"url,omitempty" example:"https://github.com/labmk/obs-viewer/releases/tag/v0.2.0"`
	// Checked is false until a check completes, so "unknown" is
	// distinguishable from "up to date".
	Checked bool `json:"checked"`
	// Enabled mirrors update_check in obs_viewer.conf.
	Enabled bool `json:"enabled"`
}

// ErrorResponse is the body of any non-2xx response.
type ErrorResponse struct {
	Error string `json:"error" example:"something failed"`
}

// StatusResponse is the body of any 2xx mutation response that has no
// other payload.
type StatusResponse struct {
	Status string `json:"status" example:"ok"`
}

// FileInfo is one entry in the FilesResponse list.
type FileInfo struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Records int64  `json:"records"`
	Enabled bool   `json:"enabled"`
	Format  string `json:"format"`
}

// FilesResponse is the body of GET /api/files.
type FilesResponse struct {
	Files          []FileInfo `json:"files"`
	TimestampField string     `json:"timestamp_field"`
}

// PathRequest is the body of POST /api/files/load and /api/files/load-dir.
type PathRequest struct {
	Path string `json:"path"`
}

// IDRequest is the body of POST /api/files/unload.
type IDRequest struct {
	ID string `json:"id"`
}

// ToggleRequest is the body of POST /api/files/toggle.
type ToggleRequest struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

// BrowseEntry is one entry in BrowseResponse.
type BrowseEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

// BrowseResponse is the body of GET /api/browse.
type BrowseResponse struct {
	CurrentPath string        `json:"current_path"`
	Entries     []BrowseEntry `json:"entries"`
	Drives      []string      `json:"drives,omitempty"`
	InZip       bool          `json:"in_zip,omitempty"`
}

// FilterClause matches engine.FilterClause but lives here so swag can
// generate it without pulling the engine package into the spec.
type FilterClause struct {
	Field      string `json:"field"`
	Operator   string `json:"operator"`
	Value      string `json:"value"`
	Combinator string `json:"combinator,omitempty"`
}

// QueryRequest is the body of POST /api/query.
type QueryRequest struct {
	Filters    []FilterClause `json:"filters"`
	TimeFrom   string         `json:"time_from"`
	TimeTo     string         `json:"time_to"`
	SortOrder  string         `json:"sort_order" enums:"asc,desc"`
	SortField  string         `json:"sort_field,omitempty"`
	SearchText string         `json:"search_text,omitempty"`
	Offset     int            `json:"offset"`
	Limit      int            `json:"limit"`
}

// QueryResponse is the body of POST /api/query.
type QueryResponse struct {
	Rows       []map[string]interface{} `json:"rows"`
	TotalCount int64                    `json:"total_count"`
	Fields     []string                 `json:"fields"`
	Offset     int                      `json:"offset"`
	Limit      int                      `json:"limit"`
}

// FieldsResponse is the body of GET /api/fields.
type FieldsResponse struct {
	Fields []string `json:"fields"`
}

// TimeRangeResponse is the body of GET /api/timerange.
type TimeRangeResponse struct {
	Min             string   `json:"min"`
	Max             string   `json:"max"`
	TimestampFields []string `json:"timestamp_fields"`
}

// TimestampFieldRequest is the body of POST /api/timestamp-field.
type TimestampFieldRequest struct {
	Field string `json:"field"`
}

// TimestampFieldResponse is the body of GET/POST /api/timestamp-field.
type TimestampFieldResponse struct {
	Field  string `json:"field"`
	Status string `json:"status,omitempty"`
}

// ExportRequest is the body of POST /api/export.
type ExportRequest struct {
	Query      QueryRequest `json:"query"`
	OutputPath string       `json:"output_path"`
}

// ExportResponse is the body of POST /api/export.
type ExportResponse struct {
	Status  string `json:"status"`
	Records int64  `json:"records"`
	Path    string `json:"path"`
}

// SelfCopyRequest is the body of POST /api/export/self-copy.
type SelfCopyRequest struct {
	TargetDir string `json:"target_dir"`
}

// SelfCopyResponse is the body of POST /api/export/self-copy.
type SelfCopyResponse struct {
	Status string `json:"status"`
	Path   string `json:"path"`
}

// SettingsBody is the body of GET/POST /api/settings.
type SettingsBody struct {
	LastDirectory    string `json:"last_directory"`
	AutoLoadPrevious bool   `json:"auto_load_previous"`
}

// LoadDirResponse is the body of POST /api/files/load-dir.
type LoadDirResponse struct {
	Status string   `json:"status"`
	Loaded []string `json:"loaded"`
	Errors []string `json:"errors"`
}

// ModuleManifest is one entry in ModulesResponse.
type ModuleManifest struct {
	ID     string                 `json:"id"`
	Tab    map[string]interface{} `json:"tab,omitempty"`
	Bundle string                 `json:"bundle,omitempty"`
	Style  string                 `json:"style,omitempty"`
	Config map[string]interface{} `json:"config,omitempty"`
}

// ModulesResponse is the body of GET /api/modules.
type ModulesResponse struct {
	Modules []ModuleManifest `json:"modules"`
}

// HistogramBucket is one bar in a HistogramResponse.
type HistogramBucket struct {
	// Start is the bucket's first instant, ISO-8601 UTC.
	Start string `json:"start"`
	Count int64  `json:"count"`
}

// HistogramResponse is the body of POST /api/histogram.
type HistogramResponse struct {
	Buckets []HistogramBucket `json:"buckets"`
	// IntervalSeconds is the bucket width chosen for this span.
	IntervalSeconds int64 `json:"interval_seconds" example:"300"`
	// Min and Max bound the buckets, so a drag-select can map a pixel
	// back to an instant without re-deriving the range.
	Min string `json:"min,omitempty"`
	Max string `json:"max,omitempty"`
	// Total sums the buckets. Lower than the query's total_count by
	// however many matching records have no usable timestamp.
	Total int64 `json:"total"`
	// Field is the timestamp column the buckets were built on.
	Field string `json:"field,omitempty" example:"@timestamp"`
}

// ProfileRequest is the body of POST /api/profile — a QueryRequest
// plus an optional field subset. Omitting `fields` profiles every
// column in the union.
type ProfileRequest struct {
	QueryRequest
	Fields []string `json:"fields,omitempty"`
}

// FieldProfileEntry is one field's summary in a ProfileResponse.
type FieldProfileEntry struct {
	Name string `json:"name"`
	// Present counts rows with a usable value — not NULL, not empty.
	// The same rows the `exists` operator selects.
	Present int64 `json:"present"`
	// Distinct is approximate (HyperLogLog). The question it answers is
	// "identifier or category?", which does not need the last digit.
	Distinct int64 `json:"distinct"`
	// Files and FilesTotal say how many enabled files declare the field
	// at all — a different fact from its fill rate, and often the more
	// useful one.
	Files      int `json:"files"`
	FilesTotal int `json:"files_total"`
}

// ProfileResponse is the body of POST /api/profile.
type ProfileResponse struct {
	Total  int64               `json:"total"`
	Fields []FieldProfileEntry `json:"fields"`
	// Truncated reports that the field list hit the cap.
	Truncated bool `json:"truncated"`
}

// FieldValuesRequest is the body of POST /api/profile/values.
type FieldValuesRequest struct {
	QueryRequest
	Field string `json:"field"`
	// Top caps how many values come back. Defaults to, and is capped
	// at, 50.
	Top int `json:"top,omitempty"`
}

// FieldValueEntry is one value and how often it occurs.
type FieldValueEntry struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}

// FieldValuesResponse is the body of POST /api/profile/values.
type FieldValuesResponse struct {
	Field string `json:"field"`
	// Total sums the returned values only, not the whole field — it is
	// the denominator for the shares shown beside them.
	Total  int64             `json:"total"`
	Values []FieldValueEntry `json:"values"`
}

// AdapterVerdict is one adapter's answer in a DiagnosisResponse.
type AdapterVerdict struct {
	// Name is the adapter name, e.g. "ndjson", "line".
	Name string `json:"name" example:"line"`
	// Score is what Detect returned: 0 declines, 100 is an exact magic
	// match. The highest score wins, ties broken by name.
	Score int `json:"score" example:"0"`
	// Reason is why, in one sentence.
	Reason string `json:"reason" example:"none of 5 rule(s) matched any of the first 40 non-blank lines: iso-bracket, dashdate-level"`
}

// DiagnosisResponse is the body of POST /api/files/explain.
type DiagnosisResponse struct {
	Path string `json:"path"`
	// Chosen is the adapter that would handle the file, empty when
	// nothing matched.
	Chosen    string           `json:"chosen" example:""`
	BestScore int              `json:"best_score" example:"0"`
	Adapters  []AdapterVerdict `json:"adapters"`
	// FirstLine is the first non-blank line as the parser sees it,
	// after BOM removal and CRLF folding.
	FirstLine string `json:"first_line" example:"2026-03-18T06:00:00 gateway[4179]: queue depth 2347"`
	// Notes are encoding traits that break matching invisibly.
	Notes []string `json:"notes"`
}

// RuleInfo is one entry in RulesResponse.
type RuleInfo struct {
	Name     string `json:"name" example:"iso-bracket"`
	Family   string `json:"family" example:"line"`
	Priority int    `json:"priority" example:"100"`
	File     string `json:"file" example:"20-line-iso-bracket.yaml"`
}

// RulesResponse is the body of GET /api/rules.
type RulesResponse struct {
	Rules []RuleInfo `json:"rules"`
	// Dir is the parsers.d directory the rules were read from.
	Dir string `json:"dir"`
}

// RuleSub is one timestamp substitution in a RuleDraft, applied to the
// captured text before the layout is used. Needed for formats Go's
// layout language cannot express, such as a colon before milliseconds.
type RuleSub struct {
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
}

// RuleDraft is a line-format rule being authored — the body of
// /api/rules/suggest and part of the preview and save requests.
type RuleDraft struct {
	Name     string `json:"name" example:"gateway-log"`
	Priority int    `json:"priority" example:"50"`
	// Parse must capture the timestamp as (?P<ts>...). Every other
	// named group becomes a field.
	Parse string `json:"parse"`
	// TsLayout is a Go time layout — the reference time spelled out,
	// e.g. 2006-01-02 15:04:05 — not a strftime pattern.
	TsLayout string `json:"ts_layout" example:"2006-01-02 15:04:05"`
	// TsAssumeTZ names the zone for timestamps that carry none.
	TsAssumeTZ string `json:"ts_assume_tz,omitempty" example:"Europe/Berlin"`
	// TsUseMtimeDate supplies the date from the file's modification
	// time, for formats carrying only a time of day.
	TsUseMtimeDate bool `json:"ts_use_mtime_date,omitempty"`
	// TsUseMtimeYear supplies only the year, for formats carrying month
	// and day but no year.
	TsUseMtimeYear bool      `json:"ts_use_mtime_year,omitempty"`
	TsRegexSubs    []RuleSub `json:"ts_regex_subs,omitempty"`
	// MessageField names the field non-matching lines are appended to.
	// Defaults to "message".
	MessageField string `json:"message_field,omitempty"`
	// Sample is the text the rule was inferred from; saved into the
	// rule file as a header comment.
	Sample string `json:"sample,omitempty"`
	// Warnings are advisory — an ambiguous date order, a missing year.
	Warnings []string `json:"warnings,omitempty"`
}

// SampleRequest is the body of POST /api/rules/suggest.
type SampleRequest struct {
	Sample string `json:"sample"`
}

// PreviewRequest is the body of POST /api/rules/preview.
type PreviewRequest struct {
	Rule   RuleDraft `json:"rule"`
	Sample string    `json:"sample"`
}

// PreviewLine is one sample line and what the rule did with it.
type PreviewLine struct {
	Text string `json:"text"`
	// Status is "parsed" when the line opened a record, or
	// "continuation" when it was appended to the previous record.
	Status string `json:"status" example:"parsed"`
	// TS is the raw captured timestamp text, before the layout runs.
	TS string `json:"ts,omitempty"`
	// TSError is why that text could not be turned into a timestamp.
	TSError string `json:"ts_error,omitempty"`
}

// PreviewResponse is the body of POST /api/rules/preview.
type PreviewResponse struct {
	// Fields are the capture names in regex order.
	Fields []string                 `json:"fields"`
	Rows   []map[string]interface{} `json:"rows"`
	Lines  []PreviewLine            `json:"lines"`
	// Parsed and Continuation count the sample lines by status.
	Parsed       int `json:"parsed"`
	Continuation int `json:"continuation"`
	// TimestampErrors counts parsed lines whose timestamp the layout
	// rejected — rows that would load with no time on them.
	TimestampErrors int      `json:"timestamp_errors"`
	Warnings        []string `json:"warnings,omitempty"`
	// Error is set when the rule itself is unusable, e.g. the regex
	// does not compile. Reported in-band because that is an ordinary
	// state while editing.
	Error string `json:"error,omitempty"`
}

// SaveRuleRequest is the body of POST /api/rules/save.
type SaveRuleRequest struct {
	Rule RuleDraft `json:"rule"`
	// Overwrite replaces an existing rule file of the same name.
	Overwrite bool `json:"overwrite"`
}

// SaveRuleResponse is the body of POST /api/rules/save.
type SaveRuleResponse struct {
	Status string `json:"status" example:"ok"`
	Path   string `json:"path"`
	File   string `json:"file" example:"50-line-gateway-log.yaml"`
	// Rules is how many line rules are loaded after the save.
	Rules int `json:"rules" example:"6"`
}
