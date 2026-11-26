package tools

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ericflo/eagent/internal/event"
	"github.com/ericflo/eagent/internal/store"
)

// Archive exposes a session's own log to eagent, so it can follow the
// file:line places its briefing cites and look up what the briefing left out.
type Archive struct {
	Path string // session directory
}

// Read renders lines from..to (1-based, inclusive) of one file. Each line is
// prefixed with file:line so it can be cited. A result holds whole lines up
// to maxChars; a line too long for one result on its own is cut, on a rune
// boundary, and the result says which offset reads on. With offset >= 0 it
// reads line from alone, whole (the readable form is not shortened), from
// that offset into its text: the way to every byte of a line, however long.
func (a Archive) Read(file string, from, to int, raw bool, offset, maxChars int) (string, error) {
	if strings.ContainsAny(file, "/\\") || !strings.HasSuffix(file, ".jsonl") {
		return "", fmt.Errorf("file must be one of the session's record files, named like 1788740857293.jsonl")
	}
	if from < 1 {
		from = 1
	}
	if to < from {
		to = from + 39
	}
	evs, last, err := readLines(filepath.Join(a.Path, file), from, to)
	if err != nil {
		return "", err
	}
	if from > last {
		return fmt.Sprintf("%s has only %d lines", file, last), nil
	}
	if offset >= 0 {
		if len(evs) == 0 || evs[0].Source.Line != from {
			return fmt.Sprintf("%s:%d is a blank line; there is nothing in it to read\n", file, from), nil
		}
		return readPiece(file, evs[0], raw, offset, maxChars), nil
	}
	if len(evs) == 0 {
		return fmt.Sprintf("%s:%d to %d are blank lines\n", file, from, min(to, last)), nil
	}
	rawArg := ""
	if raw {
		rawArg = " raw=true"
	}
	var b strings.Builder
	for _, ev := range evs {
		var entry string
		if raw {
			entry = fmt.Sprintf("%s:%d %s\n", file, ev.Source.Line, wholeLine(ev, true))
		} else {
			head, body := summaryParts(ev)
			entry = fmt.Sprintf("%s:%d %s", file, ev.Source.Line, shorten(head, body, 400))
			if len(body) > 400 {
				entry += fmt.Sprintf(" [shortened; from=%d offset=0 reads it whole]", ev.Source.Line)
			}
			entry += "\n"
		}
		if maxChars <= 0 || b.Len()+len(entry) <= maxChars {
			b.WriteString(entry)
			continue
		}
		if b.Len() > 0 {
			// The next line is read whole by the next call.
			fmt.Fprintf(&b, "[... output limit reached before line %d of %d; call again with from=%d%s ...]\n", ev.Source.Line, last, ev.Source.Line, rawArg)
			break
		}
		// One line longer than a whole result: its first piece.
		b.WriteString(readPiece(file, ev, raw, 0, maxChars))
		break
	}
	return b.String(), nil
}

// readLines parses the events on lines from..to of one record file, each
// with the line it sits on (a blank line is counted and holds no event, as
// store.ReadFile does), and counts the file's committed lines. Only the
// lines asked for are parsed: a long session is read in pages, and each
// page would otherwise parse the whole file again.
func readLines(path string, from, to int) ([]event.Event, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	name := filepath.Base(path)
	r := bufio.NewReaderSize(f, 1<<20)
	var evs []event.Event
	line := 0
	for line < to {
		if line+1 < from {
			// Skip a line before the range without keeping it.
			done, err := skipLine(r)
			if err != nil {
				return nil, 0, err
			}
			if !done {
				return evs, line, nil // the uncommitted tail, or the end
			}
			line++
			continue
		}
		raw, err := r.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			// An uncommitted tail is not a line yet.
			return evs, line, nil
		}
		if err != nil {
			return nil, 0, err
		}
		line++
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 {
			continue
		}
		var ev event.Event
		if jerr := json.Unmarshal(trimmed, &ev); jerr != nil {
			return nil, 0, fmt.Errorf("%s:%d: corrupt event: %w", name, line, jerr)
		}
		ev.Source = event.Source{File: name, Line: line}
		evs = append(evs, ev)
	}
	// The lines after the range are only counted.
	buf := make([]byte, 64<<10)
	for {
		n, err := r.Read(buf)
		line += bytes.Count(buf[:n], []byte{'\n'})
		if errors.Is(err, io.EOF) {
			return evs, line, nil
		}
		if err != nil {
			return nil, 0, err
		}
	}
}

// skipLine reads past one newline-terminated line; done is false when the
// file ends first.
func skipLine(r *bufio.Reader) (done bool, err error) {
	for {
		_, err := r.ReadSlice('\n')
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return false, nil
		default:
			return false, err
		}
	}
}

// wholeLine is one event as session_read renders it whole: its JSON, or
// the readable form not shortened.
func wholeLine(ev event.Event, raw bool) string {
	if raw {
		line, _ := json.Marshal(ev)
		return string(line)
	}
	return Summarize(ev, 0)
}

// readPiece renders the piece of one line that starts at offset (a byte
// offset into wholeLine, moved back to a rune boundary) and fits in
// maxChars, and says how to read on when more remains.
func readPiece(file string, ev event.Event, raw bool, offset, maxChars int) string {
	text, line := wholeLine(ev, raw), ev.Source.Line
	rawArg := ""
	if raw {
		rawArg = " raw=true"
	}
	if offset >= len(text) {
		return fmt.Sprintf("%s:%d is %d long in this form; offset %d is past its end, so there is nothing more to read (from=%d offset=0%s reads it from the start)\n", file, line, len(text), offset, line, rawArg)
	}
	for offset > 0 && !utf8.RuneStart(text[offset]) {
		offset--
	}
	head := fmt.Sprintf("%s:%d [offset %d of %d] ", file, line, offset, len(text))
	end := len(text)
	if maxChars > 0 {
		// Room for the note that says how to read on.
		room := max(maxChars-len(head)-200, 256)
		if offset+room < end {
			end = offset + room
			for end > offset && !utf8.RuneStart(text[end]) {
				end--
			}
		}
	}
	out := head + text[offset:end] + "\n"
	if end < len(text) {
		out += fmt.Sprintf("[... line %d goes on: %d of %d shown so far; call again with from=%d offset=%d%s for the rest ...]\n", line, end, len(text), line, end, rawArg)
	}
	return out
}

// MaxSearchResults is the most one search returns, whatever was asked for.
const MaxSearchResults = 200

// Search finds events whose text contains query, ignoring case and
// treating any run of spaces and line breaks as one space. The text is what
// a person reads in the event: its fields decoded from JSON, without the
// provider's replay payload, so "a && b", "<div>" or a path with
// backslashes match, and a short word does not match inside signed or
// encrypted reasoning.
func (a Archive) Search(query, file string, maxResults int) (string, error) {
	if strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("query is required")
	}
	entries, err := os.ReadDir(a.Path)
	if err != nil {
		return "", err
	}
	if maxResults <= 0 {
		maxResults = 40
	}
	// A result is a line of up to 300 characters, and the whole list goes
	// into a prompt.
	maxResults = min(maxResults, MaxSearchResults)
	m := newMatcher(query)
	var b strings.Builder
	n := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") || (file != "" && name != file) {
			continue
		}
		evs, err := store.ReadFile(filepath.Join(a.Path, name))
		if err != nil {
			continue
		}
		for _, ev := range evs {
			if !m.mayMatch(ev) || !strings.Contains(searchText(ev), m.text) {
				continue
			}
			head, body := summaryParts(ev)
			shown := shorten(head, body, 300)
			fmt.Fprintf(&b, "%s:%d %s%s\n", name, ev.Source.Line, shown, m.place(ev, shown, len(body) > 300))
			n++
			if n >= maxResults {
				fmt.Fprintf(&b, "[... %d results shown; narrow the query or set a file ...]\n", n)
				return b.String(), nil
			}
		}
	}
	if n == 0 {
		return "no matches", nil
	}
	return b.String(), nil
}

// matcher is one search query: the text it matches, and patterns that
// find it in the two forms session_read shows.
type matcher struct {
	text string // lower case, spaces collapsed
	// readable finds the query in the readable form, where a line break is
	// shown as ⏎; escaped finds it as JSON writes it (\u003c for <, \\ for
	// a backslash), in the raw form and in tool arguments.
	readable, escaped *regexp.Regexp
	// must are the query's words JSON writes as they are; an event whose
	// JSON lacks one cannot match, and is passed over without decoding it.
	must []string
}

func newMatcher(query string) matcher {
	words := strings.Fields(query)
	plain := make([]string, len(words))
	esc := make([]string, len(words))
	var must []string
	for i, w := range words {
		if verbatimInJSON(w) {
			must = append(must, strings.ToLower(w))
		}
		plain[i] = regexp.QuoteMeta(w)
		j, _ := json.Marshal(w)
		esc[i] = regexp.QuoteMeta(string(j[1 : len(j)-1]))
	}
	// Between words: spaces, the ⏎ mark, or a JSON-escaped break.
	const gap = `(?:\s|⏎|\\[nrt])+`
	return matcher{
		text:     searchFold(query),
		readable: regexp.MustCompile("(?i)" + strings.Join(plain, gap)),
		escaped:  regexp.MustCompile("(?i)" + strings.Join(esc, gap)),
		must:     must,
	}
}

// verbatimInJSON reports whether any JSON encoder writes w as it is: plain
// ASCII with nothing an encoder escapes (Go escapes <, > and &; some
// encoders escape /).
func verbatimInJSON(w string) bool {
	for i := 0; i < len(w); i++ {
		c := w[i]
		if c < 0x20 || c >= 0x7f || strings.IndexByte(`<>&"\\/`, c) >= 0 {
			return false
		}
	}
	return true
}

// mayMatch is the quick test before an event is decoded for a search.
func (m matcher) mayMatch(ev event.Event) bool {
	if len(m.must) == 0 || legacyTypes[ev.Type] {
		// A legacy event's readable form has words of its own.
		return true
	}
	hay := strings.ToLower(string(ev.Data) + "\x00" + ev.Type + "\x00" + ev.Actor + "\x00" + ev.Task)
	for _, w := range m.must {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

var legacyTypes = map[string]bool{
	event.LegacyTaskCreate: true, event.LegacyTaskStart: true, event.LegacyTaskEnd: true,
	event.LegacyNote: true, event.LegacyYield: true, event.LegacyProcStart: true,
	event.LegacyProcPID: true, event.LegacyProcExit: true, event.LegacyNarratorTrim: true,
}

// place says, for a hit whose match the result line does not show (it was
// shortened, or the match is in a field the line leaves out), where in the
// event the match is and what session_read takes to show it.
func (m matcher) place(ev event.Event, shown string, shortened bool) string {
	line := ev.Source.Line
	if !shortened && (m.readable.MatchString(shown) || m.escaped.MatchString(shown)) {
		return ""
	}
	note := ""
	if shortened {
		note = "shortened; "
	}
	for _, raw := range []bool{false, true} {
		text := wholeLine(ev, raw)
		skipFrom, skipTo := -1, -1
		if raw {
			skipFrom, skipTo = nativeSpan(text)
		}
		at := -1
		for _, re := range []*regexp.Regexp{m.readable, m.escaped} {
			if loc := firstOutside(re, text, skipFrom, skipTo); loc >= 0 && (at < 0 || loc < at) {
				at = loc
			}
		}
		if at < 0 {
			continue
		}
		// A little before the match, so it is read in context.
		at = max(0, at-200)
		for at > 0 && !utf8.RuneStart(text[at]) {
			at--
		}
		rawArg := ""
		if raw {
			rawArg = " raw=true"
		}
		return fmt.Sprintf(" [%sthe match is read with from=%d offset=%d%s]", note, line, at, rawArg)
	}
	if shortened {
		return fmt.Sprintf(" [shortened; from=%d offset=0 reads it whole]", line)
	}
	return ""
}

// firstOutside is where re first matches in text outside [from, to) (the
// provider's replay payload), or -1. Only the first match is looked for:
// a line can be a megabyte with the query in it thousands of times. No
// pattern of a query is anchored, so looking on from the end of the
// payload finds what a search of the whole line would.
func firstOutside(re *regexp.Regexp, text string, from, to int) int {
	loc := re.FindStringIndex(text)
	if loc == nil {
		return -1
	}
	if loc[0] < from || loc[0] >= to {
		return loc[0]
	}
	if loc = re.FindStringIndex(text[to:]); loc == nil {
		return -1
	}
	return to + loc[0]
}

// searchText is the text a query is matched against: the event's type,
// actor and task, its readable form, and every string in its data decoded,
// except the provider's replay payload (an assistant event's native).
// Fields are kept apart, so a phrase does not match across two of them.
func searchText(ev event.Event) string {
	parts := []string{ev.Type, ev.Actor, ev.Task}
	if b, ok := summarizeLegacy(ev); ok {
		parts = append(parts, b)
	}
	var data any
	dec := json.NewDecoder(bytes.NewReader(ev.Data))
	dec.UseNumber()
	if len(ev.Data) > 0 && dec.Decode(&data) == nil {
		if obj, ok := data.(map[string]any); ok && ev.Type == event.Assistant {
			delete(obj, "native")
		}
		parts = appendStrings(parts, data)
	}
	return searchFold(strings.Join(parts, "\x00"))
}

func appendStrings(parts []string, v any) []string {
	switch x := v.(type) {
	case string:
		parts = append(parts, x)
	case map[string]any:
		for _, e := range x {
			parts = appendStrings(parts, e)
		}
	case []any:
		for _, e := range x {
			parts = appendStrings(parts, e)
		}
	}
	return parts
}

// searchFold lower-cases s and makes every run of white space one space.
func searchFold(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

// nativeSpan is where an event's data.native value sits in its JSON line,
// or -1, -1 when it has none.
func nativeSpan(line string) (int, int) {
	dec := json.NewDecoder(strings.NewReader(line))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return -1, -1
	}
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return -1, -1
		}
		if k != "data" {
			var skip json.RawMessage
			if dec.Decode(&skip) != nil {
				return -1, -1
			}
			continue
		}
		if t, err := dec.Token(); err != nil || t != json.Delim('{') {
			return -1, -1
		}
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return -1, -1
			}
			start := dec.InputOffset()
			var v json.RawMessage
			if dec.Decode(&v) != nil {
				return -1, -1
			}
			if k == "native" {
				return int(start), int(dec.InputOffset())
			}
		}
		return -1, -1
	}
	return -1, -1
}

// Summarize renders one event as a compact human-readable line.
func Summarize(ev event.Event, max int) string {
	head, body := summaryParts(ev)
	return shorten(head, body, max)
}

// shorten puts a summary's parts together with the body cut to about max
// (0 for whole).
func shorten(head, body string, max int) string {
	if max > 0 && len(body) > max {
		body = oneLine(Truncate(body, max))
	}
	return head + body
}

// summaryParts splits Summarize's line into its "time type actor: " head
// and its one-line body, which alone is shortened.
func summaryParts(ev event.Event) (head, body string) {
	ts := ev.Time.Format("15:04:05")
	who := ev.Actor
	if ev.Task != "" {
		who += "(" + ev.Task + ")"
	}
	switch ev.Type {
	case event.UserMessage:
		var d event.UserMessageData
		_ = ev.Decode(&d)
		body = d.Text
	case event.UserAnswer:
		var d event.UserAnswerData
		_ = ev.Decode(&d)
		body = d.Text
	case event.UserDismiss:
		var d event.UserDismissData
		_ = ev.Decode(&d)
		body = "dismissed " + d.QuestionID + " without answering"
	case event.Assistant:
		var d event.AssistantData
		_ = ev.Decode(&d)
		var parts []string
		if d.Text != "" {
			parts = append(parts, d.Text)
		}
		for _, tc := range d.ToolCalls {
			parts = append(parts, fmt.Sprintf("%s(%s)", tc.Name, oneLine(string(tc.Args))))
		}
		body = strings.Join(parts, " | ")
	case event.ToolResult:
		var d event.ToolResultData
		_ = ev.Decode(&d)
		body = d.Name + " -> " + d.Output
		if d.IsError {
			body = d.Name + " ERROR -> " + d.Output
		}
	case event.Message:
		var d event.MessageData
		_ = ev.Decode(&d)
		body = d.Text
	case event.Question:
		var d event.QuestionData
		_ = ev.Decode(&d)
		body = d.Text
	case event.Dossier:
		var d event.DossierData
		_ = ev.Decode(&d)
		body = d.Text
	case event.HarnessMessage:
		var d event.HarnessMessageData
		_ = ev.Decode(&d)
		body = d.Text
	case event.Steer:
		var d event.SteerData
		_ = ev.Decode(&d)
		body = d.Text
	case event.RelayTurn:
		var d event.RelayTurnData
		_ = ev.Decode(&d)
		body = d.Name + " finished a turn: " + d.Text
	case event.RelayProgress:
		var d event.RelayProgressData
		_ = ev.Decode(&d)
		body = d.Name + " said: " + d.Text
	case event.RelaySend:
		var d event.RelaySendData
		_ = ev.Decode(&d)
		body = fmt.Sprintf("to %s from %s (%s, %s): %s", d.Name, d.From, d.ID, d.Mode, d.Text)
	case event.RelayState:
		var d event.RelayStateData
		_ = ev.Decode(&d)
		body = d.Name + " " + d.State
		if d.Detail != "" {
			body += ": " + d.Detail
		}
	default:
		if b, ok := summarizeLegacy(ev); ok {
			body = b
		} else {
			body = string(ev.Data)
		}
	}
	return fmt.Sprintf("%s %s %s: ", ts, ev.Type, who), oneLine(body)
}

// The data of event types only logs from before eagent was one loop hold,
// as they were written then.
type (
	legacyTaskCreate struct {
		ID, Title, Description, Kind string
	}
	legacyTaskStart struct {
		ID string
	}
	legacyTaskEnd struct {
		ID, Status, Summary string
	}
	legacyNote struct {
		Text   string
		Answer bool
	}
	legacyProcStart struct {
		Handle, Command, Cwd string
	}
	legacyProcPID struct {
		Handle string
		PID    int
	}
	legacyProcExit struct {
		Handle   string
		ExitCode int `json:"exit_code"`
		Reason   string
		Tail     string
	}
	legacyNarratorTrim struct {
		FromSeq int64 `json:"from_seq"`
		Recap   string
	}
)

// summarizeLegacy renders the events of the three-actor design readably;
// ok is false for any other type.
func summarizeLegacy(ev event.Event) (string, bool) {
	switch ev.Type {
	case event.LegacyTaskCreate:
		var d legacyTaskCreate
		_ = ev.Decode(&d)
		body := d.ID + " " + d.Title + ": " + d.Description
		if d.Kind == "dossier" {
			body = d.ID + " (notes for a fresh context) " + d.Title + ": " + d.Description
		}
		return body, true
	case event.LegacyTaskStart:
		var d legacyTaskStart
		_ = ev.Decode(&d)
		return d.ID + " started", true
	case event.LegacyTaskEnd:
		var d legacyTaskEnd
		_ = ev.Decode(&d)
		return d.ID + " " + d.Status + ": " + d.Summary, true
	case event.LegacyNote:
		var d legacyNote
		_ = ev.Decode(&d)
		if d.Answer {
			return "(answer) " + d.Text, true
		}
		return d.Text, true
	case event.LegacyYield:
		var d event.LegacyYieldData
		_ = ev.Decode(&d)
		if d.Done {
			return "done: " + d.Reason, true
		}
		return "waiting: " + d.Reason, true
	case event.LegacyProcStart:
		var d legacyProcStart
		_ = ev.Decode(&d)
		body := d.Handle + " " + d.Command
		if d.Cwd != "" {
			body += " (in " + d.Cwd + ")"
		}
		return body, true
	case event.LegacyProcPID:
		var d legacyProcPID
		_ = ev.Decode(&d)
		return fmt.Sprintf("%s pid %d", d.Handle, d.PID), true
	case event.LegacyProcExit:
		var d legacyProcExit
		_ = ev.Decode(&d)
		body := fmt.Sprintf("%s %s code=%d", d.Handle, d.Reason, d.ExitCode)
		if d.Tail != "" {
			body += ": " + d.Tail
		}
		return body, true
	case event.LegacyNarratorTrim:
		var d legacyNarratorTrim
		_ = ev.Decode(&d)
		body := fmt.Sprintf("narrator conversation trimmed from #%d", d.FromSeq)
		if d.Recap != "" {
			body += ": " + d.Recap
		}
		return body, true
	}
	return "", false
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "⏎ ")
	return strings.TrimSpace(s)
}

// Truncate bounds text to about max characters, keeping the head and the
// tail, without splitting a UTF-8 sequence.
func Truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	head := max * 2 / 3
	tail := max - head
	for head > 0 && !utf8.RuneStart(s[head]) {
		head--
	}
	tailStart := len(s) - tail
	for tailStart < len(s) && !utf8.RuneStart(s[tailStart]) {
		tailStart++
	}
	return s[:head] + fmt.Sprintf("\n\n[... %d characters omitted; %d total ...]\n\n", tailStart-head, len(s)) + s[tailStart:]
}
