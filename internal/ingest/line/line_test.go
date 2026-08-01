package line

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labmk/obs-viewer/internal/ingest"
)

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sample.log")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func runStream(t *testing.T, l *Loader, body string, mtime time.Time) []ingest.Record {
	t.Helper()
	path := writeTemp(t, body)
	var got []ingest.Record
	hint := ingest.LoadHint{Path: path, MTime: mtime}
	if err := l.Stream(context.Background(), hint, func(r ingest.Record) error {
		got = append(got, r)
		return nil
	}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	return got
}

// --- Format A: ISO-bracket (bracketed level/component/PID) ---

func ruleIsoBracket() ingest.RawRule {
	return ingest.RawRule{
		Family: "line", Name: "iso-bracket",
		Data: map[string]any{
			"name":      "iso-bracket",
			"priority":  100,
			"parse":     `^(?P<ts>\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3}) \[(?P<level>\w+)\] \[(?P<component>\w+)\] \[PID:(?P<pid>\d+)\] (?P<message>.*)$`,
			"ts_layout": "2006-01-02 15:04:05.000",
		},
	}
}

func TestIsoBracketBasic(t *testing.T) {
	l, err := New([]ingest.RawRule{ruleIsoBracket()})
	if err != nil {
		t.Fatal(err)
	}
	body := "2026-03-25 00:31:11.231 [INF] [CS] [PID:4252] clientManagement.xml.enc\n" +
		"2026-03-25 00:31:11.246 [INF] [CS] [PID:4252] DC.Extension.xml.enc\n"

	got := runStream(t, l, body, time.Now())
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2", len(got))
	}
	if got[0]["level"] != "INF" || got[0]["component"] != "CS" || got[0]["pid"] != "4252" {
		t.Errorf("record 0 fields wrong: %+v", got[0])
	}
	if got[0]["message"] != "clientManagement.xml.enc" {
		t.Errorf("message=%q", got[0]["message"])
	}
	if got[0][ingest.FieldTimestamp] != "2026-03-25T00:31:11.231Z" {
		t.Errorf("@timestamp=%v", got[0][ingest.FieldTimestamp])
	}
	if got[0][ingest.FieldSource] != "line:iso-bracket" {
		t.Errorf("_source_format=%v", got[0][ingest.FieldSource])
	}
}

func TestIsoBracketContinuation(t *testing.T) {
	l, _ := New([]ingest.RawRule{ruleIsoBracket()})
	body := "2026-03-25 00:31:11.231 [INF] [CS] [PID:4252] header\n" +
		"continuation line one\n" +
		"  indented continuation two\n" +
		"2026-03-25 00:31:11.246 [INF] [CS] [PID:4252] next\n"
	got := runStream(t, l, body, time.Now())
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	want := "header\ncontinuation line one\n  indented continuation two"
	if got[0]["message"] != want {
		t.Errorf("message=%q\n  want %q", got[0]["message"], want)
	}
}

// --- Format B: dotdate-pidtid with colon-separated msec ---

func ruleDotdatePIDTID() ingest.RawRule {
	return ingest.RawRule{
		Family: "line", Name: "dotdate-pidtid",
		Data: map[string]any{
			"name":      "dotdate-pidtid",
			"priority":  90,
			"parse":     `^(?P<source>[\w.]+):\s+(?P<ts>\d{2}\.\d{2}\.\d{2}\s+\d{2}:\d{2}:\d{2}:\d+)\s+(?P<pid>\d+)/(?P<tid>\d+)\s+(?P<message>.*)$`,
			"ts_layout": "02.01.06 15:04:05.000",
			"ts_regex_subs": []any{
				map[string]any{"pattern": `(\d{2}:\d{2}:\d{2}):(\d+)$`, "replacement": "$1.$2"},
			},
		},
	}
}

func TestDotdatePIDTID(t *testing.T) {
	l, err := New([]ingest.RawRule{ruleDotdatePIDTID()})
	if err != nil {
		t.Fatal(err)
	}
	body := "app.Common.CacheService: 26.03.26 09:55:01:280 36800/113264 ConfigModel creation started.\n" +
		"app.Common.CacheService: 26.03.26 09:55:01:547 36800/113264 ConfigModel creation finished.\n"
	got := runStream(t, l, body, time.Now())
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	if got[0]["source"] != "app.Common.CacheService" {
		t.Errorf("source=%q", got[0]["source"])
	}
	if got[0]["pid"] != "36800" || got[0]["tid"] != "113264" {
		t.Errorf("pid/tid wrong: %+v", got[0])
	}
	if got[0][ingest.FieldTimestamp] != "2026-03-26T09:55:01.28Z" {
		t.Errorf("@timestamp=%v (want 2026-03-26T09:55:01.28Z)", got[0][ingest.FieldTimestamp])
	}
}

// --- Format C: dash-date level ---

func ruleDashdateLevel() ingest.RawRule {
	return ingest.RawRule{
		Family: "line", Name: "dashdate-level",
		Data: map[string]any{
			"name":      "dashdate-level",
			"priority":  90,
			"parse":     `^(?P<ts>\d{2}-\d{2}-\d{4} \d{2}:\d{2}:\d{2}\.\d{3})\s+(?P<level>\w+)\s+(?P<message>.*)$`,
			"ts_layout": "02-01-2006 15:04:05.000",
		},
	}
}

func TestDashdateLevel(t *testing.T) {
	l, _ := New([]ingest.RawRule{ruleDashdateLevel()})
	body := "28-02-2026 04:00:40.417 Info    Analyzing Configuration/SqlServerConfigSettings ...\n"
	got := runStream(t, l, body, time.Now())
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0]["level"] != "Info" {
		t.Errorf("level=%q", got[0]["level"])
	}
	if got[0][ingest.FieldTimestamp] != "2026-02-28T04:00:40.417Z" {
		t.Errorf("@timestamp=%v", got[0][ingest.FieldTimestamp])
	}
}

// --- Format D: time-only + mtime ---

func ruleTimeOnly() ingest.RawRule {
	return ingest.RawRule{
		Family: "line", Name: "time-pidtid",
		Data: map[string]any{
			"name":              "time-pidtid",
			"priority":          80,
			"parse":             `^(?P<ts>\d{2}:\d{2}:\d{2}\.\d+)\s+(?P<pid>\d+)/(?P<tid>\d+)\s+(?P<level>\w+)\s+(?P<message>.*)$`,
			"ts_layout":         "15:04:05.999999999",
			"ts_use_mtime_date": true,
		},
	}
}

func TestTimeOnlyUsesMtimeDate(t *testing.T) {
	l, _ := New([]ingest.RawRule{ruleTimeOnly()})
	body := "15:13:52.592268 26804/46320 Info   CaseViewActivationSetter created\n" +
		"15:14:05.801728 26804/46320 Info   CacheSize applied to Falcon is 4.\n"
	mtime := time.Date(2026, 3, 24, 14, 14, 0, 0, time.UTC)
	got := runStream(t, l, body, mtime)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	if got[0][ingest.FieldTimestamp] != "2026-03-24T15:13:52.592268Z" {
		t.Errorf("@timestamp=%v", got[0][ingest.FieldTimestamp])
	}
	if got[1][ingest.FieldTimestamp] != "2026-03-24T15:14:05.801728Z" {
		t.Errorf("@timestamp=%v", got[1][ingest.FieldTimestamp])
	}
}

func TestTimeOnlyDayRollover(t *testing.T) {
	l, _ := New([]ingest.RawRule{ruleTimeOnly()})
	body := "23:59:58.000000 1/1 Info just before midnight\n" +
		"23:59:59.500000 1/1 Info almost midnight\n" +
		"00:00:01.000000 1/1 Info next day\n" +
		"00:00:02.000000 1/1 Info still next day\n"
	mtime := time.Date(2026, 3, 24, 0, 0, 30, 0, time.UTC) // start at day 24
	got := runStream(t, l, body, mtime)
	if len(got) != 4 {
		t.Fatalf("got %d, want 4", len(got))
	}
	if !strings.HasPrefix(got[0][ingest.FieldTimestamp].(string), "2026-03-24T23:59:58") {
		t.Errorf("record 0 ts=%v", got[0][ingest.FieldTimestamp])
	}
	if !strings.HasPrefix(got[2][ingest.FieldTimestamp].(string), "2026-03-25T00:00:01") {
		t.Errorf("record 2 (post-midnight) ts=%v, want 2026-03-25T00:00:01...", got[2][ingest.FieldTimestamp])
	}
	if !strings.HasPrefix(got[3][ingest.FieldTimestamp].(string), "2026-03-25T00:00:02") {
		t.Errorf("record 3 ts=%v", got[3][ingest.FieldTimestamp])
	}
}

// --- Format E: time-then-date with colon-terminator ---

func ruleTimeDotdate() ingest.RawRule {
	return ingest.RawRule{
		Family: "line", Name: "time-dotdate",
		Data: map[string]any{
			"name":      "time-dotdate",
			"priority":  85,
			"parse":     `^(?P<ts>\d{2}:\d{2}:\d{2}\.\d{3} \d{2}\.\d{2}\.\d{4}):\s*(?P<message>.*)$`,
			"ts_layout": "15:04:05.000 02.01.2006",
		},
	}
}

func TestTimeDotdate(t *testing.T) {
	l, _ := New([]ingest.RawRule{ruleTimeDotdate()})
	body := "22:30:10.741 02.08.2024: Installer started.\n" +
		"22:30:10.741 02.08.2024: Configuration completed.\n"
	got := runStream(t, l, body, time.Now())
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	if got[0][ingest.FieldTimestamp] != "2024-08-02T22:30:10.741Z" {
		t.Errorf("@timestamp=%v", got[0][ingest.FieldTimestamp])
	}
	if got[0]["message"] != "Installer started." {
		t.Errorf("message=%q", got[0]["message"])
	}
}

// --- Generic rule plumbing ---

func TestDetectScoresHighestRule(t *testing.T) {
	l, _ := New([]ingest.RawRule{ruleIsoBracket(), ruleTimeOnly()})
	sniff := []byte("2026-03-25 00:31:11.231 [INF] [CS] [PID:4252] x\n")
	got := l.Detect(ingest.LoadHint{Sniff: sniff})
	if got != 100 {
		t.Errorf("got %d, want 100 (iso-bracket priority)", got)
	}
}

func TestNoRuleMatchesReturnsZero(t *testing.T) {
	l, _ := New([]ingest.RawRule{ruleIsoBracket()})
	if got := l.Detect(ingest.LoadHint{Sniff: []byte("not a log line\n")}); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

// TestOverlongLineDoesNotKillIngest — a single multi-MB malformed
// line previously triggered bufio.ErrTooLong which is terminal for
// bufio.Scanner; the entire file's remaining records were lost.
// After the switch to ReadLineBounded, the bad line is truncated
// (and may simply not match any rule) and the next normal line is
// still parsed.
func TestOverlongLineDoesNotKillIngest(t *testing.T) {
	l, err := New([]ingest.RawRule{ruleIsoBracket()})
	if err != nil {
		t.Fatal(err)
	}
	// 1 line that matches → 1 oversize garbage line → 1 line that matches.
	// The oversize line is far larger than typical (33 MB) but the test
	// uses a smaller cap to keep the test fast.
	garbage := strings.Repeat("X", 32*1024*1024)
	body := "2026-05-18 10:00:00.000 [INF] [CS] [PID:1] before\n" +
		garbage + "\n" +
		"2026-05-18 10:00:01.000 [INF] [CS] [PID:1] after\n"

	got := runStream(t, l, body, time.Now())
	if len(got) < 2 {
		t.Fatalf("got %d records, want at least 2 (overlong line must not stop ingest)", len(got))
	}
	if got[0]["message"] != "before" {
		t.Errorf("first record message=%q, want before", got[0]["message"])
	}
	lastIdx := len(got) - 1
	if got[lastIdx]["message"] != "after" {
		t.Errorf("last record message=%q, want after — ingest stopped early", got[lastIdx]["message"])
	}
}

func TestCompileRejectsMissingTs(t *testing.T) {
	_, err := New([]ingest.RawRule{{Data: map[string]any{
		"name":      "x",
		"parse":     `^(?P<msg>.*)$`,
		"ts_layout": "2006",
	}}})
	if err == nil {
		t.Error("expected error: parse regex without (?P<ts>...)")
	}
}
