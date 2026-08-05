package selfupdate

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// DefaultAssets is the release download prefix. The full URL is
// deterministic from the tag and the asset name, so no second API call is
// needed to discover it.
const DefaultAssets = "https://github.com/labmk/kopusha/releases/download"

// maxArchive caps the download. A release archive is ~60 MB; this leaves
// generous headroom while still refusing an endpoint that streams
// forever.
const maxArchive = 300 << 20

// maxEntry caps a single file extracted from the archive, so a small zip
// claiming an enormous member cannot exhaust memory or disk.
const maxEntry = 200 << 20

func (u *Updater) assetsBase() string {
	if u.Assets != "" {
		return u.Assets
	}
	return DefaultAssets
}

// download fetches the release archive into memory. It is held in memory
// rather than streamed to disk because it must be hashed and verified
// before anything is written — writing first and checking after is the
// ordering this package exists to avoid.
func (u *Updater) download(ctx context.Context, version, asset string) ([]byte, error) {
	url := fmt.Sprintf("%s/v%s/%s", u.assetsBase(), version, asset)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "kopusha/"+u.Current)

	resp, err := u.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", asset, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("release %s has no archive for this platform (%s)", version, asset)
		}
		return nil, fmt.Errorf("download %s: %s", asset, resp.Status)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxArchive+1))
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", asset, err)
	}
	if len(data) > maxArchive {
		return nil, fmt.Errorf("download %s: archive exceeds %d bytes", asset, maxArchive)
	}
	return data, nil
}

// archive is a verified release archive, opened for reading.
type archive struct {
	zip *zip.Reader
}

func openArchive(data []byte) (*archive, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	return &archive{zip: zr}, nil
}

// safeName rejects any archive member that would write outside the
// directory it is extracted into: absolute paths, drive letters, and any
// path that climbs with "..". The archive is verified before this runs,
// so this is defence in depth rather than the primary control — but an
// extractor that trusts its input is a bug waiting for the day the
// verification is loosened.
func safeName(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("archive entry has no name")
	}
	clean := path.Clean(strings.ReplaceAll(name, `\`, "/"))
	if path.IsAbs(clean) || strings.HasPrefix(clean, "../") || clean == ".." {
		return "", fmt.Errorf("archive entry %q escapes the target directory", name)
	}
	if len(clean) > 1 && clean[1] == ':' {
		return "", fmt.Errorf("archive entry %q is an absolute Windows path", name)
	}
	return clean, nil
}

// read returns one member's contents by exact name, or ok=false.
func (a *archive) read(name string) ([]byte, bool, error) {
	for _, f := range a.zip.File {
		clean, err := safeName(f.Name)
		if err != nil {
			return nil, false, err
		}
		if clean != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, false, fmt.Errorf("read %s: %w", name, err)
		}
		defer rc.Close()
		data, err := io.ReadAll(io.LimitReader(rc, maxEntry+1))
		if err != nil {
			return nil, false, fmt.Errorf("read %s: %w", name, err)
		}
		if len(data) > maxEntry {
			return nil, false, fmt.Errorf("archive entry %s is implausibly large", name)
		}
		return data, true, nil
	}
	return nil, false, nil
}

// rules returns every parsers.d/ member, keyed by base name.
func (a *archive) rules() (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, f := range a.zip.File {
		clean, err := safeName(f.Name)
		if err != nil {
			return nil, err
		}
		dir, base := path.Split(clean)
		if dir != "parsers.d/" || base == "" || !isRuleFile(base) {
			continue
		}
		data, ok, err := a.read(clean)
		if err != nil {
			return nil, err
		}
		if ok {
			out[base] = data
		}
	}
	return out, nil
}

// SampleEntry is one file from samples/, with the modification time the
// archive recorded for it.
//
// The mtime is carried because it is data, not metadata. One sample
// format has a time of day and no date, and its parser rule takes the
// day from the file's mtime — so a sample written with today's date
// lands years away from the other samples, and the histogram over them
// becomes two bars with a gulf between. Extracting the zip by hand
// preserves the mtime; writing the file from the updater has to do the
// same or the two paths disagree.
type SampleEntry struct {
	Data []byte
	Mod  time.Time
}

// samples returns every samples/ member, keyed by base name. Unlike
// rules() there is no extension filter: the folder ships .log, .txt,
// .ndjson, .parquet and a README, and a filter here would silently drop
// whichever format is added next.
func (a *archive) samples() (map[string]SampleEntry, error) {
	out := map[string]SampleEntry{}
	for _, f := range a.zip.File {
		clean, err := safeName(f.Name)
		if err != nil {
			return nil, err
		}
		dir, base := path.Split(clean)
		if dir != "samples/" || base == "" {
			continue
		}
		data, ok, err := a.read(clean)
		if err != nil {
			return nil, err
		}
		if ok {
			out[base] = SampleEntry{Data: data, Mod: f.Modified}
		}
	}
	return out, nil
}

// binaryName is the executable inside the archive for this platform.
func binaryName() string {
	if isWindows() {
		return "kopusha.exe"
	}
	return "kopusha"
}

// writable reports whether the install directory can be written, which on
// Windows is frequently false and is the single most common reason an
// update cannot proceed. It is checked before downloading 60 MB, so the
// failure arrives immediately rather than at the end.
func writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".kopusha-write-check-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

// InstallDirOf returns the directory holding the executable.
func InstallDirOf(exePath string) string { return filepath.Dir(exePath) }
