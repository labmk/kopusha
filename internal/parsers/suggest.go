package parsers

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Suggest infers a candidate line rule from sample lines.
//
// The problem it solves is not "write a regex" — it is that the person
// with the log file is, by assumption, the person who does not write
// regexes. Anyone who does was never blocked.
//
// The approach is structural rather than clever. Log lines have a fixed
// header and a free-text tail; the header is what a rule needs. So:
// find the timestamp, tokenize every sample line the same way, and walk
// the token streams in lockstep. Positions where every line agrees are
// literal text; positions where they disagree are fields. The walk
// stops where the structure does, and everything after that is the
// message.
//
// It is wrong sometimes. That is why it feeds a live preview instead of
// writing a file — the operator sees the parsed table before anything
// is saved, which makes a wrong guess cheap and obvious rather than
// silent.
func Suggest(sample string) Draft {
	lines := sampleLines(sample, maxSampleLines)
	if len(lines) == 0 {
		return Draft{
			Priority: defaultPriority,
			Sample:   sample,
			Warnings: []string{"the sample has no non-blank lines"},
		}
	}

	cand, matches := pickCandidate(lines)
	if cand == nil {
		return Draft{
			Priority: defaultPriority,
			Sample:   sample,
			Warnings: []string{
				"no recognizable timestamp — write the regex by hand, capturing the timestamp as (?P<ts>...), and the preview below will show what it produces",
			},
		}
	}

	layout, subs, flags, warnings := cand.layout(matches)

	toks := make([][]token, 0, len(lines))
	for _, ln := range lines {
		toks = append(toks, tokenize(ln, cand, maxTokens))
	}

	pattern, buildWarnings := buildPattern(toks, cand)
	warnings = append(warnings, buildWarnings...)

	d := Draft{
		Priority:       defaultPriority,
		Parse:          pattern,
		TsLayout:       layout,
		TsRegexSubs:    subs,
		TsUseMtimeDate: flags.useMtimeDate,
		TsUseMtimeYear: flags.useMtimeYear,
		Sample:         sample,
		Warnings:       warnings,
	}
	return d
}

const (
	// maxSampleLines caps how much of a paste is analysed. Structure is
	// visible in a handful of lines; more only slows the round trip a
	// user is watching happen.
	maxSampleLines = 200
	// maxTokens caps how deep into a line the walk looks. A header
	// longer than this is free text being mistaken for structure.
	maxTokens = 40
	// maxCaptures caps how many fields are proposed. Past this the
	// suggestion has stopped describing a format and started describing
	// one particular line.
	maxCaptures = 8
)

func sampleLines(sample string, limit int) []string {
	// Fold CRLF and strip a BOM the same way the adapter does, so what
	// the inference sees is what the parser will see. A pasted Windows
	// log otherwise produces a regex with an invisible \r before every $.
	sample = strings.TrimPrefix(sample, "\ufeff")
	sample = strings.ReplaceAll(sample, "\r\n", "\n")
	out := make([]string, 0, 16)
	for _, ln := range strings.Split(sample, "\n") {
		ln = strings.TrimRight(ln, "\r")
		if strings.TrimSpace(ln) == "" {
			continue
		}
		out = append(out, ln)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// tsFlags are the adapter switches a timestamp shape implies.
type tsFlags struct {
	useMtimeDate bool // the capture has no date at all
	useMtimeYear bool // the capture has month and day but no year
}

// tsCandidate is one recognized timestamp shape: how to find it, and
// how to describe the text it found in Go's layout language.
//
// The layout is derived from the matched text rather than fixed with
// the pattern, because one pattern covers several real spellings — a
// zone may be `Z`, `+02:00` or `+0200`, and those are three different
// layouts. Guessing the wrong one produces a rule that matches every
// line and dates none of them, which is the worst failure available
// here: it looks like it worked.
type tsCandidate struct {
	name   string
	re     *regexp.Regexp
	layout func(matches []string) (layout string, subs []Sub, flags tsFlags, warnings []string)
}

// candidates are tried in order and the first one that matches a
// majority of the sample lines wins. Order is most-specific-first:
// every dated form is tried before the bare time-of-day, which would
// otherwise match the time inside every one of them.
var candidates = []*tsCandidate{
	{
		name: "iso",
		re:   regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2})?`),
		layout: func(m []string) (string, []Sub, tsFlags, []string) {
			sep := "T"
			if len(m[0]) > 10 && m[0][10] == ' ' {
				sep = " "
			}
			return "2006-01-02" + sep + "15:04:05" + zoneLayout(m[0]), nil, tsFlags{}, nil
		},
	},
	{
		name: "iso-slash",
		re:   regexp.MustCompile(`\d{4}/\d{2}/\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?`),
		layout: func(m []string) (string, []Sub, tsFlags, []string) {
			sep := "T"
			if len(m[0]) > 10 && m[0][10] == ' ' {
				sep = " "
			}
			return "2006/01/02" + sep + "15:04:05", nil, tsFlags{}, nil
		},
	},
	{
		name: "clf",
		re:   regexp.MustCompile(`\d{2}/[A-Za-z]{3}/\d{4}:\d{2}:\d{2}:\d{2} [+-]\d{4}`),
		layout: func(m []string) (string, []Sub, tsFlags, []string) {
			return "02/Jan/2006:15:04:05 -0700", nil, tsFlags{}, nil
		},
	},
	{
		name: "dmy-dash",
		re:   regexp.MustCompile(`\d{2}-\d{2}-\d{4}[ T]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?`),
		layout: func(m []string) (string, []Sub, tsFlags, []string) {
			order, warn := dateOrder(m, `^(\d{2})-(\d{2})-`)
			sep := string(m[0][10])
			return order[0] + "-" + order[1] + "-2006" + sep + "15:04:05", nil, tsFlags{}, warn
		},
	},
	{
		name: "dmy-dot",
		re:   regexp.MustCompile(`\d{2}\.\d{2}\.\d{2,4}[ T]\d{2}:\d{2}:\d{2}(?:[.:,]\d+)?`),
		layout: func(m []string) (string, []Sub, tsFlags, []string) {
			order, warn := dateOrder(m, `^(\d{2})\.(\d{2})\.`)
			year := "2006"
			sep := " "
			if p := regexp.MustCompile(`^\d{2}\.\d{2}\.(\d{2,4})([ T])`).FindStringSubmatch(m[0]); p != nil {
				if len(p[1]) == 2 {
					year = "06"
				}
				sep = p[2]
			}
			// A colon before the sub-second part — 09:55:01:280 — is not
			// expressible in Go's layout language at all: it only knows
			// `.` and `,` as the fractional separator. A substitution
			// rewrites the captured text before parsing, which is what
			// the ts_regex_subs field exists for.
			var subs []Sub
			if colonMillis.MatchString(m[0]) {
				subs = []Sub{{
					Pattern:     `(\d{2}:\d{2}:\d{2}):(\d+)$`,
					Replacement: `$1.$2`,
				}}
			}
			return order[0] + "." + order[1] + "." + year + sep + "15:04:05", subs, tsFlags{}, warn
		},
	},
	{
		name: "syslog",
		re:   regexp.MustCompile(`\b[A-Z][a-z]{2} [ 0-9]\d \d{2}:\d{2}:\d{2}\b`),
		layout: func(m []string) (string, []Sub, tsFlags, []string) {
			return "Jan _2 15:04:05", nil, tsFlags{useMtimeYear: true},
				[]string{"this format carries no year — the year is taken from the file's modification time"}
		},
	},
	{
		name: "time-only",
		re:   regexp.MustCompile(`\d{2}:\d{2}:\d{2}(?:[.,]\d+)?`),
		layout: func(m []string) (string, []Sub, tsFlags, []string) {
			return "15:04:05", nil, tsFlags{useMtimeDate: true},
				[]string{"this format carries no date — the date is taken from the file's modification time, advancing a day at each midnight rollover"}
		},
	},
}

var colonMillis = regexp.MustCompile(`\d{2}:\d{2}:\d{2}:\d+$`)

// zoneLayout returns the layout fragment for whatever zone suffix the
// matched timestamp actually carries. `Z07:00` and `Z0700` are not
// interchangeable: each rejects the other's spelling outright.
func zoneLayout(matched string) string {
	switch {
	case strings.HasSuffix(matched, "Z"):
		return "Z07:00"
	case regexp.MustCompile(`[+-]\d{2}:\d{2}$`).MatchString(matched):
		return "Z07:00"
	case regexp.MustCompile(`[+-]\d{4}$`).MatchString(matched):
		return "Z0700"
	}
	return ""
}

// dateOrder decides whether a two-digit/two-digit date is day-first or
// month-first, by looking for a component above 12 anywhere in the
// sample.
//
// When neither component ever exceeds 12 the sample cannot settle it,
// and no amount of analysis will — 03-04 is the third of April to most
// of the world and the fourth of March in the US. Day-first is the
// default because it is the more common spelling with these separators,
// and the ambiguity is reported rather than hidden, since a silently
// transposed date is a bug that survives all the way to a conclusion.
func dateOrder(matches []string, prefix string) ([2]string, []string) {
	re := regexp.MustCompile(prefix)
	for _, m := range matches {
		p := re.FindStringSubmatch(m)
		if p == nil {
			continue
		}
		first, _ := strconv.Atoi(p[1])
		second, _ := strconv.Atoi(p[2])
		if first > 12 {
			return [2]string{"02", "01"}, nil
		}
		if second > 12 {
			return [2]string{"01", "02"}, nil
		}
	}
	return [2]string{"02", "01"}, []string{
		"day and month are both 12 or lower everywhere in the sample, so their order is a guess — day-first was assumed; swap 02 and 01 in ts_layout if that is wrong",
	}
}

// pickCandidate returns the first timestamp shape that appears in a
// majority of the sample lines, along with the text it matched in each.
func pickCandidate(lines []string) (*tsCandidate, []string) {
	need := (len(lines) + 1) / 2
	for _, c := range candidates {
		matches := make([]string, 0, len(lines))
		for _, ln := range lines {
			if m := c.re.FindString(ln); m != "" {
				matches = append(matches, m)
			}
		}
		if len(matches) >= need {
			return c, matches
		}
	}
	return nil, nil
}

type tokenKind uint8

const (
	kindSpace tokenKind = iota
	kindPunct
	kindWord
	kindNumber
	kindLevel
	kindTimestamp
)

type token struct {
	kind tokenKind
	text string
}

var levelWords = map[string]bool{
	"trace": true, "debug": true, "dbg": true, "info": true, "inf": true,
	"notice": true, "warn": true, "warning": true, "wrn": true,
	"error": true, "err": true, "fatal": true, "crit": true, "critical": true,
	"severe": true, "verbose": true, "fine": true, "finer": true, "finest": true,
	"panic": true, "alert": true, "emerg": true,
}

// tokenize splits one line into the token stream the alignment walk
// compares. The timestamp is located first and taken whole, so the
// walk never has to reassemble it from the digits and punctuation it
// would otherwise decompose into.
func tokenize(line string, cand *tsCandidate, limit int) []token {
	var tsStart, tsEnd = -1, -1
	if cand != nil {
		if loc := cand.re.FindStringIndex(line); loc != nil {
			tsStart, tsEnd = loc[0], loc[1]
		}
	}

	out := make([]token, 0, 16)
	i := 0
	for i < len(line) && len(out) < limit {
		if i == tsStart {
			out = append(out, token{kind: kindTimestamp, text: line[tsStart:tsEnd]})
			i = tsEnd
			continue
		}
		// A run never crosses into the timestamp: the digits of a date
		// belong to the timestamp token, not to the number before it.
		stop := len(line)
		if tsStart > i {
			stop = tsStart
		}

		r, size := utf8.DecodeRuneInString(line[i:])
		switch {
		case r == ' ' || r == '\t':
			j := scanRun(line, i, stop, func(r rune) bool { return r == ' ' || r == '\t' })
			out = append(out, token{kind: kindSpace, text: line[i:j]})
			i = j
		case unicode.IsDigit(r):
			j := scanRun(line, i, stop, unicode.IsDigit)
			out = append(out, token{kind: kindNumber, text: line[i:j]})
			i = j
		case unicode.IsLetter(r) || r == '_':
			j := scanRun(line, i, stop, func(r rune) bool {
				return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
			})
			text := line[i:j]
			k := kindWord
			if levelWords[strings.ToLower(text)] {
				k = kindLevel
			}
			out = append(out, token{kind: k, text: text})
			i = j
		default:
			out = append(out, token{kind: kindPunct, text: line[i : i+size]})
			i += size
		}
	}
	return out
}

func scanRun(s string, from, stop int, ok func(rune) bool) int {
	j := from
	for j < stop {
		r, size := utf8.DecodeRuneInString(s[j:])
		if !ok(r) {
			break
		}
		j += size
	}
	return j
}

// buildPattern emits a regex from the aligned token streams: literal
// where the lines agree, a capture where they differ in a way that
// still looks like a field, and a message tail once they stop agreeing
// at all.
//
// It runs the walk up to twice. The timestamp has to land at the same
// token index in every line for a shared prefix to exist, and the walk
// can also stop short of the timestamp on its own. Either way the
// answer is the same: throw the prefix away, align on the timestamp
// itself, and let a lazy `.*?` cover whatever came before.
func buildPattern(lines [][]token, cand *tsCandidate) (string, []string) {
	if _, aligned := timestampIndex(lines); aligned {
		if pattern, ok := walkPattern(lines, cand, "^"); ok {
			return pattern, nil
		}
	}
	realigned := realign(lines)
	if pattern, ok := walkPattern(realigned, cand, `^.*?`); ok {
		return pattern, []string{
			"the text before the timestamp varies too much to describe, so it is skipped rather than captured",
		}
	}
	return `^.*?(?P<ts>` + cand.re.String() + `)\s*(?P<message>.*)$`, []string{
		"only the timestamp could be located reliably; everything else is left in the message",
	}
}

// walkPattern is one pass of the alignment walk. It reports false when
// it stopped before reaching the timestamp, which makes the pattern
// useless — a rule that matches lines without dating them.
//
// The recurring hazard here is accidental agreement. A three-line
// sample is a tiny amount of evidence, and any token that happens to
// repeat across it looks exactly like structure: a process ID that did
// not change, a word that starts two messages, a level in a calm
// stretch of the file. Freezing one of those into the regex produces a
// rule that parses the sample perfectly and rejects the rest of the
// file, which is the worst way to be wrong here — it passes the demo.
//
// So agreement alone is not enough to make something literal. Each kind
// has to earn it:
//
//   - Spaces never do. Column-aligned formats pad to a width that moves
//     with the value, so a run of spaces copied from one line rejects
//     the others.
//   - Levels never do, for the reason above.
//   - Numbers never do. Numbers in a log header are values — pids,
//     ports, sizes — and a genuinely constant one is rare enough that
//     capturing it costs nothing when it happens.
//   - Words do, but only when punctuation closes them, as in `PID:` or
//     `[thread=`. An undelimited word is where the header ends and
//     prose begins.
func walkPattern(lines [][]token, cand *tsCandidate, prefix string) (string, bool) {
	var b strings.Builder
	b.WriteString(prefix)

	names := newNameSet()
	// literals tracks the most recent literal tokens, so a capture that
	// follows `PID:` can be called pid instead of field2.
	var literals []token
	captures := 0
	anon := 0
	emittedTS := false

	shortest := len(lines[0])
	for _, l := range lines {
		if len(l) < shortest {
			shortest = len(l)
		}
	}
	stopped := shortest

	capture := func(want, pattern string) {
		if want == "" {
			anon++
			want = "field" + strconv.Itoa(anon)
		}
		b.WriteString("(?P<" + names.take(want) + ">" + pattern + ")")
		captures++
		literals = nil
	}

walk:
	for i := 0; i < shortest && i < maxTokens; i++ {
		kind, ok := commonKind(lines, i)
		if !ok {
			stopped = i
			break
		}
		if kind != kindSpace && kind != kindTimestamp && captures >= maxCaptures {
			stopped = i
			break
		}

		switch kind {
		case kindTimestamp:
			if emittedTS {
				stopped = i
				break walk
			}
			b.WriteString("(?P<ts>" + cand.re.String() + ")")
			emittedTS = true
			literals = nil

		case kindSpace:
			b.WriteString(`\s+`)
			literals = nil

		case kindLevel:
			capture("level", "[A-Za-z]+")

		case kindNumber:
			// A dotted run of numbers is one value — an address, a
			// version — not three fields with periods between them.
			if end := dottedNumberRun(lines, i, shortest); end > i {
				capture(labelName(literals), `[\d.]+`)
				i = end
				continue
			}
			capture(labelName(literals), `\d+`)

		case kindWord:
			delim, delimited := closingDelimiter(lines, i)
			if allSame(lines, i) {
				if !delimited {
					stopped = i
					break walk
				}
				tok := lines[0][i]
				b.WriteString(regexp.QuoteMeta(tok.text))
				literals = append(literals, tok)
				continue
			}
			if !delimited {
				stopped = i
				break walk
			}
			capture(labelName(literals), "[^\\s"+regexp.QuoteMeta(delim)+"]+")

		case kindPunct:
			if !allSame(lines, i) {
				stopped = i
				break walk
			}
			tok := lines[0][i]
			b.WriteString(regexp.QuoteMeta(tok.text))
			literals = append(literals, tok)

		default:
			stopped = i
			break walk
		}
	}

	if !emittedTS {
		return "", false
	}

	// Absorb the whitespace at the stop point into the separator rather
	// than the message, so messages don't all begin with a space.
	if stopped < shortest {
		if kind, ok := commonKind(lines, stopped); ok && kind == kindSpace {
			b.WriteString(`\s+`)
		}
	}
	b.WriteString("(?P<message>.*)$")
	return b.String(), true
}

// dottedNumberRun returns the index of the last token in a
// number.number(.number…) run starting at i, or i when there is no run.
func dottedNumberRun(lines [][]token, i, shortest int) int {
	end := i
	for end+2 < shortest {
		if !tokenIs(lines, end+1, kindPunct, ".") {
			break
		}
		if k, ok := commonKind(lines, end+2); !ok || k != kindNumber {
			break
		}
		end += 2
	}
	return end
}

func tokenIs(lines [][]token, i int, kind tokenKind, text string) bool {
	for _, l := range lines {
		if i >= len(l) || l[i].kind != kind || l[i].text != text {
			return false
		}
	}
	return true
}

// timestampIndex reports the token index of the timestamp when every
// line agrees on it.
func timestampIndex(lines [][]token) (int, bool) {
	idx := -1
	for _, l := range lines {
		found := -1
		for i, t := range l {
			if t.kind == kindTimestamp {
				found = i
				break
			}
		}
		if found < 0 {
			return 0, false
		}
		if idx == -1 {
			idx = found
		} else if idx != found {
			return 0, false
		}
	}
	return idx, idx >= 0
}

// realign drops each line's tokens up to its own timestamp, so lines
// whose prefixes differ in shape can still be compared from the
// timestamp onwards. Lines with no timestamp at all are dropped.
func realign(lines [][]token) [][]token {
	out := make([][]token, 0, len(lines))
	for _, l := range lines {
		for i, t := range l {
			if t.kind == kindTimestamp {
				out = append(out, l[i:])
				break
			}
		}
	}
	if len(out) == 0 {
		return lines
	}
	return out
}

// commonKind returns the kind every line has at index i, treating a
// level word and an ordinary word as the same kind — one line's "INFO"
// and another's "Analyzing" occupy the same slot, and the slot is a
// level if enough lines think so.
func commonKind(lines [][]token, i int) (tokenKind, bool) {
	levels := 0
	words := 0
	var other tokenKind
	haveOther := false
	for _, l := range lines {
		if i >= len(l) {
			return 0, false
		}
		switch l[i].kind {
		case kindLevel:
			levels++
		case kindWord:
			words++
		default:
			if haveOther && l[i].kind != other {
				return 0, false
			}
			other = l[i].kind
			haveOther = true
		}
	}
	if haveOther {
		if levels > 0 || words > 0 {
			return 0, false
		}
		return other, true
	}
	if levels*2 >= levels+words {
		return kindLevel, true
	}
	return kindWord, true
}

func allSame(lines [][]token, i int) bool {
	first := lines[0][i].text
	for _, l := range lines {
		if l[i].text != first {
			return false
		}
	}
	return true
}

// closingDelimiter reports the punctuation that every line places
// immediately after index i, which is what makes a varying word safe to
// capture instead of a reason to stop.
func closingDelimiter(lines [][]token, i int) (string, bool) {
	if i+1 >= len(lines[0]) {
		return "", false
	}
	first := lines[0][i+1]
	if first.kind != kindPunct {
		return "", false
	}
	for _, l := range lines {
		if i+1 >= len(l) || l[i+1].kind != kindPunct || l[i+1].text != first.text {
			return "", false
		}
	}
	return first.text, true
}

// labelName reads a capture's name off the literal text just before it,
// when that text is a label — `PID:` or `thread=`. Returns "" when
// there is nothing to go on, and the caller falls back to field1,
// field2, which the operator renames.
func labelName(literals []token) string {
	if len(literals) < 2 {
		return ""
	}
	last := literals[len(literals)-1]
	prev := literals[len(literals)-2]
	if last.kind != kindPunct || (last.text != ":" && last.text != "=") {
		return ""
	}
	if prev.kind != kindWord {
		return ""
	}
	s, err := Slug(prev.text)
	if err != nil {
		return ""
	}
	return strings.ReplaceAll(s, "-", "_")
}

// nameSet hands out capture-group names, keeping them unique. Go's
// regexp rejects a duplicated group name outright, so a format with two
// levels or two labelled `id:` fields would otherwise produce a regex
// that does not compile at all.
type nameSet struct{ used map[string]bool }

func newNameSet() *nameSet {
	// ts and message are reserved: the adapter requires the first and
	// appends continuation lines to the second.
	return &nameSet{used: map[string]bool{"ts": true, "message": true}}
}

func (n *nameSet) take(want string) string {
	if !n.used[want] {
		n.used[want] = true
		return want
	}
	for i := 2; ; i++ {
		alt := want + strconv.Itoa(i)
		if !n.used[alt] {
			n.used[alt] = true
			return alt
		}
	}
}
