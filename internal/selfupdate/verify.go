package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ErrNoAttestation is returned when GitHub holds no build attestation for
// the downloaded bytes. It is the single most important failure in this
// package: it means the archive was not produced by the release workflow,
// whatever else may be true about it.
var ErrNoAttestation = errors.New("no build attestation exists for these bytes")

// ErrManualRestart means the update is installed but this platform will
// not re-execute itself; the user has to start the new binary. See
// Restart on Windows.
var ErrManualRestart = errors.New("the update is installed — start kopusha again to use it")

// DefaultAttestationEndpoint is queried by artifact digest — the %s is a
// hex SHA-256. It is deliberately a different host from the release
// download (objects.githubusercontent.com), which is what gives the check
// its value; see Verify. Overridable so tests can point at an httptest
// server.
const DefaultAttestationEndpoint = "https://api.github.com/repos/labmk/kopusha/attestations/sha256:%s"

// Expected claims. A build that does not carry all of these is rejected
// even if an attestation exists, because "some workflow in some repository
// produced this" is not the question being asked.
const (
	wantPredicateType = "https://slsa.dev/provenance/v1"
	wantRepository    = "https://github.com/labmk/kopusha"
	wantWorkflowPath  = ".github/workflows/build.yml"
	wantRunnerEnv     = "github-hosted"
)

// maxAttestationBody caps the response. Real bundles are ~12 KB; four
// artifacts share one statement, so this does not grow with platforms.
const maxAttestationBody = 4 << 20

// Attestation is what the build claims about itself, after the claims have
// been checked against what this binary expects.
type Attestation struct {
	// Digest is the SHA-256 of the verified archive, hex.
	Digest string `json:"digest"`
	// Repository, WorkflowRef and WorkflowPath identify the build.
	Repository   string `json:"repository"`
	WorkflowRef  string `json:"workflow_ref"`
	WorkflowPath string `json:"workflow_path"`
	// Commit is the source revision the archive was built from. This is
	// the claim worth showing a person: it names a commit they can read.
	Commit string `json:"commit"`
	// RunnerEnvironment is "github-hosted" for a normal release. A
	// self-hosted runner would mean the build ran on a machine outside
	// GitHub's control.
	RunnerEnvironment string `json:"runner_environment"`
}

// Verify checks that GitHub holds a build attestation for exactly these
// bytes, and that the attestation describes the build this binary expects.
//
// # What this establishes, and what it does not
//
// The attestation is fetched by content digest. An archive that no release
// workflow produced has no attestation, and the API answers 404 — so an
// asset uploaded to a release by hand, or swapped after publication, is
// rejected here. That is the realistic attack against a project whose
// releases are built in CI, and it is the one this catches. The attestation
// also pins the source commit, so a malicious build has to exist as a
// public commit in this repository to produce one at all.
//
// What it does not do is verify the Sigstore signature on the bundle. The
// bundle's authenticity rests on TLS to api.github.com rather than on the
// certificate chain inside it. The gap is an adversary who can forge
// responses from api.github.com but not produce a valid signature — a
// CA-level compromise, essentially — and such an adversary can equally
// forge the download itself.
//
// Closing that gap means verifying the DSSE envelope against a pinned
// Fulcio root and a Rekor inclusion proof. That is the right thing to do
// and it is what `gh attestation verify` does; it is not done here because
// the library that does it correctly quadruples this project's build-time
// dependency graph. Anyone who wants the full check has it:
//
//	gh attestation verify kopusha-<version>-<platform>.zip --repo labmk/kopusha
//
// The UI says which of the two happened rather than reporting an
// unqualified "verified", and SECURITY.md states the boundary. Overstating
// this would be worse than not checking at all.
func (u *Updater) Verify(ctx context.Context, archive []byte, assetName, version string) (Attestation, error) {
	sum := sha256.Sum256(archive)
	digest := hex.EncodeToString(sum[:])

	bundle, err := u.fetchAttestation(ctx, digest)
	if err != nil {
		return Attestation{}, err
	}

	stmt, err := decodeStatement(bundle)
	if err != nil {
		return Attestation{}, err
	}

	// The statement covers every archive in the release, so find the one
	// whose digest matches what we downloaded. Matching on digest rather
	// than on name is the point: a renamed archive cannot borrow another
	// platform's attestation.
	var matched bool
	for _, s := range stmt.Subject {
		if strings.EqualFold(s.Digest.SHA256, digest) {
			if s.Name != assetName {
				return Attestation{}, fmt.Errorf(
					"attestation names these bytes %q, not %q", s.Name, assetName)
			}
			matched = true
			break
		}
	}
	if !matched {
		// The API keys on digest, so this means GitHub returned a bundle
		// that does not cover what we asked about.
		return Attestation{}, fmt.Errorf("attestation does not cover digest %s", digest)
	}

	if stmt.PredicateType != wantPredicateType {
		return Attestation{}, fmt.Errorf("unexpected predicate type %q", stmt.PredicateType)
	}

	wf := stmt.Predicate.BuildDefinition.ExternalParameters.Workflow
	if wf.Repository != wantRepository {
		return Attestation{}, fmt.Errorf("built from %q, not %s", wf.Repository, wantRepository)
	}
	if wf.Path != wantWorkflowPath {
		return Attestation{}, fmt.Errorf("built by %q, not %s", wf.Path, wantWorkflowPath)
	}
	// A release archive must come from the tag it claims to be. Without
	// this, an attestation from a branch build would satisfy everything
	// above.
	if wantRef := "refs/tags/v" + version; wf.Ref != wantRef {
		return Attestation{}, fmt.Errorf("built from %q, not %s", wf.Ref, wantRef)
	}

	env := stmt.Predicate.BuildDefinition.InternalParameters.GitHub.RunnerEnvironment
	if env != wantRunnerEnv {
		return Attestation{}, fmt.Errorf("built on a %s runner, not %s", env, wantRunnerEnv)
	}

	att := Attestation{
		Digest:            digest,
		Repository:        wf.Repository,
		WorkflowRef:       wf.Ref,
		WorkflowPath:      wf.Path,
		RunnerEnvironment: env,
	}
	for _, d := range stmt.Predicate.BuildDefinition.ResolvedDependencies {
		if d.Digest.GitCommit != "" {
			att.Commit = d.Digest.GitCommit
			break
		}
	}
	if att.Commit == "" {
		return Attestation{}, errors.New("attestation records no source commit")
	}
	return att, nil
}

// fetchAttestation retrieves the bundle for a digest. A 404 is the
// meaningful answer, not an error to paper over, so it gets its own type.
func (u *Updater) fetchAttestation(ctx context.Context, digest string) (json.RawMessage, error) {
	url := fmt.Sprintf(u.attestationEndpoint(), digest)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "kopusha/"+u.Current)

	resp, err := u.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch attestation: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, ErrNoAttestation
	default:
		// 403 here is almost always GitHub's unauthenticated rate limit.
		// It is not evidence against the archive, so it must not be
		// reported as one.
		return nil, fmt.Errorf("attestation lookup failed: %s", resp.Status)
	}

	var payload struct {
		Attestations []struct {
			Bundle struct {
				DSSEEnvelope struct {
					Payload     string `json:"payload"`
					PayloadType string `json:"payloadType"`
				} `json:"dsseEnvelope"`
			} `json:"bundle"`
		} `json:"attestations"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAttestationBody)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode attestation: %w", err)
	}
	if len(payload.Attestations) == 0 {
		return nil, ErrNoAttestation
	}
	env := payload.Attestations[0].Bundle.DSSEEnvelope
	if env.PayloadType != "application/vnd.in-toto+json" {
		return nil, fmt.Errorf("unexpected payload type %q", env.PayloadType)
	}
	raw, err := base64.StdEncoding.DecodeString(env.Payload)
	if err != nil {
		return nil, fmt.Errorf("decode attestation payload: %w", err)
	}
	return raw, nil
}

// statement is the subset of the in-toto SLSA provenance statement this
// package reads. Fields not checked are deliberately absent — decoding
// only what is asserted keeps the checks and the struct from drifting.
type statement struct {
	PredicateType string `json:"predicateType"`
	Subject       []struct {
		Name   string `json:"name"`
		Digest struct {
			SHA256 string `json:"sha256"`
		} `json:"digest"`
	} `json:"subject"`
	Predicate struct {
		BuildDefinition struct {
			ExternalParameters struct {
				Workflow struct {
					Ref        string `json:"ref"`
					Repository string `json:"repository"`
					Path       string `json:"path"`
				} `json:"workflow"`
			} `json:"externalParameters"`
			InternalParameters struct {
				GitHub struct {
					RunnerEnvironment string `json:"runner_environment"`
				} `json:"github"`
			} `json:"internalParameters"`
			ResolvedDependencies []struct {
				URI    string `json:"uri"`
				Digest struct {
					GitCommit string `json:"gitCommit"`
				} `json:"digest"`
			} `json:"resolvedDependencies"`
		} `json:"buildDefinition"`
	} `json:"predicate"`
}

func decodeStatement(raw json.RawMessage) (statement, error) {
	var s statement
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, fmt.Errorf("decode in-toto statement: %w", err)
	}
	if len(s.Subject) == 0 {
		return s, errors.New("attestation statement has no subject")
	}
	return s, nil
}
