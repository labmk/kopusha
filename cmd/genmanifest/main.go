// Command genmanifest writes parsers.d.sha256, the record of which
// parser rules a binary shipped with.
//
// Run from build.sh before the Go build so the manifest compiled into the
// binary always describes the parsers.d/ shipped beside it. A test
// (internal/manifest) asserts the committed file is current, so a rule
// changed without regenerating fails CI rather than shipping a binary
// that misjudges which rules the user edited.
//
//	go run ./cmd/genmanifest [-dir parsers.d] [-out parsers.d.sha256]
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/labmk/obs-viewer/internal/manifest"
)

func main() {
	dir := flag.String("dir", "parsers.d", "rules directory to hash")
	out := flag.String("out", "parsers.d.sha256", "manifest file to write")
	flag.Parse()

	m, err := manifest.Build(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "genmanifest: %v\n", err)
		os.Exit(1)
	}
	rendered := m.Render()

	// Skip the write when nothing changed, so a build does not touch the
	// file's mtime and make the tree look dirty for no reason.
	if existing, err := os.ReadFile(*out); err == nil && string(existing) == string(rendered) {
		fmt.Printf("  Parser manifest unchanged (%d rules)\n", len(m))
		return
	}
	if err := os.WriteFile(*out, rendered, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "genmanifest: write %s: %v\n", *out, err)
		os.Exit(1)
	}
	fmt.Printf("  Parser manifest written: %s (%d rules)\n", *out, len(m))
}
