// Package selfupdate downloads a newer release and replaces the running
// binary with it, when the user asks for it.
//
// Three properties are structural rather than incidental, and changing any
// of them changes what this package is:
//
//   - **User-initiated, always.** Nothing here runs on a timer or at
//     startup. internal/update reports that a release exists; this package
//     acts only when a person presses a button.
//   - **Verify before writing.** The archive is checked against GitHub's
//     build attestation for its exact bytes before anything touches the
//     install directory. See Verify for what that establishes and what it
//     does not.
//   - **Never lose a user's work.** Config files are not replaced at all,
//     and a parser rule is replaced only when it is byte-identical to what
//     shipped. What was skipped is reported, not silently dropped.
//
// The reasoning behind all of it is in docs/SELF_UPDATE_PROPOSAL.md.
package selfupdate

import (
	"fmt"
	"net/http"
	"runtime"
	"time"
)

// downloadTimeout bounds the whole fetch. A release archive is tens of
// megabytes, and the user is watching, so this is generous but finite.
const downloadTimeout = 10 * time.Minute

// Updater performs one update. It holds no state between attempts: a
// failed update leaves nothing to reset.
type Updater struct {
	// Current is the running version, without a leading "v".
	Current string
	// InstallDir is the directory holding the executable, parsers.d/ and
	// the config files.
	InstallDir string
	// ExePath is the running executable, as it will be replaced.
	ExePath string
	// RulesDir is the parsers.d/ directory being merged into.
	RulesDir string
	// Shipped is the parser manifest this binary was built with. Empty
	// means the binary predates the manifest, in which case the merge
	// touches nothing — see MergeRules.
	Shipped map[string]string

	// Client, Endpoint and Assets exist so tests can substitute them.
	Client   *http.Client
	Endpoint string
	Assets   string
}

func (u *Updater) client() *http.Client {
	if u.Client != nil {
		return u.Client
	}
	return &http.Client{Timeout: downloadTimeout}
}

func (u *Updater) attestationEndpoint() string {
	if u.Endpoint != "" {
		return u.Endpoint
	}
	return DefaultAttestationEndpoint
}

// AssetName is the release archive for the running platform. Release
// archives are named kopusha-<version>-<os>-<arch>.zip, and GOOS/GOARCH
// are already the names used, so no mapping table is needed — which is
// worth keeping true, because a mapping table is a thing that silently
// goes stale when a platform is added.
func AssetName(version string) string {
	return fmt.Sprintf("kopusha-%s-%s-%s.zip", version, runtime.GOOS, runtime.GOARCH)
}
