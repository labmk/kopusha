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
