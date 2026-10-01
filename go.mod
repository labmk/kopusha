module github.com/labmk/kopusha

// Require Go 1.26.6+ — Go 1.25 left upstream support when Go 1.27
// shipped (August 2026), and 1.26.6 is the first 1.26 release that
// fixes the six stdlib advisories govulncheck flags as reachable on
// 1.26.5: GO-2026-6218 (net/url quadratic resolvePath), GO-2026-6090
// (crypto/tls post-handshake messages), GO-2026-6089 (net/http h2c
// ReadHeaderTimeout), GO-2026-6088 (encoding/xml recursion depth, hit
// by the XML loader), GO-2026-5972 (encoding/asn1 recursion depth, on
// the --cert/--key path) and GO-2026-5026 (punycode labels, reached via
// the self-update fetch).
//
// `toolchain go1.26.8` pins the build environment to the newest 1.26
// patch release; the `go` line stays the floor for downstream builds.
go 1.26.6

toolchain go1.26.8

require (
	github.com/Velocidex/ordereddict v0.0.0-20210502082334-cf5d9045c0d1
	github.com/duckdb/duckdb-go/v2 v2.10505.0
	github.com/swaggo/swag v1.16.6
	gopkg.in/yaml.v3 v3.0.1
	www.velocidex.com/golang/evtx v0.2.0
)

require (
	github.com/KyleBanks/depth v1.2.1 // indirect
	github.com/Velocidex/pkcs7 v0.0.0-20210524015001-8d1eee94a157 // indirect
	github.com/apache/arrow-go/v18 v18.5.1 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
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
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/klauspost/compress v1.18.3 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/mailru/easyjson v0.7.6 // indirect
	github.com/pierrec/lz4/v4 v4.1.25 // indirect
	github.com/pkg/errors v0.8.1 // indirect
	github.com/zeebo/xxh3 v1.1.0 // indirect
	golang.org/x/exp v0.0.0-20260112195511-716be5621a96 // indirect
	golang.org/x/mod v0.32.0 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/telemetry v0.0.0-20260116145544-c6413dc483f5 // indirect
	golang.org/x/text v0.33.0 // indirect
	golang.org/x/tools v0.41.0 // indirect
	golang.org/x/xerrors v0.0.0-20240903120638-7835f813f4da // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	www.velocidex.com/golang/binparsergen v0.1.1-0.20201101234514-bbdb29f9ee31 // indirect
	www.velocidex.com/golang/go-pe v0.1.1-0.20211006062218-8f6d1ad6b2d5 // indirect
)
