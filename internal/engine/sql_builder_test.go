package engine

import (
	"strings"
	"testing"
)

// Pure SQL-builder tests for buildFilterCondition, buildWhereClause,
// quoteFieldRef, quotedValueList, escapeSQLString, pickTimestampField,
// isTimestampLike, resolveSortField. No DuckDB involved.

func newEmpty() *Engine {
	return &Engine{
		files:            make(map[string]*FileInfo),
		tableCols:        make(map[string][]string),
		tableStructPaths: make(map[string][]string),
		pathIndex:        make(map[string]string),
		timestampField:   "@timestamp",
	}
}

func TestEscapeSQLString(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"plain", "plain"},
		{"O'Brien", "O''Brien"},
		{"a'b'c", "a''b''c"},
		{"''", "''''"},
	}
	for _, c := range cases {
		if got := escapeSQLString(c.in); got != c.want {
			t.Errorf("escapeSQLString(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestQuoteIdent(t *testing.T) {
	cases := []struct{ in, want string }{
		{"col", `"col"`},
		{"@timestamp", `"@timestamp"`},
		{`bad"name`, `"bad""name"`},
		{`a"b"c`, `"a""b""c"`},
		// Path with dot — quoteIdent treats the whole input as one identifier.
		// Use pathExprFromSegments for struct paths.
		{"a.b", `"a.b"`},
	}
	for _, c := range cases {
		if got := quoteIdent(c.in); got != c.want {
			t.Errorf("quoteIdent(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPathExprFromSegments(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"nodeinfo"}, `"nodeinfo"`},
		{[]string{"nodeinfo", "type"}, `"nodeinfo"."type"`},
		{[]string{"a", "b", "c"}, `"a"."b"."c"`},
		{[]string{`bad"name`, "x"}, `"bad""name"."x"`},
	}
	for _, c := range cases {
		if got := pathExprFromSegments(c.in); got != c.want {
			t.Errorf("pathExprFromSegments(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPickTimestampField(t *testing.T) {
	cases := []struct {
		name    string
		columns []string
		want    string
	}{
		{"empty falls back to @timestamp literal", nil, "@timestamp"},
		{"@timestamp preferred", []string{"foo", "@timestamp", "timestamp"}, "@timestamp"},
		{"timestamp when no @timestamp", []string{"foo", "timestamp", "time"}, "timestamp"},
		{"case-insensitive @timestamp", []string{"foo", "@TIMESTAMP"}, "@TIMESTAMP"},
		{"time fallback", []string{"foo", "time"}, "time"},
		{"event_time over generic name", []string{"foo", "event_time", "bar"}, "event_time"},
		{"any time-like column when no canonical", []string{"foo", "log_time"}, "log_time"},
		{"first column as last resort", []string{"unrelated", "alsounrelated"}, "unrelated"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pickTimestampField(c.columns); got != c.want {
				t.Errorf("pickTimestampField(%v) = %q, want %q", c.columns, got, c.want)
			}
		})
	}
}

func TestIsTimestampLike(t *testing.T) {
	// "createdAt" does NOT match — isTimestampLike checks substrings
	// "time"/"date" + the literal "ts" only.
	yes := []string{"@timestamp", "timestamp", "time", "ts", "EventTime", "log_date", "DateTime"}
	no := []string{"message", "level", "host", "id", "value", "name", "createdAt"}
	for _, c := range yes {
		if !isTimestampLike(c) {
			t.Errorf("isTimestampLike(%q) = false, want true", c)
		}
	}
	for _, c := range no {
		if isTimestampLike(c) {
			t.Errorf("isTimestampLike(%q) = true, want false", c)
		}
	}
}

func TestQuotedValueList(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{",,", ""},
		{"a", "LOWER('a')"},
		{"a,b,c", "LOWER('a'), LOWER('b'), LOWER('c')"},
		{" a , b , c ", "LOWER('a'), LOWER('b'), LOWER('c')"},
		{"O'Brien,jane", "LOWER('O''Brien'), LOWER('jane')"},
		{"a,,b", "LOWER('a'), LOWER('b')"}, // empties dropped
	}
	for _, c := range cases {
		if got := quotedValueList(c.in); got != c.want {
			t.Errorf("quotedValueList(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// quoteFieldRef routing — literal vs struct sub-path vs unknown.
func TestQuoteFieldRefLiteralColumn(t *testing.T) {
	e := newEmpty()
	e.tableCols["file_a"] = []string{"@timestamp", "Message", "app.name"}

	if got := e.quoteFieldRef("Message"); got != `"Message"` {
		t.Errorf("Message → %q, want \"Message\"", got)
	}
	// Dotted literal column preserved (not split into struct path).
	if got := e.quoteFieldRef("app.name"); got != `"app.name"` {
		t.Errorf("app.name → %q, want \"app.name\"", got)
	}
	// Case-insensitive match returns the actual casing.
	if got := e.quoteFieldRef("message"); got != `"Message"` {
		t.Errorf("message → %q, want \"Message\"", got)
	}
}

func TestQuoteFieldRefStructSubPath(t *testing.T) {
	e := newEmpty()
	e.tableCols["file_a"] = []string{"@timestamp", "nodeinfo"}
	e.tableStructPaths["file_a"] = []string{"nodeinfo.type", "nodeinfo.id", "nodeinfo.id.major"}

	// One-level struct path.
	got := e.quoteFieldRef("nodeinfo.type")
	want := `json_extract_string("nodeinfo", '$.type')`
	if got != want {
		t.Errorf("nodeinfo.type → %q, want %q", got, want)
	}
	// Two-level nested.
	got = e.quoteFieldRef("nodeinfo.id.major")
	want = `json_extract_string("nodeinfo", '$.id.major')`
	if got != want {
		t.Errorf("nodeinfo.id.major → %q, want %q", got, want)
	}
	// Case-insensitive struct match.
	got = e.quoteFieldRef("NODEINFO.TYPE")
	if !strings.Contains(got, `json_extract_string`) {
		t.Errorf("NODEINFO.TYPE → %q, want json_extract_string", got)
	}
}

// A struct key carrying a single quote must not break out of the SQL
// string literal in the emitted json_extract_string path. Before this
// was escaped, a crafted NDJSON key like
//
//	x') || (SELECT version()) || json_extract_string("nested", '$.x
//
// rebalanced the quotes and parentheses into a working subquery, which
// DuckDB then ran with the process's filesystem access — arbitrary SQL
// execution from a loaded log file. The identifier half was quoted from
// the start; the path half was not.
func TestQuoteFieldRefEscapesStructPathQuotes(t *testing.T) {
	e := newEmpty()
	e.tableCols["file_a"] = []string{"nested"}
	e.tableStructPaths["file_a"] = []string{"nested.ev'il"}

	got := e.quoteFieldRef("nested.ev'il")
	// The single quote inside the path is doubled, so the literal stays
	// closed exactly where it should.
	want := `json_extract_string("nested", '$.ev''il')`
	if got != want {
		t.Errorf("quoteFieldRef → %q, want %q", got, want)
	}
	// And the weaponised form: no bare, unescaped quote may survive
	// between the emitted path delimiters.
	e.tableStructPaths["file_a"] = []string{`nested.x') || (SELECT version()) || json_extract_string("nested", '$.x`}
	got = e.quoteFieldRef(`nested.x') || (SELECT version()) || json_extract_string("nested", '$.x`)
	inner := got[strings.Index(got, "'$")+1 : strings.LastIndex(got, "'")]
	for i := 0; i < len(inner); i++ {
		if inner[i] == '\'' {
			// A lone quote is an injection; a doubled quote ('') is escaped data.
			if i+1 >= len(inner) || inner[i+1] != '\'' {
				t.Fatalf("unescaped quote at %d in emitted path: %q", i, got)
			}
			i++ // skip the pair
		}
	}
}

func TestQuoteFieldRefUnknownFallsBack(t *testing.T) {
	e := newEmpty()
	e.tableCols["file_a"] = []string{"a", "b"}
	// Unknown field — quote as a single ident so DuckDB surfaces a
	// clear error rather than silently dropping the filter.
	if got := e.quoteFieldRef("nope"); got != `"nope"` {
		t.Errorf("nope → %q, want \"nope\"", got)
	}
	// Unknown dotted name with no struct match — same fallback.
	if got := e.quoteFieldRef("nope.deeper"); got != `"nope.deeper"` {
		t.Errorf("nope.deeper → %q, want \"nope.deeper\"", got)
	}
}

func TestBuildFilterConditionAllOperators(t *testing.T) {
	e := newEmpty()
	e.tableCols["file_a"] = []string{"@timestamp", "Message", "level"}

	cases := []struct {
		name string
		f    Filter
		want string
	}{
		{
			"is",
			Filter{Field: "Message", Operator: "is", Value: "alpha"},
			`LOWER(TRY_CAST("Message" AS VARCHAR)) = LOWER('alpha')`,
		},
		{
			"is_not",
			Filter{Field: "Message", Operator: "is_not", Value: "alpha"},
			`LOWER(TRY_CAST("Message" AS VARCHAR)) != LOWER('alpha')`,
		},
		{
			"contains",
			Filter{Field: "Message", Operator: "contains", Value: "err"},
			`TRY_CAST("Message" AS VARCHAR) ILIKE '%err%'`,
		},
		{
			"not_contains",
			Filter{Field: "Message", Operator: "not_contains", Value: "err"},
			`TRY_CAST("Message" AS VARCHAR) NOT ILIKE '%err%'`,
		},
		{
			"wildcard: * → %",
			Filter{Field: "Message", Operator: "wildcard", Value: "al*ha"},
			`TRY_CAST("Message" AS VARCHAR) ILIKE 'al%ha'`,
		},
		{
			"not_wildcard: * → %",
			Filter{Field: "Message", Operator: "not_wildcard", Value: "*x*"},
			`TRY_CAST("Message" AS VARCHAR) NOT ILIKE '%x%'`,
		},
		{
			"exists",
			Filter{Field: "Message", Operator: "exists"},
			`("Message" IS NOT NULL AND TRY_CAST("Message" AS VARCHAR) != '')`,
		},
		{
			"does_not_exist",
			Filter{Field: "Message", Operator: "does_not_exist"},
			`("Message" IS NULL OR TRY_CAST("Message" AS VARCHAR) = '')`,
		},
		{
			"is_one_of: comma list",
			Filter{Field: "level", Operator: "is_one_of", Value: "info, warn, error"},
			`LOWER(TRY_CAST("level" AS VARCHAR)) IN (LOWER('info'), LOWER('warn'), LOWER('error'))`,
		},
		{
			"is_not_one_of: NULL kept on right side",
			Filter{Field: "level", Operator: "is_not_one_of", Value: "info"},
			`(LOWER(TRY_CAST("level" AS VARCHAR)) NOT IN (LOWER('info')) OR "level" IS NULL)`,
		},
		{
			"unknown operator → empty (caller skips)",
			Filter{Field: "Message", Operator: "wat", Value: "x"},
			``,
		},
		{
			"is_one_of: all-empty list → empty",
			Filter{Field: "level", Operator: "is_one_of", Value: ",, ,"},
			``,
		},
		{
			"SQL-escape in value",
			Filter{Field: "Message", Operator: "is", Value: "O'Brien"},
			`LOWER(TRY_CAST("Message" AS VARCHAR)) = LOWER('O''Brien')`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := e.buildFilterCondition(c.f); got != c.want {
				t.Errorf("buildFilterCondition\n  got:  %s\n  want: %s", got, c.want)
			}
		})
	}
}

func TestBuildFilterConditionStructSubPath(t *testing.T) {
	e := newEmpty()
	e.tableCols["file_a"] = []string{"@timestamp", "nodeinfo"}
	e.tableStructPaths["file_a"] = []string{"nodeinfo.type"}

	// Routed through quoteFieldRef → json_extract_string.
	got := e.buildFilterCondition(Filter{Field: "nodeinfo.type", Operator: "is", Value: "worker"})
	if !strings.Contains(got, `json_extract_string("nodeinfo", '$.type')`) {
		t.Errorf("expected json_extract_string in: %s", got)
	}
	if !strings.Contains(got, `LOWER('worker')`) {
		t.Errorf("expected LOWER('worker') in: %s", got)
	}
}

func TestBuildWhereClauseTimeRangeOnly(t *testing.T) {
	e := newEmpty()
	e.timestampField = "@timestamp"
	from := "2026-05-01T00:00:00Z"
	to := "2026-05-31T23:59:59Z"
	clauses := e.buildWhereClause(QueryRequest{TimeFrom: &from, TimeTo: &to}, nil)
	joined := strings.Join(clauses, " ")
	// Both sides go through TRY_CAST. The column is VARCHAR in the union,
	// so comparing as text would make the bound's spelling decide the
	// result — see TestTimeFilterAcceptsEverySpellingOfAnInstant.
	if !strings.Contains(joined, `TRY_CAST("@timestamp" AS TIMESTAMP) >= TRY_CAST('2026-05-01T00:00:00Z' AS TIMESTAMP)`) {
		t.Errorf("missing from clause: %q", joined)
	}
	if !strings.Contains(joined, `TRY_CAST("@timestamp" AS TIMESTAMP) <= TRY_CAST('2026-05-31T23:59:59Z' AS TIMESTAMP)`) {
		t.Errorf("missing to clause: %q", joined)
	}
	if !strings.Contains(joined, "AND") {
		t.Errorf("expected AND between from and to: %q", joined)
	}
}

func TestBuildWhereClauseSearchTextORsAcrossCols(t *testing.T) {
	e := newEmpty()
	allCols := []string{"Message", "level", "host"}
	clauses := e.buildWhereClause(QueryRequest{SearchText: "boom"}, allCols)
	joined := strings.Join(clauses, " ")
	// Each column appears with ILIKE '%boom%'
	for _, c := range allCols {
		needle := `TRY_CAST("` + c + `" AS VARCHAR) ILIKE '%boom%'`
		if !strings.Contains(joined, needle) {
			t.Errorf("missing %s in: %s", needle, joined)
		}
	}
	// Joined with OR, wrapped in parens.
	if !strings.Contains(joined, " OR ") || !strings.Contains(joined, "(") || !strings.Contains(joined, ")") {
		t.Errorf("expected ORed parenthesised group: %s", joined)
	}
}

func TestBuildWhereClauseSearchTextEscapesQuotes(t *testing.T) {
	e := newEmpty()
	clauses := e.buildWhereClause(QueryRequest{SearchText: "O'Brien"}, []string{"Message"})
	joined := strings.Join(clauses, " ")
	if !strings.Contains(joined, `'%O''Brien%'`) {
		t.Errorf("expected escaped quote, got: %s", joined)
	}
}

func TestBuildWhereClauseDropsHalfTypedFilters(t *testing.T) {
	// A filter with empty value (and an operator that needs a value) is
	// silently dropped — protects WHERE from spurious half-typed UI rows.
	e := newEmpty()
	e.tableCols["file_a"] = []string{"Message"}
	clauses := e.buildWhereClause(QueryRequest{
		Filters: []Filter{
			{Field: "Message", Operator: "is", Value: ""},
		},
	}, nil)
	if len(clauses) != 0 {
		t.Errorf("expected empty clauses, got %d: %v", len(clauses), clauses)
	}
}

func TestBuildWhereClauseExistsNeedsNoValue(t *testing.T) {
	// `exists` and `does_not_exist` are the two operators that survive
	// empty value — they take no value by definition.
	e := newEmpty()
	e.tableCols["file_a"] = []string{"Message"}
	clauses := e.buildWhereClause(QueryRequest{
		Filters: []Filter{
			{Field: "Message", Operator: "exists"},
		},
	}, nil)
	joined := strings.Join(clauses, " ")
	if !strings.Contains(joined, "IS NOT NULL") {
		t.Errorf("expected exists clause, got: %s", joined)
	}
}

func TestBuildWhereClauseOrLogic(t *testing.T) {
	e := newEmpty()
	e.tableCols["file_a"] = []string{"level", "host"}
	clauses := e.buildWhereClause(QueryRequest{
		Filters: []Filter{
			{Field: "level", Operator: "is", Value: "ERROR"},
			{Field: "host", Operator: "is", Value: "h1", Logic: "or"},
		},
	}, nil)
	joined := strings.Join(clauses, " ")
	if !strings.Contains(joined, "OR") {
		t.Errorf("expected OR connector: %s", joined)
	}
	if strings.Count(joined, "LOWER(TRY_CAST(") != 2 {
		t.Errorf("expected two LOWER(TRY_CAST(...)) terms: %s", joined)
	}
}

func TestResolveSortField(t *testing.T) {
	e := newEmpty()
	e.timestampField = "@timestamp"
	allCols := []string{"@timestamp", "Message", "ObservedTimestamp"}

	// No request override → engine default.
	if got := e.resolveSortField("", allCols); got != `"@timestamp"` {
		t.Errorf("default → %q", got)
	}
	// Override → use it (case-insensitive match).
	if got := e.resolveSortField("observedtimestamp", allCols); got != `"ObservedTimestamp"` {
		t.Errorf("override case-insensitive → %q", got)
	}
	// Override absent from union → fall back to engine default.
	if got := e.resolveSortField("nope", allCols); got != `"@timestamp"` {
		t.Errorf("missing override → %q, want \"@timestamp\"", got)
	}
}

func TestEnabledTablesRespectsDisabled(t *testing.T) {
	e := newEmpty()
	e.files["1"] = &FileInfo{ID: "1", TableName: "file_1", Enabled: true}
	e.files["2"] = &FileInfo{ID: "2", TableName: "file_2", Enabled: false}
	e.files["3"] = &FileInfo{ID: "3", TableName: "file_3", Enabled: true}
	e.fileOrder = []string{"1", "2", "3"}

	got := e.enabledTables()
	want := []string{"file_1", "file_3"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("enabledTables = %v, want %v", got, want)
	}
}
