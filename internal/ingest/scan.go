package ingest

import (
	"bufio"
	"strings"
)

// MaxLineBytes is the per-line ceiling for text adapters (block, line).
// Beyond this, ReadLineBounded truncates the line, drops the rest, and
// reports the truncation so the caller can mark or skip the record.
// 16 MiB matches the previous bufio.Scanner buffer; lifting the limit
// arbitrarily would let one pathological line OOM the process.
const MaxLineBytes = 16 * 1024 * 1024

// ReadLineBounded reads one logical line from r, returning at most
// maxBytes of content even when the line is longer.
//
// Semantics:
//   - Returns (line, false, nil) on a normal '\n'-terminated line.
//   - Returns (line, true, nil) when the line exceeded maxBytes; the
//     returned text contains the first maxBytes; the rest of the line
//     up to the next '\n' is silently drained so the next call starts
//     at a real line boundary.
//   - Returns ("", false, io.EOF) on a clean EOF before any data.
//   - Returns (line, false, io.EOF) when the file ends without a
//     trailing newline; the caller should process the partial line
//     and stop iterating.
//   - Trailing '\r' is stripped from the returned text so CRLF files
//     parse the same as LF files.
//
// This replaces bufio.Scanner for adapters that must keep ingesting
// after a single overlong line. Scanner's ErrTooLong is terminal:
// once it fires, that scanner cannot be resumed, and the underlying
// reader's position is undefined relative to line boundaries.
func ReadLineBounded(r *bufio.Reader, maxBytes int) (string, bool, error) {
	var sb strings.Builder
	truncated := false
	for {
		b, err := r.ReadByte()
		if err != nil {
			if sb.Len() == 0 {
				return "", false, err
			}
			return strings.TrimRight(sb.String(), "\r"), truncated, err
		}
		if b == '\n' {
			return strings.TrimRight(sb.String(), "\r"), truncated, nil
		}
		if truncated {
			// Already over the cap — discard until next newline.
			continue
		}
		if sb.Len() >= maxBytes {
			truncated = true
			continue
		}
		sb.WriteByte(b)
	}
}
