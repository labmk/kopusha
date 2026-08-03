// Package update checks whether a newer kopusha release exists.
//
// It only ever *reports*. Nothing is downloaded, nothing is executed,
// and the running binary is never modified — replacing the executable in
// place needs signed releases to be safe, and that is deliberately not
// built yet. See docs/SELF_UPDATE_PROPOSAL.md.
//
// The check is enabled by default and switched off with `update_check =
// false` in kopusha.conf or `--no-update-check` on the command line.
// Because it is on by default it must be invisible when it cannot work:
//
//   - It runs in the background. Startup never waits for it.
//   - It has a short timeout. An unreachable network costs nothing.
//   - Failure is silent at normal verbosity. An air-gapped host must not
//     accumulate warnings for a feature it cannot use.
//   - Nothing about the host is transmitted. The request is a plain GET
//     to the GitHub releases API, which necessarily reveals the client's
//     IP address and User-Agent and nothing else. There is no telemetry,
//     no identifier, and no phone-home on any other event.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultEndpoint is GitHub's "latest release" API for this project.
// Overridable so tests can point at an httptest server.
const DefaultEndpoint = "https://api.github.com/repos/labmk/kopusha/releases/latest"

// RepoURL is the project's page, served to the UI so the header can
// link to it. Kept beside DefaultEndpoint because the two must name the
// same repository — a fork that changes one and not the other would
// offer updates from somewhere other than where its code lives.
//
// Note that nothing fetches this: it is a destination for a person to
// click, and the only request kopusha itself makes is the release
// check above.
const RepoURL = "https://github.com/labmk/kopusha"

// checkTimeout bounds the whole request. Generous enough for a slow link,
// short enough that a black-holed connection resolves well within the
// first inactivity window.
const checkTimeout = 8 * time.Second

// maxBody caps how much of the response we will read. The real payload is
// a few KB; this stops a hostile or misconfigured endpoint from feeding us
// an unbounded stream.
const maxBody = 1 << 20

// Status is the result of the most recent check, as served to the UI.
type Status struct {
	// Current is the running version.
	Current string `json:"current"`
	// Latest is the newest published release, empty if unknown.
	Latest string `json:"latest,omitempty"`
	// Available is true only when Latest is confidently newer.
	Available bool `json:"available"`
	// URL is the release page, for a human to visit. Never a download.
	URL string `json:"url,omitempty"`
	// Checked reports whether a check has completed at all, so the UI can
	// tell "no update" apart from "we don't know".
	Checked bool `json:"checked"`
	// Enabled mirrors the configuration, so the UI can stay quiet rather
	// than implying an update state it has no basis for.
	Enabled bool `json:"enabled"`
}

// Checker performs the check and caches its result for the process
// lifetime.
type Checker struct {
	Current  string
	Endpoint string
	Client   *http.Client
	Enabled  bool

	mu     sync.RWMutex
	status Status
}

// New returns a Checker for the running version.
func New(current string, enabled bool) *Checker {
	return &Checker{
		Current:  current,
		Endpoint: DefaultEndpoint,
		Enabled:  enabled,
		Client:   &http.Client{Timeout: checkTimeout},
		status:   Status{Current: current, Enabled: enabled},
	}
}

// Status returns the cached result. Safe to call before any check has run.
func (c *Checker) Status() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status
}

// Start runs one check in the background when enabled. It returns
// immediately; callers must not wait on it.
func (c *Checker) Start(ctx context.Context, logf func(string, ...any)) {
	if !c.Enabled {
		return
	}
	go func() {
		latest, url, err := c.fetch(ctx)
		if err != nil {
			// Deliberately not a warning. On an air-gapped host this is
			// the expected outcome on every single start.
			logf("update check: %v", err)
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		c.status.Latest = latest
		c.status.URL = url
		c.status.Available = Newer(c.Current, latest)
		c.status.Checked = true
	}()
}

// fetch performs the request and extracts the release tag and page URL.
func (c *Checker) fetch(ctx context.Context) (version, url string, err error) {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Endpoint, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "kopusha/"+c.Current)

	resp, err := c.Client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 403 here is almost always GitHub's unauthenticated rate limit.
		return "", "", fmt.Errorf("unexpected status %s", resp.Status)
	}

	var payload struct {
		TagName    string `json:"tag_name"`
		HTMLURL    string `json:"html_url"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&payload); err != nil {
		return "", "", err
	}
	if payload.Draft || payload.Prerelease {
		return "", "", fmt.Errorf("latest release is a draft or prerelease")
	}
	if payload.TagName == "" {
		return "", "", fmt.Errorf("release has no tag")
	}
	return strings.TrimPrefix(payload.TagName, "v"), payload.HTMLURL, nil
}

// Newer reports whether latest is a strictly greater version than
// current, comparing dotted numeric components left to right.
//
// Unparseable input reports false. Claiming an update on a version string
// we do not understand would nag the user with no way to make it stop —
// staying quiet is the safer failure.
func Newer(current, latest string) bool {
	cur, ok1 := parseVersion(current)
	lat, ok2 := parseVersion(latest)
	if !ok1 || !ok2 {
		return false
	}
	for i := 0; i < len(cur) && i < len(lat); i++ {
		if lat[i] != cur[i] {
			return lat[i] > cur[i]
		}
	}
	return len(lat) > len(cur)
}

// parseVersion splits a dotted version into numeric components. Any
// pre-release or build suffix is dropped: 1.2.3-rc1 compares as 1.2.3,
// which is acceptable because prereleases are filtered out before we get
// here.
func parseVersion(v string) ([]int, bool) {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "v"))
	if v == "" {
		return nil, false
	}
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}
