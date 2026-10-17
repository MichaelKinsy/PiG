// Session resume + listing helpers.
//
// Splits the high-level "find the right session to resume" logic out
// of session.go so the load primitive (loadSessionFile) stays small.
//
// Mirrors:
//   - upstream `findMostRecentSession`: newest mtime in dir.
//   - upstream `SessionInfo` shape on `list`: id, cwd, name, parent,
//     modified, message_count, first_message, all_messages_text.

package codingagent

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsonvalid"
	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
	"github.com/MichaelKinsy/PiG/internal/nodefs"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
	"github.com/MichaelKinsy/PiG/internal/procpolicy"
)

// SessionInfo is the picker-display summary of a session JSONL.
// Returned by ListSessions; consumed by /resume picker (TUI overlay
// follow-up) and by --continue startup flag.
type SessionInfo struct {
	Path              string
	ID                string
	CWD               string
	Name              string // user-set name (via /name): empty when unset
	ParentSessionPath string // path of source jsonl when this session is a clone
	Created           time.Time
	Modified          time.Time
	MessageCount      int
	FirstMessage      string // first user message text: for picker preview
	AllMessagesText   string // concatenated text: for fuzzy search
}

const maxConcurrentSessionInfoLoads = 10 // upstream: coding-agent/src/core/session-manager.ts:MAX_CONCURRENT_SESSION_INFO_LOADS

// ListSessions returns every valid jsonl in this manager's session
// directory, newest message activity first. Files that fail header parsing are
// silently skipped (matches upstream: corrupted sessions shouldn't
// crash the picker).
func (sm *SessionManager) ListSessions(options ...SessionListOptions) ([]SessionInfo, error) {
	if len(options) == 0 {
		return listSessionsInDir(sm.sessionDir)
	}
	return listSessionsInDirWithOptions(sm.sessionDir, sessionListOptions(options))
}

// ListCurrentSessions returns the selector's Current Folder scope. A custom
// session directory may contain sessions from several projects, so it filters
// by header cwd; the default encoded directory is already cwd-scoped.
func (sm *SessionManager) ListCurrentSessions(options ...SessionListOptions) ([]SessionInfo, error) {
	selected := sessionListOptions(options)
	filter := !sm.sessionDirIsCwdScoped()
	if filter && selected.OnProgress != nil {
		progress := selected.OnProgress
		selected.OnProgress = func(loaded, total int, partial []SessionInfo) {
			if partial != nil {
				filtered := make([]SessionInfo, 0, len(partial))
				for _, info := range partial {
					if sessionCwdMatches(info.CWD, sm.cwd) {
						filtered = append(filtered, info)
					}
				}
				partial = filtered
			}
			progress(loaded, total, partial)
		}
	}
	infos, err := sm.ListSessions(selected)
	if err != nil || !filter {
		return infos, err
	}
	out := make([]SessionInfo, 0, len(infos))
	for _, info := range infos {
		if sessionCwdMatches(info.CWD, sm.cwd) {
			out = append(out, info)
		}
	}
	return out, nil
}

// ListAllSessions returns every valid session JSONL under
// <agentDir>/sessions/*/*.jsonl, newest message activity first.
// Mirrors upstream session selector "All" scope.
func (sm *SessionManager) ListAllSessions(options ...SessionListOptions) ([]SessionInfo, error) {
	selected := sessionListOptions(options)
	if !sm.sessionDirIsCwdScoped() {
		return listSessionsInDirWithOptions(sm.sessionListRoot(), selected)
	}
	return listSessionsAcrossRootWithOptions(sm.sessionListRoot(), selected)
}

// sessionListRoot is the directory the All scope lists: a custom session directory itself, or the agent sessions directory that holds every project's session directory.
func (sm *SessionManager) sessionListRoot() string {
	if !sm.sessionDirIsCwdScoped() {
		return sm.sessionDir
	}
	return filepath.Join(AgentDir(), "sessions")
}

func listSessionsAcrossRoot(root string) ([]SessionInfo, error) {
	return listSessionsAcrossRootWithOptions(root, sessionListOptions(nil))
}

var listSessionsInDir = func(dir string) ([]SessionInfo, error) {
	return listSessionsInDirWithOptions(dir, sessionListOptions(nil))
}

func summarizeSessionFiles(files []string) []SessionInfo {
	infos, _ := summarizeSessionFilesWithOptions(files, sessionListOptions(nil), 10, false)
	return infos
}

// noMessagesText is the first message of a session that holds no text, as buildSessionInfo sets it.
const noMessagesText = "(no messages)"

var errSessionDropped = errors.New("session: not listed")

// summarizeSessionFile streams picker metadata and searchable text without retaining entry objects.
func summarizeSessionFile(path string) (SessionInfo, error) {
	return summarizeSessionFileContext(context.Background(), path)
}

// jsDateValue is the time value of `new Date(value)` for a parsed JSON value; present tells an absent member (undefined) from null. Objects and arrays read as an invalid date.
func jsDateValue(value any, present bool) float64 {
	switch v := value.(type) {
	case string:
		return ai.DateParse(v)
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 8.64e15 {
			return math.NaN()
		}
		return math.Trunc(v) + 0
	case bool:
		if v {
			return 1
		}
		return 0
	case nil:
		if present {
			return 0
		}
	}
	return math.NaN()
}

// msTime is the Date for a time value, or the zero time for an invalid date.
func msTime(ms float64) time.Time {
	if math.IsNaN(ms) {
		return time.Time{}
	}
	return time.UnixMilli(int64(ms))
}

// sessionMessageText is extractTextContent of Pi's buildSessionInfo: false where Pi's code throws, which drops the session from the listing.
func sessionMessageText(content any) (string, bool) {
	switch c := content.(type) {
	case string:
		return c, true
	case []any:
		var texts []string
		for _, block := range c {
			if block == nil {
				return "", false
			}
			obj, ok := block.(map[string]any)
			if !ok || obj["type"] != "text" {
				continue
			}
			text := ""
			if obj["text"] != nil {
				text = jsStringValue(obj["text"])
			}
			texts = append(texts, text)
		}
		return strings.Join(texts, " "), true
	}
	return "", false
}

// summarizeSessionFileContext ports buildSessionInfo of packages/coding-agent/src/core/session-manager.ts. Pi's code throws, and so omits the session, when a session_info name is neither absent nor a string, when a message entry has no message object, and when a user or assistant message's content is neither a string nor an array of objects.
func summarizeSessionFileContext(ctx context.Context, path string) (SessionInfo, error) {
	if err := ctx.Err(); err != nil {
		return SessionInfo{}, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return SessionInfo{}, err
	}
	return summarizeSessionFileWith(ctx, path, st, true)
}

// summaryPartial is the fold of the picker summary over a run of whole lines. Partials of consecutive ranges merge in file order into the serial fold; a noHeader range assumes the header was seen earlier.
type summaryPartial struct {
	header   map[string]any
	noHeader bool
	count    int
	name     string
	last     float64
	first    string
	texts    []string
}

func newSummaryPartial(noHeader bool) *summaryPartial {
	return &summaryPartial{noHeader: noHeader, last: math.NaN()}
}

// fork returns a copy whose appends never touch p's backing array.
func (p *summaryPartial) fork() *summaryPartial {
	c := *p
	c.texts = p.texts[:len(p.texts):len(p.texts)]
	return &c
}

// merge folds the later range q into p. nameSeen is whether q saw any session_info line.
func (p *summaryPartial) merge(q *summaryPartial, nameSeen bool) {
	p.count += q.count
	if nameSeen {
		p.name = q.name
	}
	if !math.IsNaN(q.last) {
		p.last = math.Max(nanToZero(p.last), q.last)
	}
	if p.first == "" {
		p.first = q.first
	}
	p.texts = append(p.texts, q.texts...)
}

func nanToZero(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return v
}

// feed applies one line (without terminator) in file order, as Pi's buildSessionInfo does per line. sawName is set when a session_info line sets the name.
func (p *summaryPartial) feed(line []byte, fast bool, sawName *bool) error {
	if len(bytes.TrimFunc(line, isJSWhitespace)) == 0 {
		return nil
	}
	hasHeader := p.header != nil || p.noHeader
	if fast && hasHeader && isToolResultMessageLine(line) {
		p.count++
		return nil
	}
	if hasHeader {
		if done, err := p.feedMessageLine(line); done {
			return err
		}
	}
	return p.feedDecoded(line, hasHeader, sawName)
}

// feedDecoded is feed by decoding the whole line into a generic value, as buildSessionInfo's JSON.parse does.
func (p *summaryPartial) feedDecoded(line []byte, hasHeader bool, sawName *bool) error {
	var value any
	if json.Unmarshal(line, &value) != nil {
		return nil
	}
	switch v := value.(type) {
	case nil:
		return nil
	case bool:
		if !v {
			return nil
		}
	case float64:
		if v == 0 {
			return nil
		}
	case string:
		if v == "" {
			return nil
		}
	}
	entry, isObject := value.(map[string]any)
	if !hasHeader {
		if !isObject || entry["type"] != "session" {
			return errSessionDropped
		}
		p.header = entry
		return nil
	}
	if !isObject {
		return nil
	}
	switch entry["type"] {
	case "session_info":
		switch name := entry["name"].(type) {
		case nil:
			p.name = ""
		case string:
			p.name = jsTrim(name)
		default:
			return errSessionDropped
		}
		*sawName = true
		return nil
	case "message":
	default:
		return nil
	}
	p.count++
	if entry["message"] == nil {
		return errSessionDropped
	}
	message, isObject := entry["message"].(map[string]any)
	if !isObject {
		return nil
	}
	role, hasRole := message["role"].(string)
	_, hasContent := message["content"]
	if !hasRole || !hasContent || (role != "user" && role != "assistant") {
		return nil
	}
	var activity float64
	if ts, ok := message["timestamp"].(float64); ok {
		activity = ts
	} else {
		activity = jsDateValue(entry["timestamp"], hasKey(entry, "timestamp"))
	}
	if !math.IsNaN(activity) {
		p.last = math.Max(nanToZero(p.last), activity)
	}
	text, ok := sessionMessageText(message["content"])
	if !ok {
		return errSessionDropped
	}
	if text == "" {
		return nil
	}
	p.texts = append(p.texts, text)
	if p.first == "" && role == "user" {
		p.first = text
	}
	return nil
}

// feedMessageLine is feed of a line after the header, when the line is an object whose type is a plain string, decoding only the members the
// summary reads: the entry's type, timestamp and message; the message's role, timestamp and content; each content block's type and, for a
// text block, its text. A tool call's arguments, thinking and every other member are only scanned. done is false for a line it leaves to
// the whole decode: one whose members it cannot read exactly as json.Unmarshal into a map does (an escaped or case-folded key), or whose type
// is not a plain string. A repeated member reads as its last, as the map holds it.
func (p *summaryPartial) feedMessageLine(line []byte) (done bool, err error) {
	start := skipJSONSpace(line, 0)
	// The whole decode skips a line that is no JSON text; those lines are left to it. Its decoder reads a number too large for a float64 as
	// JSON.parse does, as an infinity, so no valid line fails it.
	if start >= len(line) || line[start] != '{' || !json.Valid(line) {
		return false, nil
	}
	var typ, timestamp, message []byte
	hasTimestamp := false
	_, ok := scanObjectMembers(line, start, []string{"type", "timestamp", "message"}, func(name string, value []byte) {
		switch name {
		case "type":
			typ = value
		case "timestamp":
			timestamp, hasTimestamp = value, true
		case "message":
			message = value
		}
	})
	if !ok {
		return false, nil
	}
	typeText, plain := plainJSONString(typ)
	if !plain || typeText == "session_info" {
		return false, nil
	}
	if typeText != "message" {
		return true, nil
	}
	// Every case the walk leaves to the whole decode is found before the summary changes.
	if message == nil || string(message) == "null" {
		p.count++
		return true, errSessionDropped
	}
	if message[0] != '{' {
		p.count++
		return true, nil
	}
	var role, content, messageTimestamp []byte
	if _, ok := scanObjectMembers(message, 0, []string{"role", "content", "timestamp"}, func(name string, value []byte) {
		switch name {
		case "role":
			role = value
		case "content":
			content = value
		case "timestamp":
			messageTimestamp = value
		}
	}); !ok {
		return false, nil
	}
	roleText, roleIsString := plainJSONString(role)
	if role != nil && role[0] == '"' && !roleIsString {
		return false, nil
	}
	if !roleIsString || content == nil || (roleText != "user" && roleText != "assistant") {
		p.count++
		return true, nil
	}
	var activity float64
	if messageTimestamp != nil && (messageTimestamp[0] == '-' || messageTimestamp[0] >= '0' && messageTimestamp[0] <= '9') {
		if json.Unmarshal(messageTimestamp, &activity) != nil {
			return false, nil
		}
	} else {
		var entryTimestamp any
		if hasTimestamp && json.Unmarshal(timestamp, &entryTimestamp) != nil {
			return false, nil
		}
		activity = jsDateValue(entryTimestamp, hasTimestamp)
	}
	text, ok, known := summaryContentText(content)
	if !known {
		return false, nil
	}
	p.count++
	if !math.IsNaN(activity) {
		p.last = math.Max(nanToZero(p.last), activity)
	}
	if !ok {
		return true, errSessionDropped
	}
	if text == "" {
		return true, nil
	}
	p.texts = append(p.texts, text)
	if p.first == "" && roleText == "user" {
		p.first = text
	}
	return true, nil
}

// summaryContentText is sessionMessageText of the content member raw, decoding only the text of its text blocks. known is false when a
// block's members cannot be read exactly as the map decode reads them.
func summaryContentText(raw []byte) (text string, ok, known bool) {
	switch raw[0] {
	case '"':
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return "", false, false
		}
		return s, true, true
	case '[':
	default:
		return "", false, true
	}
	var texts []string
	for i := skipJSONSpace(raw, 1); i < len(raw) && raw[i] != ']'; {
		end := skipJSONValue(raw, i)
		if end < 0 {
			return "", false, false
		}
		block := raw[i:end]
		switch block[0] {
		case 'n':
			return "", false, true
		case '{':
			var typ, value []byte
			hasText := false
			if _, ok := scanObjectMembers(block, 0, []string{"type", "text"}, func(name string, v []byte) {
				if name == "type" {
					typ = v
				} else {
					value, hasText = v, true
				}
			}); !ok {
				return "", false, false
			}
			typeText, plain := plainJSONString(typ)
			if typ != nil && typ[0] == '"' && !plain {
				return "", false, false
			}
			if plain && typeText == "text" {
				piece := ""
				if hasText && string(value) != "null" {
					var decoded any
					if json.Unmarshal(value, &decoded) != nil {
						return "", false, false
					}
					piece = jsStringValue(decoded)
				}
				texts = append(texts, piece)
			}
		}
		i = skipJSONSpace(raw, end)
		if i < len(raw) && raw[i] == ',' {
			i = skipJSONSpace(raw, i+1)
		}
	}
	return strings.Join(texts, " "), true, true
}

// plainJSONString is the value of raw, a JSON string without escapes, and true; false for anything else.
func plainJSONString(raw []byte) (string, bool) {
	if len(raw) < 2 || raw[0] != '"' {
		return "", false
	}
	value, end, plain := scanPlainJSONString(raw, 0)
	if !plain || end != len(raw) {
		return "", false
	}
	return string(value), true
}

// scanRange feeds the lines of f in [start,end) into p. A lone carriage return also ends a line, as in Node's readline.
func (p *summaryPartial) scanRange(ctx context.Context, f *os.File, start, end int64, fast bool, sawName *bool) error {
	return forEachJSONLLineContext(ctx, io.NewSectionReader(f, start, end-start), func(line []byte) error {
		if bytes.IndexByte(line, '\r') < 0 {
			return p.feed(line, fast, sawName)
		}
		for part := range bytes.SplitSeq(line, []byte{'\r'}) {
			if err := p.feed(part, fast, sawName); err != nil {
				return err
			}
		}
		return nil
	})
}

// pig additive (D112): Pi reads a session's summary on one thread; a range below parallelScanMinBytes, or too short for two parallelScanChunkBytes chunks, is
// scanned serially, and the parallel scan merges its parts in file order, so both give the serial result.
var (
	parallelScanMinBytes   int64 = 8 << 20
	parallelScanChunkBytes int64 = 4 << 20
	parallelScanMaxWorkers int64 = 8
)

// lineBoundary returns the offset just after the first newline at or after off, or size.
func lineBoundary(f *os.File, off, size int64) (int64, error) {
	buf := make([]byte, 64<<10)
	for off < size {
		n, err := f.ReadAt(buf[:min(int64(len(buf)), size-off)], off)
		if i := bytes.IndexByte(buf[:n], '\n'); i >= 0 {
			return off + int64(i) + 1, nil
		}
		off += int64(n)
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		if n == 0 {
			break
		}
	}
	return size, nil
}

// scanParallel folds [start,end) of f into p, splitting a large range at newlines over workers and merging in file order, so the result equals the serial fold. start must begin a line.
func scanParallel(ctx context.Context, f *os.File, p *summaryPartial, start, end int64, fast bool, sawName *bool) error {
	n := min(parallelScanMaxWorkers, (end-start)/parallelScanChunkBytes)
	if !fast || end-start < parallelScanMinBytes || n < 2 {
		return p.scanRange(ctx, f, start, end, fast, sawName)
	}
	// pig additive (D174): the workers count and run at the runtime default width.
	defer procpolicy.Widen()()
	if n = min(n, int64(runtime.GOMAXPROCS(0))); n < 2 {
		return p.scanRange(ctx, f, start, end, fast, sawName)
	}
	bounds := []int64{start}
	for i := int64(1); i < n; i++ {
		b, err := lineBoundary(f, start+(end-start)*i/n, end)
		if err != nil {
			return fmt.Errorf("session: read: %w", err)
		}
		if b > bounds[len(bounds)-1] && b < end {
			bounds = append(bounds, b)
		}
	}
	bounds = append(bounds, end)
	if len(bounds) < 3 {
		return p.scanRange(ctx, f, start, end, fast, sawName)
	}
	parts := make([]*summaryPartial, len(bounds)-1)
	names := make([]bool, len(parts))
	errs := make([]error, len(parts))
	var wg sync.WaitGroup
	for i := range parts {
		parts[i] = newSummaryPartial(true)
		if i == 0 {
			parts[0] = p.fork()
		}
		wg.Go(func() { //portlint:allow gorecover parallel split of one sequential scan joined by wg; a panic in scanRange is the same crash the sequential path (scanRange above) raises
			errs[i] = parts[i].scanRange(ctx, f, bounds[i], bounds[i+1], fast, &names[i])
		})
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	if parts[0].header == nil && !parts[0].noHeader {
		// The header is not in the first range: only a serial scan decides where it is.
		return p.scanRange(ctx, f, start, end, fast, sawName)
	}
	*p = *parts[0]
	*sawName = *sawName || names[0]
	for i, q := range parts[1:] {
		p.merge(q, names[i+1])
		*sawName = *sawName || names[i+1]
	}
	return nil
}

// lastNewlineEnd returns the offset just after the last newline in [lo,size), or lo when there is none.
func lastNewlineEnd(f *os.File, lo, size int64) int64 {
	buf := make([]byte, 64<<10)
	for end := size; end > lo; {
		start := max(lo, end-int64(len(buf)))
		n, _ := f.ReadAt(buf[:end-start], start)
		if i := bytes.LastIndexByte(buf[:n], '\n'); i >= 0 {
			return start + int64(i) + 1
		}
		end = start
	}
	return lo
}

func summarizeSessionFileWith(ctx context.Context, path string, st os.FileInfo, fast bool) (SessionInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return SessionInfo{}, err
	}
	defer func() { _ = f.Close() }()
	interrupted := make(chan struct{})
	stopInterrupt := context.AfterFunc(ctx, func() { _ = f.Close(); close(interrupted) })
	defer func() {
		if !stopInterrupt() {
			<-interrupted
		}
	}()

	size := st.Size()
	var base *summaryPartial
	var baseOff int64
	if fast {
		base, baseOff = summaryCacheCatchUp(f, path, st)
	}
	if base == nil {
		base, baseOff = newSummaryPartial(false), 0
	}
	// Cacheable state ends at the last newline; a final unterminated fragment is folded into a fork.
	cut := baseOff
	if size > baseOff {
		cut = lastNewlineEnd(f, baseOff, size)
	}
	sawName := false
	if cut > baseOff {
		if err = scanParallel(ctx, f, base, baseOff, cut, fast, &sawName); err == nil && fast && ctx.Err() == nil {
			summaryCachePut(f, path, st, base, cut)
		}
	}
	p := base
	if err == nil && cut < size {
		p = base.fork()
		err = p.scanRange(ctx, f, cut, size, fast, &sawName)
	}
	if ctx.Err() != nil {
		return SessionInfo{}, ctx.Err()
	}
	if err != nil {
		return SessionInfo{}, err
	}
	return p.finish(path, st)
}

func (p *summaryPartial) finish(path string, st os.FileInfo) (SessionInfo, error) {
	if p.header == nil {
		return SessionInfo{}, errSessionDropped
	}
	header := p.header
	info := SessionInfo{Path: path, MessageCount: p.count, Name: p.name}
	info.ID, _ = header["id"].(string)
	info.CWD, _ = header["cwd"].(string)
	info.ParentSessionPath, _ = header["parentSession"].(string)
	info.Created = msTime(jsDateValue(header["timestamp"], hasKey(header, "timestamp")))
	headerTime := math.NaN()
	if timestamp, ok := header["timestamp"].(string); ok {
		headerTime = ai.DateParse(timestamp)
	}
	switch {
	case !math.IsNaN(p.last) && p.last > 0:
		info.Modified = msTime(jsDateValue(p.last, true))
	case !math.IsNaN(headerTime):
		info.Modified = msTime(headerTime)
	default:
		info.Modified = st.ModTime()
	}
	info.FirstMessage = p.first
	if info.FirstMessage == "" {
		info.FirstMessage = noMessagesText
	}
	info.AllMessagesText = strings.Join(p.texts, " ")
	return info, nil
}

func hasKey(m map[string]any, key string) bool {
	_, ok := m[key]
	return ok
}

// extractMessageText pulls the visible text out of a MessageEntry -
// concatenates TextContent blocks; ignores tool_use/tool_result.
func extractMessageText(me MessageEntry) string {
	var b strings.Builder
	for _, c := range me.Message.ContentBlocks() {
		if t, ok := contentText(c); ok {
			b.WriteString(t)
		}
	}
	return b.String()
}

// contentText returns the .Text field of a TextContent, or "" for
// other block types. Done by JSON round-trip to avoid reaching into
// ai types from here.
func contentText(c any) (string, bool) {
	raw, err := json.Marshal(c)
	if err != nil {
		return "", false
	}
	var probe struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return "", false
	}
	if probe.Type == "text" {
		return probe.Text, true
	}
	return "", false
}

// truncate cuts s to at most n bytes at a character boundary and appends an ellipsis.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// FindMostRecent returns the path to the most-recently-modified valid
// session jsonl in this manager's directory, or "" when none exist.
func (sm *SessionManager) FindMostRecent() string {
	return sm.findMostRecent(false)
}

// findMostRecent mirrors upstream `findMostRecentSession`: files in directory order, a stable sort by `mtimeMs` descending (so files with equal mtimes keep directory order), then the first whose bounded header read is a session header for the cwd.
func (sm *SessionManager) findMostRecent(filterCwd bool) string {
	files, err := nodefs.ReadDir(sm.sessionDir)
	if err != nil {
		return ""
	}
	type candidate struct {
		path    string
		mtimeMs float64
	}
	candidates := make([]candidate, 0, len(files))
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(sm.sessionDir, file.Name())
		stat, err := os.Stat(path)
		if err != nil {
			return ""
		}
		mod := stat.ModTime()
		// libuv: st_mtim.tv_sec * 1000 + st_mtim.tv_nsec / 1e6, in doubles.
		//portlint:allow fusedmuladd tv_sec*1000 is exact, so fusing it into the sum cannot change mtimeMs
		candidates = append(candidates, candidate{path, float64(mod.Unix())*1000 + float64(mod.Nanosecond())/1e6})
	}
	slices.SortStableFunc(candidates, func(a, b candidate) int { return cmp.Compare(b.mtimeMs, a.mtimeMs) })
	for _, c := range candidates {
		header := readSessionHeaderForDiscovery(c.path)
		if header != nil && (!filterCwd || sessionCwdMatches(header.CWD, sm.cwd)) {
			return c.path
		}
	}
	return ""
}

// FindMostRecentForContinue selects the session to resume for the
// `--continue` flag, mirroring upstream `SessionManager.continueRecent`
// (session-manager.ts). When this manager's directory is the cwd-encoded
// default it holds only this cwd's sessions, so the newest wins outright.
// When it is a custom `--session-dir` that may be shared across projects,
// the result is filtered to the newest session whose header cwd refers to
// this cwd (upstream's `filterCwd` branch) so `--continue` never resumes
// another project's session.
func (sm *SessionManager) FindMostRecentForContinue() string {
	return sm.findMostRecent(!sm.sessionDirIsCwdScoped())
}

// sessionDirIsCwdScoped reports whether this manager's session directory
// is the cwd-encoded default. The default dir only ever holds the current
// cwd's sessions, so `--continue` needs no cwd filter there; a custom dir
// might be shared across cwds and does. Mirrors upstream's
// `dir !== getDefaultSessionDirPath(cwd)` test inverted.
func (sm *SessionManager) sessionDirIsCwdScoped() bool {
	return absCleanDir(sm.sessionDir) == absCleanDir(defaultSessionDir(sm.cwd))
}

// sessionCwdMatches reports whether a session header cwd refers to the
// same directory as runtimeCwd. Mirrors upstream `sessionCwdMatches`
// (empty header cwd never matches). Both sides are made absolute and have
// symlinks resolved before comparison: Go's os.Getwd returns the logical
// path (e.g. /tmp/x) while a header written elsewhere may hold the
// canonical form (e.g. /private/tmp/x), and they denote the same dir.
func sessionCwdMatches(sessionCwd, runtimeCwd string) bool {
	if strings.TrimSpace(sessionCwd) == "" {
		return false
	}
	return canonicalDir(sessionCwd) == canonicalDir(runtimeCwd)
}

// canonicalDir resolves p to an absolute, symlink-free directory path for
// equality comparison. Falls back to a cleaned absolute path when the
// target does not exist on disk (EvalSymlinks fails), so comparison never
// crashes on a header pointing at a since-deleted directory.
func canonicalDir(p string) string {
	if p == "" {
		return ""
	}
	abs, err := nodepath.Resolve(p)
	if err != nil {
		abs = p
	}
	if resolved, err := evalCanonicalPath(abs); err == nil {
		return resolved
	}
	return filepath.Clean(abs)
}

// absCleanDir makes p absolute and cleaned without resolving symlinks.
// Used to compare two configured directory paths (the session dir vs the
// default) where both live under the agent dir and need no symlink walk.
func absCleanDir(p string) string {
	if p == "" {
		return ""
	}
	abs, err := nodepath.Resolve(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return abs
}

// FindByID finds an exact header ID without reading transcript bodies. A custom session directory is filtered by cwd; discovery errors are best-effort.
func (sm *SessionManager) FindByID(id string) string {
	files, err := nodefs.ReadDir(sm.sessionDir)
	if err != nil {
		return ""
	}
	filterCWD := !sm.sessionDirIsCwdScoped()
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(sm.sessionDir, file.Name())
		header := readSessionHeaderForDiscovery(path)
		if header == nil || header.ID != id {
			continue
		}
		if filterCWD && !sessionCwdMatches(header.CWD, sm.cwd) {
			continue
		}
		return path
	}
	return ""
}

const maxSessionHeaderScanBytes = 1024 * 1024

// readSessionHeaderForDiscovery extracts identity and cwd from the first truthy parsed entry within the upstream scan bound. Blank, malformed, and JSON-falsy entries do not terminate discovery.
func readSessionHeaderForDiscovery(path string) *SessionHeader {
	header, _ := ReadSessionHeader(path)
	return header
}

// SessionHeaderScanLimitError reports that bounded discovery could not reach a header.
type SessionHeaderScanLimitError struct{ Path string }

func (err *SessionHeaderScanLimitError) Error() string {
	return fmt.Sprintf("Session header exceeds %d-byte scan limit: %s", maxSessionHeaderScanBytes, err.Path)
}

// ReadSessionHeader scans at most Pi's header-discovery bound. Explicit opens may fall back to full loading on a scan-limit error.
func ReadSessionHeader(path string) (*SessionHeader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nodeerrno.FromPathError(err)
	}
	defer func() { _ = file.Close() }()
	reader := bufio.NewReader(io.LimitReader(file, maxSessionHeaderScanBytes+1))
	remaining := maxSessionHeaderScanBytes
	for {
		line, readErr := reader.ReadBytes('\n')
		remaining -= len(line)
		if remaining < 0 {
			return nil, &SessionHeaderScanLimitError{Path: path}
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			// readSync's error: "EISDIR: illegal operation on a directory, read".
			if pathErr, ok := errors.AsType[*fs.PathError](readErr); ok {
				return nil, nodeerrno.FromPathError(pathErr)
			}
			return nil, readErr
		}
		// upstream: coding-agent/src/core/session-manager.ts:parseSessionHeaderCandidate
		if jsonvalid.Valid(line) {
			var value any
			decoder := json.NewDecoder(bytes.NewReader(line))
			decoder.UseNumber()
			if err := decoder.Decode(&value); err != nil {
				return nil, err
			}
			switch value := value.(type) {
			case nil:
			case bool:
				if value {
					return nil, nil
				}
			case json.Number:
				number, _ := value.Float64()
				if number != 0 {
					return nil, nil
				}
			case string:
				if value != "" {
					return nil, nil
				}
			case map[string]any:
				id, hasID := value["id"].(string)
				if value["type"] != "session" || !hasID {
					return nil, nil
				}
				cwd, _ := value["cwd"].(string)
				timestamp, _ := value["timestamp"].(string)
				parent, _ := value["parentSession"].(string)
				version := 0
				if number, ok := value["version"].(json.Number); ok {
					if parsed, err := number.Int64(); err == nil {
						version = int(parsed)
					}
				}
				return &SessionHeader{Type: "session", ID: id, CWD: cwd, Timestamp: timestamp, ParentSession: parent, Version: version}, nil
			default:
				return nil, nil
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil, nil
		}
	}
}
