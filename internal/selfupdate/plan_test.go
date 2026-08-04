package selfupdate

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const newVersion = "0.4.0"

// buildArchive produces a release zip with the same flat layout the real
// one has: the binary at the root, rules under parsers.d/.
func buildArchive(t *testing.T, binary string, rules map[string]string, extra map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	write(binaryName(), binary)
	write("kopusha.conf", "# the shipped config, which must never be installed over yours\n")
	for name, body := range rules {
		write("parsers.d/"+name, body)
	}
	for name, body := range extra {
		write(name, body)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// releaseServer serves one archive plus a matching attestation, the way
// GitHub does: the asset under a tag path, the attestation keyed by the
// archive's digest.
func releaseServer(t *testing.T, archive []byte, assetName string) *httptest.Server {
	t.Helper()
	sum := sha256.Sum256(archive)
	digest := hex.EncodeToString(sum[:])

	stmt := map[string]any{
		"predicateType": wantPredicateType,
		"subject": []any{
			map[string]any{"name": assetName, "digest": map[string]any{"sha256": digest}},
		},
		"predicate": map[string]any{"buildDefinition": map[string]any{
			"externalParameters": map[string]any{"workflow": map[string]any{
				"ref": "refs/tags/v" + newVersion, "repository": wantRepository, "path": wantWorkflowPath,
			}},
			"internalParameters": map[string]any{"github": map[string]any{"runner_environment": wantRunnerEnv}},
			"resolvedDependencies": []any{
				map[string]any{"digest": map[string]any{"gitCommit": realBundleCommit}},
			},
		}},
	}
	body, err := json.Marshal(stmt)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/attestations/"):
			if !strings.HasSuffix(r.URL.Path, digest) {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"attestations": []any{map[string]any{
				"bundle": map[string]any{"dsseEnvelope": map[string]any{
					"payloadType": "application/vnd.in-toto+json",
					"payload":     base64.StdEncoding.EncodeToString(body),
				}},
			}}})
		case strings.HasSuffix(r.URL.Path, assetName):
			_, _ = w.Write(archive)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// install lays out a temp install directory the way a real one looks.
func install(t *testing.T, rules map[string]string, shippedAs map[string]string) *Updater {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, binaryName())
	if err := os.WriteFile(exe, []byte("the old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "kopusha.conf"), []byte("listen = 127.0.0.1\n# edited by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rulesDir := filepath.Join(dir, "parsers.d")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range rules {
		if err := os.WriteFile(filepath.Join(rulesDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	shipped := map[string]string{}
	for name, body := range shippedAs {
		shipped[name] = hashBytes([]byte(body))
	}
	return &Updater{
		Current: "0.3.0", InstallDir: dir, ExePath: exe, RulesDir: rulesDir, Shipped: shipped,
	}
}

func point(u *Updater, srv *httptest.Server) {
	u.Client = srv.Client()
	u.Assets = srv.URL
	u.Endpoint = srv.URL + "/attestations/sha256:%s"
}

func TestUpdateEndToEnd(t *testing.T) {
	u := install(t,
		map[string]string{
			"10-untouched.yaml": "shipped body",
			"20-edited.yaml":    "the user fixed a regex",
			"90-mine.yaml":      "the user's own rule",
		},
		map[string]string{
			"10-untouched.yaml": "shipped body",
			"20-edited.yaml":    "shipped body",
		},
	)
	asset := AssetName(newVersion)
	archive := buildArchive(t, "the new binary", map[string]string{
		"10-untouched.yaml": "a newer body",
		"20-edited.yaml":    "a newer body",
		"60-new.yaml":       "a format this release adds",
	}, nil)
	point(u, releaseServer(t, archive, asset))

	plan, err := u.Prepare(context.Background(), newVersion)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if plan.Attestation.Commit != realBundleCommit {
		t.Errorf("commit = %q", plan.Attestation.Commit)
	}
	if plan.To != newVersion || plan.From != "0.3.0" {
		t.Errorf("plan = %s → %s", plan.From, plan.To)
	}

	// Nothing may have been written yet.
	if body, _ := os.ReadFile(u.ExePath); string(body) != "the old binary" {
		t.Fatalf("Prepare wrote the binary: %q", body)
	}

	res, err := u.Apply(plan)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if body, _ := os.ReadFile(u.ExePath); string(body) != "the new binary" {
		t.Errorf("binary = %q, want the new one", body)
	}
	if body, _ := os.ReadFile(res.Backup); string(body) != "the old binary" {
		t.Errorf("backup = %q, want the old binary", body)
	}

	// The config is never replaced, even though the archive carries one.
	conf, _ := os.ReadFile(filepath.Join(u.InstallDir, "kopusha.conf"))
	if !strings.Contains(string(conf), "edited by hand") {
		t.Errorf("the user's config was overwritten: %q", conf)
	}

	for name, want := range map[string]string{
		"10-untouched.yaml": "a newer body",               // replaced
		"20-edited.yaml":    "the user fixed a regex",     // kept
		"90-mine.yaml":      "the user's own rule",        // untouched
		"60-new.yaml":       "a format this release adds", // added
	} {
		body, err := os.ReadFile(filepath.Join(u.RulesDir, name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if string(body) != want {
			t.Errorf("%s = %q, want %q", name, body, want)
		}
	}

	if len(res.RulesKept) != 1 || res.RulesKept[0].Name != "20-edited.yaml" {
		t.Errorf("kept = %+v, want just the edited rule", res.RulesKept)
	}
	if res.RulesKept[0].Reason == "" {
		t.Error("the report does not say why the rule was kept")
	}
}

// The mode bit has to survive, or the new binary cannot be run.
//
// Unix only, because the thing being asserted does not exist elsewhere:
// NTFS has no executable bit, Go reports every file as -rw-rw-rw-, and
// what makes a Windows binary runnable is the .exe extension. Copying the
// old file's mode across is still correct there, it is simply not
// observable — so asserting it would be testing the filesystem's
// behaviour rather than ours.
func TestApplyPreservesTheExecutableBit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no executable bit on this platform")
	}
	u := install(t, nil, nil)
	asset := AssetName(newVersion)
	point(u, releaseServer(t, buildArchive(t, "the new binary", nil, nil), asset))

	plan, err := u.Prepare(context.Background(), newVersion)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := u.Apply(plan); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(u.ExePath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("mode = %v, want the executable bit set", fi.Mode().Perm())
	}
}

func TestRollbackRestoresThePreviousBinary(t *testing.T) {
	u := install(t, nil, nil)
	point(u, releaseServer(t, buildArchive(t, "the new binary", nil, nil), AssetName(newVersion)))

	plan, err := u.Prepare(context.Background(), newVersion)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := u.Apply(plan); err != nil {
		t.Fatal(err)
	}
	if err := Rollback(u.ExePath); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if body, _ := os.ReadFile(u.ExePath); string(body) != "the old binary" {
		t.Errorf("after rollback binary = %q", body)
	}
}

// An archive whose bytes have no attestation must never reach the disk.
func TestPrepareRejectsAnUnattestedArchive(t *testing.T) {
	u := install(t, nil, nil)
	asset := AssetName(newVersion)
	good := buildArchive(t, "the new binary", nil, nil)
	srv := releaseServer(t, good, asset)
	point(u, srv)

	// Serve different bytes than the ones the attestation covers.
	u.Assets = srv.URL + "/tampered"
	tampered := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(append(append([]byte{}, good...), '!'))
	}))
	defer tampered.Close()
	u.Assets = tampered.URL

	_, err := u.Prepare(context.Background(), newVersion)
	if !errors.Is(err, ErrNoAttestation) {
		t.Fatalf("err = %v, want ErrNoAttestation", err)
	}
	if body, _ := os.ReadFile(u.ExePath); string(body) != "the old binary" {
		t.Errorf("a rejected update still wrote the binary: %q", body)
	}
}

// A zip whose member names climb out of the target directory must be
// refused rather than extracted.
func TestArchiveRejectsPathTraversal(t *testing.T) {
	for _, name := range []string{"../escaped.yaml", "parsers.d/../../escaped.yaml", "/etc/passwd"} {
		archive := buildArchive(t, "the new binary", nil, map[string]string{name: "hostile"})
		a, err := openArchive(archive)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.rules(); err == nil {
			t.Errorf("%q was accepted", name)
		}
	}
}

// On a locked-down Windows install the directory is frequently read-only.
// That has to fail immediately, with a message naming the directory,
// rather than after a 60 MB download.
//
// The irony of skipping this on Windows is not lost, but the alternative
// is worse. Chmod does not restrict a directory on Windows — it changes
// the read-only attribute of the entry, which does not stop files being
// created inside it — so the precondition is never actually established
// and the test passes for the wrong reason or fails for a third one.
// Establishing it properly means driving icacls against an ACL that
// varies with how the runner is provisioned.
//
// What is being tested is not platform-specific anyway: writable()
// probes by creating a real temporary file, which is the one check that
// answers the question on every platform rather than inferring it from
// metadata. That is precisely why it is written that way.
func TestPrepareFailsEarlyOnAReadOnlyInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a read-only directory cannot be created with chmod here")
	}
	u := install(t, nil, nil)
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	point(u, srv)

	if err := os.Chmod(u.InstallDir, 0o555); err != nil {
		t.Skipf("cannot make the directory read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(u.InstallDir, 0o755) })

	_, err := u.Prepare(context.Background(), newVersion)
	if !errors.Is(err, ErrNotWritable) {
		t.Fatalf("err = %v, want ErrNotWritable", err)
	}
	if !strings.Contains(err.Error(), u.InstallDir) {
		t.Errorf("err = %q, want it to name the directory", err)
	}
	if hits != 0 {
		t.Errorf("downloaded %d times before checking writability", hits)
	}
}

func TestSweepBackupRemovesTheOldBinary(t *testing.T) {
	u := install(t, nil, nil)
	point(u, releaseServer(t, buildArchive(t, "the new binary", nil, nil), AssetName(newVersion)))

	plan, err := u.Prepare(context.Background(), newVersion)
	if err != nil {
		t.Fatal(err)
	}
	res, err := u.Apply(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !SweepBackup(u.ExePath) {
		t.Fatal("SweepBackup reported nothing removed")
	}
	if _, err := os.Stat(res.Backup); !os.IsNotExist(err) {
		t.Errorf("backup survived: %v", err)
	}
	if SweepBackup(u.ExePath) {
		t.Error("SweepBackup claimed a second removal")
	}
}

// buildArchiveWithSamples adds a samples/ folder to the release zip.
func buildArchiveWithSamples(t *testing.T, samples map[string]string) []byte {
	t.Helper()
	extra := map[string]string{}
	for name, body := range samples {
		extra["samples/"+name] = body
	}
	return buildArchive(t, "the new binary", nil, extra)
}

// The case this exists for: 0.3.0 shipped no samples/, so an install that
// self-updated into 0.3.1 had no folder to update. Honouring "absent
// means you deleted it" — right for a parser rule — would mean those
// installs never receive samples at all.
func TestUpdateCreatesSamplesWhenTheInstallHasNone(t *testing.T) {
	u := install(t, nil, nil)
	u.SamplesDir = filepath.Join(u.InstallDir, "samples")

	archive := buildArchiveWithSamples(t, map[string]string{
		"sample.ndjson": `{"ts":"2026-01-01T00:00:00Z"}`,
		"unmatched.log": "nothing parses this",
		"README.md":     "what these are",
	})
	point(u, releaseServer(t, archive, AssetName(newVersion)))

	plan, err := u.Prepare(context.Background(), newVersion)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Samples.Created {
		t.Error("plan does not report that samples/ will be created")
	}
	if len(plan.Samples.Write) != 3 {
		t.Errorf("write = %v, want all three", plan.Samples.Write)
	}
	// Still nothing on disk after Prepare.
	if _, err := os.Stat(u.SamplesDir); !os.IsNotExist(err) {
		t.Fatalf("Prepare created samples/: %v", err)
	}

	res, err := u.Apply(plan)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"sample.ndjson": `{"ts":"2026-01-01T00:00:00Z"}`,
		"unmatched.log": "nothing parses this",
		"README.md":     "what these are",
	} {
		body, err := os.ReadFile(filepath.Join(u.SamplesDir, name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if string(body) != want {
			t.Errorf("%s = %q, want %q", name, body, want)
		}
	}
	if len(res.SamplesWritten) != 3 {
		t.Errorf("report says %v, want three files", res.SamplesWritten)
	}
}

// A shipped sample is overwritten — samples are demo data, and a newer
// release's version of one is the one worth having.
func TestUpdateOverwritesShippedSamplesButKeepsOthers(t *testing.T) {
	u := install(t, nil, nil)
	u.SamplesDir = filepath.Join(u.InstallDir, "samples")
	if err := os.MkdirAll(u.SamplesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"sample.ndjson":    "the old sample",
		"my-own-notes.txt": "a file the user dropped in here",
	} {
		if err := os.WriteFile(filepath.Join(u.SamplesDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	archive := buildArchiveWithSamples(t, map[string]string{"sample.ndjson": "the new sample"})
	point(u, releaseServer(t, archive, AssetName(newVersion)))

	plan, err := u.Prepare(context.Background(), newVersion)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Samples.Created {
		t.Error("plan claims to create a directory that already exists")
	}
	if _, err := u.Apply(plan); err != nil {
		t.Fatal(err)
	}

	if body, _ := os.ReadFile(filepath.Join(u.SamplesDir, "sample.ndjson")); string(body) != "the new sample" {
		t.Errorf("shipped sample = %q, want the new one", body)
	}
	// Anything the release does not carry is strictly untouched, which is
	// the one guarantee that stops samples/ being a folder you cannot use.
	body, err := os.ReadFile(filepath.Join(u.SamplesDir, "my-own-notes.txt"))
	if err != nil {
		t.Fatalf("the user's own file was removed: %v", err)
	}
	if string(body) != "a file the user dropped in here" {
		t.Errorf("the user's own file changed: %q", body)
	}
}

// A release carrying no samples/ must not disturb the folder.
func TestUpdateLeavesSamplesAloneWhenTheReleaseHasNone(t *testing.T) {
	u := install(t, nil, nil)
	u.SamplesDir = filepath.Join(u.InstallDir, "samples")
	if err := os.MkdirAll(u.SamplesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(u.SamplesDir, "kept.log"), []byte("still here"), 0o644); err != nil {
		t.Fatal(err)
	}
	point(u, releaseServer(t, buildArchive(t, "the new binary", nil, nil), AssetName(newVersion)))

	plan, err := u.Prepare(context.Background(), newVersion)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Samples.Write) != 0 {
		t.Errorf("write = %v, want nothing", plan.Samples.Write)
	}
	if _, err := u.Apply(plan); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(filepath.Join(u.SamplesDir, "kept.log")); string(body) != "still here" {
		t.Errorf("kept.log = %q", body)
	}
}

// A caller that ships no samples opts out by leaving SamplesDir empty,
// and must not have a folder invented next to its binary.
func TestUpdateSkipsSamplesWhenTheDirIsUnset(t *testing.T) {
	u := install(t, nil, nil)
	u.SamplesDir = ""
	point(u, releaseServer(t, buildArchiveWithSamples(t, map[string]string{"a.log": "x"}), AssetName(newVersion)))

	plan, err := u.Prepare(context.Background(), newVersion)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Samples.Write) != 0 || plan.Samples.Created {
		t.Fatalf("samples = %+v, want nothing", plan.Samples)
	}
	if _, err := u.Apply(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(u.InstallDir, "samples")); !os.IsNotExist(err) {
		t.Error("a samples/ folder was created despite being unset")
	}
}
