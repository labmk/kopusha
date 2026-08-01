package ingest

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"testing"
)

func read(t *testing.T, body string, max int) []struct {
	line  string
	trunc bool
} {
	t.Helper()
	br := bufio.NewReader(strings.NewReader(body))
	var out []struct {
		line  string
		trunc bool
	}
	for {
		l, tr, err := ReadLineBounded(br, max)
		if errors.Is(err, io.EOF) {
			if l != "" || tr {
				out = append(out, struct {
					line  string
					trunc bool
				}{l, tr})
			}
			break
		}
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		out = append(out, struct {
			line  string
			trunc bool
		}{l, tr})
	}
	return out
}

func TestReadLineBoundedNormalLines(t *testing.T) {
	got := read(t, "a\nbb\nccc\n", 100)
	if len(got) != 3 {
		t.Fatalf("got %d lines, want 3: %+v", len(got), got)
	}
	if got[0].line != "a" || got[1].line != "bb" || got[2].line != "ccc" {
		t.Errorf("lines wrong: %+v", got)
	}
}

func TestReadLineBoundedCRLFStripped(t *testing.T) {
	got := read(t, "a\r\nb\r\n", 100)
	if got[0].line != "a" || got[1].line != "b" {
		t.Errorf("CRLF strip wrong: %+v", got)
	}
}

func TestReadLineBoundedTruncatesAndContinues(t *testing.T) {
	// First line is 20 bytes, cap is 10 → truncated.
	// Second line is short and must be returned intact.
	body := strings.Repeat("X", 20) + "\nshort\n"
	got := read(t, body, 10)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	if got[0].line != strings.Repeat("X", 10) || !got[0].trunc {
		t.Errorf("line 0 wrong: %q trunc=%v", got[0].line, got[0].trunc)
	}
	if got[1].line != "short" || got[1].trunc {
		t.Errorf("line 1 wrong (recovery failed): %q trunc=%v", got[1].line, got[1].trunc)
	}
}

func TestReadLineBoundedFinalPartialLine(t *testing.T) {
	got := read(t, "a\nb\nc-no-newline", 100)
	if len(got) != 3 {
		t.Fatalf("got %d, want 3: %+v", len(got), got)
	}
	if got[2].line != "c-no-newline" {
		t.Errorf("trailing partial line lost: %+v", got[2])
	}
}

func TestReadLineBoundedEmptyInput(t *testing.T) {
	got := read(t, "", 100)
	if len(got) != 0 {
		t.Errorf("expected 0 lines, got %d", len(got))
	}
}
