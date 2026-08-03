package xml

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labmk/kopusha/internal/ingest"
)

func defaultRule() ingest.RawRule {
	return ingest.RawRule{
		Family: "xml", Name: "repeating-row-element",
		Data: map[string]any{
			"name":     "repeating-row-element",
			"priority": 80,
			"sniff":    `^\s*<`,
			"ts_candidates": []any{
				"@time", "Timestamp", "TimeStamp_Absolute",
			},
			"ts_layouts": []any{
				time.RFC3339Nano,
				"2006-01-02 15:04:05Z",
				"2006-01-02 15:04:05Z07:00",
			},
		},
	}
}

func loader(t *testing.T) *Loader {
	t.Helper()
	l, err := New([]ingest.RawRule{defaultRule()})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sample.xml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func run(t *testing.T, l *Loader, path string) []ingest.Record {
	t.Helper()
	hint, err := ingest.HintForFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []ingest.Record
	if err := l.Stream(context.Background(), hint, func(r ingest.Record) error {
		out = append(out, r)
		return nil
	}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	return out
}

func TestRowIsMostFrequentSibling(t *testing.T) {
	body := `<?xml version="1.0"?>
<Root>
  <Header><Title>x</Title></Header>
  <Items>
    <Item id="1">a</Item>
    <Item id="2">b</Item>
    <Item id="3">c</Item>
    <Item id="4">d</Item>
  </Items>
</Root>`
	got := run(t, loader(t), writeTemp(t, body))
	if len(got) != 4 {
		t.Fatalf("got %d rows, want 4", len(got))
	}
	if got[0]["@id"] != "1" || got[3]["@id"] != "4" {
		t.Errorf("@id wrong: %v / %v", got[0]["@id"], got[3]["@id"])
	}
}

func TestRowAttributesGoToTopLevel(t *testing.T) {
	body := `<Root>
  <Frame SopInstanceUid="x" SeriesNumber="6"/>
  <Frame SopInstanceUid="y" SeriesNumber="7"/>
  <Frame SopInstanceUid="z" SeriesNumber="8"/>
</Root>`
	got := run(t, loader(t), writeTemp(t, body))
	if len(got) != 3 {
		t.Fatalf("got %d, want 3", len(got))
	}
	if got[0]["@SopInstanceUid"] != "x" || got[0]["@SeriesNumber"] != "6" {
		t.Errorf("row 0 attrs: %+v", got[0])
	}
}

func TestNestedChildrenBecomeDotPaths(t *testing.T) {
	body := `<Root>
  <Event time="2026-05-16T10:00:00Z">
    <Process>
      <Name>worker</Name>
      <Pid>1234</Pid>
    </Process>
    <Host>vm01</Host>
  </Event>
  <Event time="2026-05-16T10:00:01Z">
    <Process>
      <Name>worker</Name>
      <Pid>5678</Pid>
    </Process>
    <Host>vm02</Host>
  </Event>
  <Event time="2026-05-16T10:00:02Z">
    <Process>
      <Name>worker</Name>
      <Pid>9012</Pid>
    </Process>
    <Host>vm03</Host>
  </Event>
</Root>`
	got := run(t, loader(t), writeTemp(t, body))
	if len(got) != 3 {
		t.Fatalf("got %d, want 3", len(got))
	}
	r := got[0]
	if r["Process.Name"] != "worker" || r["Process.Pid"] != "1234" || r["Host"] != "vm01" {
		t.Errorf("dot-paths wrong: %+v", r)
	}
}

func TestChildAttributesUseAtSyntax(t *testing.T) {
	body := `<Root>
  <Event time="2026-05-16T10:00:00Z">
    <Reference Relation="View" Target="X"/>
  </Event>
  <Event time="2026-05-16T10:00:01Z">
    <Reference Relation="View" Target="Y"/>
  </Event>
  <Event time="2026-05-16T10:00:02Z">
    <Reference Relation="View" Target="Z"/>
  </Event>
</Root>`
	got := run(t, loader(t), writeTemp(t, body))
	if len(got) != 3 {
		t.Fatalf("got %d, want 3", len(got))
	}
	r := got[0]
	if r["Reference.@Relation"] != "View" || r["Reference.@Target"] != "X" {
		t.Errorf("child attrs: %+v", r)
	}
}

func TestRepeatedChildrenCollapsedToList(t *testing.T) {
	body := `<Root>
  <Event time="2026-05-16T10:00:00Z">
    <Views>
      <Reference Target="A"/>
      <Reference Target="B"/>
      <Reference Target="C"/>
    </Views>
  </Event>
  <Event time="2026-05-16T10:00:01Z">
    <Views>
      <Reference Target="D"/>
    </Views>
  </Event>
  <Event time="2026-05-16T10:00:02Z">
    <Views>
      <Reference Target="E"/>
    </Views>
  </Event>
</Root>`
	got := run(t, loader(t), writeTemp(t, body))
	if len(got) != 3 {
		t.Fatalf("got %d, want 3", len(got))
	}
	r := got[0]
	list, ok := r["Views.Reference.@Target"].([]any)
	if !ok {
		t.Fatalf("Views.Reference.@Target = %T (want []any), full record: %+v", r["Views.Reference.@Target"], r)
	}
	if len(list) != 3 {
		t.Errorf("got %d items, want 3: %+v", len(list), list)
	}
}

func TestTimestampFromAttribute(t *testing.T) {
	body := `<Root>
  <Event time="2026-05-16T10:00:00Z"><x>1</x></Event>
  <Event time="2026-05-16T10:00:01Z"><x>2</x></Event>
  <Event time="2026-05-16T10:00:02Z"><x>3</x></Event>
</Root>`
	got := run(t, loader(t), writeTemp(t, body))
	if len(got) != 3 {
		t.Fatalf("got %d, want 3", len(got))
	}
	if got[0][ingest.FieldTimestamp] != "2026-05-16T10:00:00Z" {
		t.Errorf("@timestamp=%v", got[0][ingest.FieldTimestamp])
	}
}

func TestTimestampFromChildText(t *testing.T) {
	body := `<Root>
  <Event>
    <TimeStamp_Absolute>2026-05-16T11:00:00.123456+01:00</TimeStamp_Absolute>
  </Event>
  <Event>
    <TimeStamp_Absolute>2026-05-16T11:01:00.123456+01:00</TimeStamp_Absolute>
  </Event>
  <Event>
    <TimeStamp_Absolute>2026-05-16T11:02:00.123456+01:00</TimeStamp_Absolute>
  </Event>
</Root>`
	got := run(t, loader(t), writeTemp(t, body))
	if len(got) != 3 {
		t.Fatalf("got %d", len(got))
	}
	if got[0][ingest.FieldTimestamp] != "2026-05-16T10:00:00.123456Z" {
		t.Errorf("@timestamp=%v", got[0][ingest.FieldTimestamp])
	}
}

func TestMultiRootStreamingWithoutXmlDeclaration(t *testing.T) {
	// UtilizationEvents.txt style: no <?xml decl, no single root.
	body := `<Event time="2026-05-16T10:00:00Z"><x>1</x></Event>
<Event time="2026-05-16T10:00:01Z"><x>2</x></Event>
<Event time="2026-05-16T10:00:02Z"><x>3</x></Event>
`
	got := run(t, loader(t), writeTemp(t, body))
	if len(got) != 3 {
		t.Fatalf("got %d, want 3 (multi-root must work)", len(got))
	}
}

func TestEncodedXmlInLeafStaysAsString(t *testing.T) {
	body := `<Root>
  <Event time="2026-05-16T10:00:00Z">
    <Encoded>&lt;Inner attr=&apos;v&apos;/&gt;</Encoded>
  </Event>
  <Event time="2026-05-16T10:00:01Z">
    <Encoded>&lt;Inner attr=&apos;v&apos;/&gt;</Encoded>
  </Event>
  <Event time="2026-05-16T10:00:02Z">
    <Encoded>&lt;Inner attr=&apos;v&apos;/&gt;</Encoded>
  </Event>
</Root>`
	got := run(t, loader(t), writeTemp(t, body))
	if len(got) != 3 {
		t.Fatalf("got %d", len(got))
	}
	want := `<Inner attr='v'/>`
	if got[0]["Encoded"] != want {
		t.Errorf("Encoded=%q\n  want %q", got[0]["Encoded"], want)
	}
}

func TestDetectsXmlSniff(t *testing.T) {
	l := loader(t)
	got := l.Detect(ingest.LoadHint{Sniff: []byte(`<?xml version="1.0"?><Root/>`)})
	if got != 80 {
		t.Errorf("got %d, want 80", got)
	}
}

func TestNoMatchForNonXml(t *testing.T) {
	l := loader(t)
	got := l.Detect(ingest.LoadHint{Sniff: []byte("plain text\n")})
	if got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}
