module github.com/labmk/obs-viewer

// Require Go 1.25.6+ — this pulls the 2026-01-15 stdlib release that
// fixes CVE-2025-61728 (archive/zip name-indexing DoS) and
// CVE-2025-61726 (net/http form-parse DoS), both of which our zip
// handlers and HTTP layer feed directly. Plus four more stdlib fixes
// from the same release.
//
// `toolchain go1.26.5` pins the build toolchain to the release that
// fixes the four stdlib advisories govulncheck flags as reachable on
// 1.26.2: GO-2026-5856 (crypto/tls ECH privacy leak, reachable via
// ListenAndServeTLS on the --cert/--key path), GO-2026-5039
// (net/textproto), GO-2026-5037 (crypto/x509 hostname parsing), and
// GO-2026-4971 (net.Listen NUL-byte panic on Windows, which main.go
// hits directly). `go 1.25.6` stays as the language-compat floor so
// downstream builds on any 1.25.6+ keep working — the toolchain line
// only constrains the build environment.
go 1.25.6

toolchain go1.26.5

require (
	github.com/0xrawsec/golang-evtx v1.2.9
	github.com/duckdb/duckdb-go/v2 v2.10505.0
	github.com/swaggo/swag v1.16.6
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/0xrawsec/golang-utils v1.3.0 // indirect
	github.com/KyleBanks/depth v1.2.1 // indirect
	github.com/apache/arrow-go/v18 v18.5.1 // indirect
	github.com/duckdb/duckdb-go-bindings v0.10505.0 // indirect
	github.com/duckdb/duckdb-go-bindings/lib/darwin-amd64 v0.10505.0 // indirect
	github.com/duckdb/duckdb-go-bindings/lib/darwin-arm64 v0.10505.0 // indirect
	github.com/duckdb/duckdb-go-bindings/lib/linux-amd64 v0.10505.0 // indirect
	github.com/duckdb/duckdb-go-bindings/lib/linux-arm64 v0.10505.0 // indirect
	github.com/duckdb/duckdb-go-bindings/lib/windows-amd64 v0.10505.0 // indirect
	github.com/go-openapi/jsonpointer v0.19.5 // indirect
	github.com/go-openapi/jsonreference v0.20.0 // indirect
	github.com/go-openapi/spec v0.20.6 // indirect
	github.com/go-openapi/swag v0.19.15 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/goccy/go-json v0.10.5 // indirect
	github.com/google/flatbuffers v25.12.19+incompatible // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/klauspost/compress v1.18.3 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/mailru/easyjson v0.7.6 // indirect
	github.com/pierrec/lz4/v4 v4.1.25 // indirect
	github.com/zeebo/xxh3 v1.1.0 // indirect
	golang.org/x/exp v0.0.0-20260112195511-716be5621a96 // indirect
	golang.org/x/mod v0.32.0 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/telemetry v0.0.0-20260116145544-c6413dc483f5 // indirect
	golang.org/x/tools v0.41.0 // indirect
	golang.org/x/xerrors v0.0.0-20240903120638-7835f813f4da // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
)
