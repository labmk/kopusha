package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// The constants below identify one archive inside
// testdata/attestation-v0.2.2.json, which is the unmodified response
// GitHub's attestations API gave for the v0.2.2 release. It is here to
// catch the failure this package is most exposed to: a struct tag that
// does not match the shape GitHub actually sends. A synthetic fixture
// would agree with whatever this file claims and prove nothing.
//
// They name the project as it was before the rename, and must not be
// updated to match the current name. The statement was signed under the
// old repository and asset names, so those are what it says — for good,
// since a signature covers the bytes that existed when it was made. This
// is exactly the property the whole mechanism relies on, so the fixture
// is more useful preserved than tidied.
//
// It follows that the fixture cannot be checked against wantRepository:
// that constant describes the builds this binary will actually verify,
// which are 0.3.0 and later, all built under the new name.
const (
	realBundleAsset      = "obs_viewer-0.2.2-darwin-arm64.zip"
	realBundleDigest     = "1d5831d763fcb7b95f718367d15b4aa39892447cf451df724f3bc217eaa746e8"
	realBundleCommit     = "4d2ba4b41618f87c96c46739f3d37d8d7ee15963"
	realBundleRepository = "https://github.com/labmk/obs-viewer"
)

func TestVerifyReadsRealBundle(t *testing.T) {
	raw, err := os.ReadFile("testdata/attestation-v0.2.2.json")
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Attestations []struct {
			Bundle struct {
				DSSEEnvelope struct {
					Payload string `json:"payload"`
				} `json:"dsseEnvelope"`
			} `json:"bundle"`
		} `json:"attestations"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	stmtJSON, err := base64.StdEncoding.DecodeString(payload.Attestations[0].Bundle.DSSEEnvelope.Payload)
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := decodeStatement(stmtJSON)
	if err != nil {
		t.Fatal(err)
	}

	if stmt.PredicateType != wantPredicateType {
		t.Errorf("predicateType = %q, want %q", stmt.PredicateType, wantPredicateType)
	}
	wf := stmt.Predicate.BuildDefinition.ExternalParameters.Workflow
	if wf.Repository != realBundleRepository {
		t.Errorf("repository = %q, want %q", wf.Repository, realBundleRepository)
	}
	if wf.Path != wantWorkflowPath {
		t.Errorf("workflow path = %q, want %q", wf.Path, wantWorkflowPath)
	}
	if wf.Ref != "refs/tags/v0.2.2" {
		t.Errorf("ref = %q, want refs/tags/v0.2.2", wf.Ref)
	}
	if env := stmt.Predicate.BuildDefinition.InternalParameters.GitHub.RunnerEnvironment; env != wantRunnerEnv {
		t.Errorf("runner environment = %q, want %q", env, wantRunnerEnv)
	}
	if got := stmt.Predicate.BuildDefinition.ResolvedDependencies; len(got) == 0 || got[0].Digest.GitCommit != realBundleCommit {
		t.Errorf("source commit = %+v, want %s", got, realBundleCommit)
	}

	// One statement covers every platform archive in the release, so the
	// subject list must be searched by digest rather than assumed to hold
	// one entry.
	if len(stmt.Subject) < 2 {
		t.Fatalf("subject count = %d, want the whole release", len(stmt.Subject))
	}
	var found bool
	for _, s := range stmt.Subject {
		if s.Digest.SHA256 == realBundleDigest {
			found = true
			if s.Name != realBundleAsset {
				t.Errorf("name for digest = %q, want %q", s.Name, realBundleAsset)
			}
		}
	}
	if !found {
		t.Errorf("no subject carries digest %s", realBundleDigest)
	}
}

// serveStatement returns an Updater whose attestation endpoint answers
// with mutate() applied to a well-formed statement for the given bytes.
func serveStatement(t *testing.T, archive []byte, assetName string, mutate func(m map[string]any)) *Updater {
	t.Helper()
	sum := sha256.Sum256(archive)
	digest := hex.EncodeToString(sum[:])

	stmt := map[string]any{
		"predicateType": wantPredicateType,
		"subject": []any{
			map[string]any{"name": "kopusha-0.3.0-other-arch.zip", "digest": map[string]any{"sha256": strings.Repeat("a", 64)}},
			map[string]any{"name": assetName, "digest": map[string]any{"sha256": digest}},
		},
		"predicate": map[string]any{
			"buildDefinition": map[string]any{
				"externalParameters": map[string]any{
					"workflow": map[string]any{
						"ref":        "refs/tags/v0.3.0",
						"repository": wantRepository,
						"path":       wantWorkflowPath,
					},
				},
				"internalParameters": map[string]any{
					"github": map[string]any{"runner_environment": wantRunnerEnv},
				},
				"resolvedDependencies": []any{
					map[string]any{"digest": map[string]any{"gitCommit": realBundleCommit}},
				},
			},
		},
	}
	if mutate != nil {
		mutate(stmt)
	}
	body, err := json.Marshal(stmt)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The API keys on digest; anything else must 404 the way the real
		// one does.
		if !strings.HasSuffix(r.URL.Path, "sha256:"+digest) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		resp := map[string]any{"attestations": []any{map[string]any{
			"bundle": map[string]any{"dsseEnvelope": map[string]any{
				"payloadType": "application/vnd.in-toto+json",
				"payload":     base64.StdEncoding.EncodeToString(body),
			}},
		}}}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	return &Updater{
		Current:  "0.2.2",
		Endpoint: srv.URL + "/attestations/sha256:%s",
		Client:   srv.Client(),
	}
}

func TestVerifyAcceptsMatchingBuild(t *testing.T) {
	archive := []byte("pretend this is a zip")
	u := serveStatement(t, archive, "kopusha-0.3.0-test-arch.zip", nil)

	att, err := u.Verify(context.Background(), archive, "kopusha-0.3.0-test-arch.zip", "0.3.0")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if att.Commit != realBundleCommit {
		t.Errorf("commit = %q, want %q", att.Commit, realBundleCommit)
	}
	if att.WorkflowRef != "refs/tags/v0.3.0" {
		t.Errorf("ref = %q", att.WorkflowRef)
	}
}

// Tampering with a single byte changes the digest, so the lookup finds
// nothing. This is the case the whole check exists for.
func TestVerifyRejectsTamperedArchive(t *testing.T) {
	archive := []byte("pretend this is a zip")
	u := serveStatement(t, archive, "kopusha-0.3.0-test-arch.zip", nil)

	tampered := append(append([]byte{}, archive...), '!')
	_, err := u.Verify(context.Background(), tampered, "kopusha-0.3.0-test-arch.zip", "0.3.0")
	if !errors.Is(err, ErrNoAttestation) {
		t.Fatalf("err = %v, want ErrNoAttestation", err)
	}
}

func TestVerifyRejectsWrongClaims(t *testing.T) {
	archive := []byte("pretend this is a zip")
	asset := "kopusha-0.3.0-test-arch.zip"

	workflow := func(m map[string]any) map[string]any {
		return m["predicate"].(map[string]any)["buildDefinition"].(map[string]any)["externalParameters"].(map[string]any)["workflow"].(map[string]any)
	}

	cases := []struct {
		name    string
		version string
		mutate  func(m map[string]any)
		want    string
	}{
		{
			name:    "another repository",
			version: "0.3.0",
			mutate:  func(m map[string]any) { workflow(m)["repository"] = "https://github.com/someone/fork" },
			want:    "built from",
		},
		{
			name:    "another workflow in this repository",
			version: "0.3.0",
			mutate:  func(m map[string]any) { workflow(m)["path"] = ".github/workflows/pr.yml" },
			want:    "built by",
		},
		{
			// A branch build carries every other claim correctly, which
			// is precisely why the tag has to be checked.
			name:    "a branch build claiming to be the release",
			version: "0.3.0",
			mutate:  func(m map[string]any) { workflow(m)["ref"] = "refs/heads/main" },
			want:    "refs/tags/v0.3.0",
		},
		{
			name:    "a different version's tag",
			version: "0.4.0",
			mutate:  nil,
			want:    "refs/tags/v0.4.0",
		},
		{
			name:    "a self-hosted runner",
			version: "0.3.0",
			mutate: func(m map[string]any) {
				bd := m["predicate"].(map[string]any)["buildDefinition"].(map[string]any)
				bd["internalParameters"].(map[string]any)["github"].(map[string]any)["runner_environment"] = "self-hosted"
			},
			want: "self-hosted",
		},
		{
			name:    "the wrong predicate",
			version: "0.3.0",
			mutate:  func(m map[string]any) { m["predicateType"] = "https://example.invalid/other/v1" },
			want:    "predicate type",
		},
		{
			name:    "no source commit",
			version: "0.3.0",
			mutate: func(m map[string]any) {
				bd := m["predicate"].(map[string]any)["buildDefinition"].(map[string]any)
				bd["resolvedDependencies"] = []any{}
			},
			want: "no source commit",
		},
		{
			// Renaming an archive must not let it borrow the attestation
			// of the platform build it is impersonating.
			name:    "these bytes under another name",
			version: "0.3.0",
			mutate: func(m map[string]any) {
				m["subject"].([]any)[1].(map[string]any)["name"] = "kopusha-0.3.0-windows-amd64.zip"
			},
			want: "names these bytes",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := serveStatement(t, archive, asset, tc.mutate)
			_, err := u.Verify(context.Background(), archive, asset, tc.version)
			if err == nil {
				t.Fatal("accepted a build it should have rejected")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// A rate limit is not evidence against the archive and must not be
// reported as one — ErrNoAttestation means "these bytes were not built
// here", and 403 does not say that.
func TestVerifyDistinguishesRateLimitFromMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	u := &Updater{Current: "0.2.2", Endpoint: srv.URL + "/%s", Client: srv.Client()}
	_, err := u.Verify(context.Background(), []byte("x"), "a.zip", "0.3.0")
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrNoAttestation) {
		t.Errorf("a rate limit was reported as a missing attestation: %v", err)
	}
}
