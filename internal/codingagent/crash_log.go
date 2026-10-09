package codingagent

// crash_log.go ports upstream core/crash-log.ts: crash persistence plus loaded
// extension attribution for JavaScript-style and Go panic stack frames.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// upstream: coding-agent/src/core/crash-log.ts:MAX_AGE
const (
	maxCrashRecords = 5
	maxCrashAge     = 7 * 24 * time.Hour
)

// CrashRecord is one crashes.json entry. Mirrors upstream CrashRecord; Kind is
// "uncaught_exception" or "fatal_error". A record read from the log keeps the
// JSON object it came from, so a rewrite and a bug report carry its other
// members and the types it was written with, as Pi's untyped objects do.
type CrashRecord struct {
	Timestamp   string  `json:"timestamp"`
	Version     string  `json:"version"`
	Kind        string  `json:"kind"`
	Message     string  `json:"message"`
	Stack       *string `json:"stack"`
	SessionFile *string `json:"sessionFile"`
	CWD         string  `json:"cwd"`
	Notified    bool    `json:"notified,omitempty"`

	// members is the record as read: JSON.stringify(JSON.parse(text)) of the log entry, in member order. Nil for a record this process created.
	members *orderedjson.Object
}

// MarshalJSON writes the record as the log holds it: the members it was read with, or the fields of a new record.
func (r CrashRecord) MarshalJSON() ([]byte, error) {
	if r.members != nil {
		return r.members.MarshalJSON()
	}
	type plain CrashRecord
	return json.Marshal(plain(r))
}

// withoutNotified is `({ notified: _notified, ...record }) => record` (bug-report.ts:221).
func (r CrashRecord) withoutNotified() CrashRecord {
	r.Notified = false
	if r.members != nil {
		r.members = r.members.Clone()
		r.members.Delete("notified")
	}
	return r
}

// markNotified is `{ ...record, notified: true }`: the member keeps its place when the record has it and is appended otherwise.
func (r CrashRecord) markNotified() CrashRecord {
	r.Notified = true
	if r.members != nil {
		r.members = r.members.Clone()
		r.members.Set("notified", json.RawMessage("true"))
	}
	return r
}

// crashNotifiedTruthy is JavaScript's ToBoolean of a parsed JSON value, numbers included whatever their spelling (`0.0`, `-0`, `0e5`).
func crashNotifiedTruthy(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case float64:
		return v != 0 && !math.IsNaN(v)
	case string:
		return v != ""
	}
	return true
}

// CrashLogPath returns the crash log location for an agent directory.
func CrashLogPath(agentDir string) string {
	return filepath.Join(agentDir, "crashes.json")
}

// readCrashMember decodes one member of a record into target when it has the target's type. A member of another type leaves the field empty: Pi's record keeps it untouched, so only the typed view loses it.
func readCrashMember(members *orderedjson.Object, name string, target any) {
	if raw, ok := members.Get(name); ok {
		_ = json.Unmarshal(raw, target)
	}
}

// ReadCrashLog returns the valid records, or none when the file is missing
// or unreadable. A record needs a string timestamp and message. Mirrors
// upstream readCrashLog.
func ReadCrashLog(path string) []CrashRecord {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var raw []json.RawMessage
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}
	records := make([]CrashRecord, 0, len(raw))
	// upstream: coding-agent/src/core/crash-log.ts:readCrashLog
	for _, item := range raw {
		canonical, err := jsonstringify.Canonicalize(item)
		if err != nil || len(canonical) == 0 || canonical[0] != '{' {
			continue
		}
		members, err := orderedjson.Parse(canonical)
		if err != nil {
			continue
		}
		var record CrashRecord
		var timestampOK, messageOK bool
		if v, ok := members.Get("timestamp"); ok && json.Unmarshal(v, &record.Timestamp) == nil && len(v) > 0 && v[0] == '"' {
			timestampOK = true
		}
		if v, ok := members.Get("message"); ok && json.Unmarshal(v, &record.Message) == nil && len(v) > 0 && v[0] == '"' {
			messageOK = true
		}
		if !timestampOK || !messageOK {
			continue
		}
		readCrashMember(members, "version", &record.Version)
		readCrashMember(members, "kind", &record.Kind)
		readCrashMember(members, "stack", &record.Stack)
		readCrashMember(members, "sessionFile", &record.SessionFile)
		readCrashMember(members, "cwd", &record.CWD)
		if v, ok := members.Get("notified"); ok {
			record.Notified = crashNotifiedTruthy(v)
		}
		record.members = members
		records = append(records, record)
	}
	return records
}

// writeCrashLog is `writeFileSync(path, JSON.stringify(records, null, 2) + "\n")`.
func writeCrashLog(records []CrashRecord, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	compact := []byte{'['}
	for i, record := range records {
		encoded, err := record.MarshalJSON()
		if err != nil {
			return err
		}
		if record.members == nil {
			// A new record's strings are written as JSON.stringify writes them.
			if encoded, err = jsonstringify.Canonicalize(encoded); err != nil {
				return err
			}
		}
		if i > 0 {
			compact = append(compact, ',')
		}
		compact = append(compact, encoded...)
	}
	compact = append(compact, ']')
	var buffer bytes.Buffer
	if err := json.Indent(&buffer, compact, "", "  "); err != nil {
		return err
	}
	buffer.WriteByte('\n')
	return os.WriteFile(path, buffer.Bytes(), 0o644)
}

// CrashInput describes a crash to record.
type CrashInput struct {
	Kind        string
	Message     string
	Stack       string
	SessionFile string
	CWD         string
	Version     string
}

// ExtensionStackMetadata is the loaded extension provenance used to attribute
// stack frames after a crash.
type ExtensionStackMetadata struct {
	Path         string
	ResolvedPath string
	SourceInfo   ResourceSourceInfo
}

// FindExtensionStackMatches returns loaded extensions whose source files occur
// in stack frames.
func FindExtensionStackMatches(stack string, extensions []ExtensionStackMetadata) []string {
	if stack == "" {
		return nil
	}
	lines := strings.Split(stack, "\n")
	if len(lines) > 0 {
		lines = lines[1:]
	}
	frames := make([]string, 0, len(lines))
	goStack := goroutineHeader.MatchString(stack)
	for _, line := range lines {
		if !isStackFrame(line, goStack) {
			continue
		}
		frames = append(frames, decodeStackFrameURI(line))
	}
	normalizedStack := strings.ReplaceAll(strings.Join(frames, "\n"), `\`, "/")
	matches := make([]string, 0)
	seen := make(map[string]struct{})
	for _, extension := range extensions {
		resolvedPath := normalizeStackPath(extension.ResolvedPath)
		packageRoot := ExtensionPackageRoot(extension.SourceInfo.Origin, extension.SourceInfo.Source, extension.SourceInfo.BaseDir)
		slashIndex := strings.LastIndexByte(resolvedPath, '/')
		directoryEntry := isDirectoryEntry(resolvedPath)
		var matched bool
		switch {
		case packageRoot != "":
			matched = stackContainsPath(normalizedStack, packageRoot, true)
		case directoryEntry && slashIndex != -1:
			matched = stackContainsPath(normalizedStack, resolvedPath[:slashIndex], true)
		default:
			matched = stackContainsPath(normalizedStack, resolvedPath, false)
		}
		if !matched {
			continue
		}
		label := extension.Path
		if extension.SourceInfo.Origin == "package" && extension.SourceInfo.Source != "" {
			label = extension.SourceInfo.Source
		}
		if _, duplicate := seen[label]; duplicate {
			continue
		}
		seen[label] = struct{}{}
		matches = append(matches, label)
	}
	return matches
}

func normalizeStackPath(value string) string {
	return strings.TrimRight(strings.ReplaceAll(value, `\`, "/"), "/")
}

func stackContainsPath(stack, targetPath string, includeDescendants bool) bool {
	target := normalizeStackPath(targetPath)
	if target == "" || strings.HasPrefix(target, "<") {
		return false
	}
	haystack, needle := stack, target
	if isWindowsDrivePath(target) {
		haystack, needle = strings.ToLower(stack), strings.ToLower(target)
	}
	if includeDescendants {
		return strings.Contains(haystack, needle+"/")
	}
	for offset := 0; ; {
		index := strings.Index(haystack[offset:], needle)
		if index == -1 {
			return false
		}
		index += offset
		next := index + len(needle)
		if next == len(haystack) || haystack[next] == ':' || haystack[next] == ')' {
			return true
		}
		if r, _ := utf8.DecodeRuneInString(haystack[next:]); widthx.IsJSSpace(r) {
			return true
		}
		offset = index + len(needle)
	}
}

// pig additive (D100): a Go goroutine trace is read for crashes of the PiG process itself.
// isStackFrame is `/^\s+at\s/u.test(line)` (JavaScript's \s) and, in a Go goroutine trace (goStack), which puts the source location on a tab-indented line below the function name, also a line of the form `\t<file>:<line>[ +0x<offset>]`.
// A Go trace has no `at` frames, so without the second form a stack recorded for a Go crash would never attribute an extension. A JavaScript stack is matched exactly as Pi matches it.
func isStackFrame(line string, goStack bool) bool {
	trimmed := strings.TrimLeftFunc(line, widthx.IsJSSpace)
	if trimmed == line {
		return false
	}
	if after, ok := strings.CutPrefix(trimmed, "at"); ok {
		r, _ := utf8.DecodeRuneInString(after)
		return after != "" && widthx.IsJSSpace(r)
	}
	return goStack && goPanicFrame.MatchString(line)
}

var (
	goPanicFrame = lazyregexp.New(`^\t[^\t]+:[0-9]+(?: \+0x[0-9a-f]+)?$`)
	// goroutineHeader is the header line of each goroutine in a Go panic or runtime/debug.Stack trace.
	goroutineHeader = lazyregexp.New(`(?m)^goroutine [0-9]+ \[[^\]\n]*\]:$`)
)

func decodeStackFrameURI(line string) string {
	// decodeURI preserves escapes for URI delimiters while decoding path spaces
	// and UTF-8. Protect those delimiters before using Go's path unescaper.
	var protected strings.Builder
	protected.Grow(len(line))
	for i := 0; i < len(line); i++ {
		if line[i] == '%' && i+2 < len(line) {
			if value, ok := hexByte(line[i+1], line[i+2]); ok && strings.ContainsRune(";/?:@&=+$,#", rune(value)) {
				protected.WriteString("%25")
				protected.WriteByte(line[i+1])
				protected.WriteByte(line[i+2])
				i += 2
				continue
			}
		}
		protected.WriteByte(line[i])
	}
	decoded, err := url.PathUnescape(protected.String())
	if err != nil {
		return line
	}
	return decoded
}

func hexByte(high, low byte) (byte, bool) {
	hex := func(value byte) (byte, bool) {
		switch {
		case value >= '0' && value <= '9':
			return value - '0', true
		case value >= 'a' && value <= 'f':
			return value - 'a' + 10, true
		case value >= 'A' && value <= 'F':
			return value - 'A' + 10, true
		default:
			return 0, false
		}
	}
	hi, ok := hex(high)
	if !ok {
		return 0, false
	}
	lo, ok := hex(low)
	return hi<<4 | lo, ok
}

func isWindowsDrivePath(path string) bool {
	return len(path) >= 3 && ((path[0] >= 'a' && path[0] <= 'z') || (path[0] >= 'A' && path[0] <= 'Z')) && path[1] == ':' && path[2] == '/'
}

// ExtensionPackageRoot is the directory of the Package that supplied an extension, or "" for a top-level extension and for a local source that is a single script file (crash-log.ts singleFilePackage, which mirrors package-manager.ts leaving packageRoot unset for a file).
func ExtensionPackageRoot(origin, source, baseDir string) string {
	if origin != "package" || (!hasRemotePackageScheme(source) && isJSSourceFile(source)) {
		return ""
	}
	return baseDir
}

func hasRemotePackageScheme(source string) bool {
	return strings.HasPrefix(source, "npm:") || strings.HasPrefix(source, "git:") || strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") || strings.HasPrefix(source, "ssh://")
}

func isJSSourceFile(path string) bool {
	return strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".cjs") || strings.HasSuffix(path, ".mjs") || strings.HasSuffix(path, ".cts") || strings.HasSuffix(path, ".mts")
}

func isDirectoryEntry(path string) bool {
	for _, suffix := range []string{"/index.js", "/index.ts", "/index.cjs", "/index.mjs", "/index.cts", "/index.mts"} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

// FormatCrashExtensionHint formats the warning shown for matching extensions.
func FormatCrashExtensionHint(extensionMatches []string) string {
	matches := make([]string, 0, len(extensionMatches))
	for _, match := range extensionMatches {
		if match != "" {
			matches = append(matches, match)
		}
	}
	if len(matches) == 0 {
		return ""
	}
	quoted := make([]string, len(matches))
	for i, match := range matches {
		quoted[i] = "`" + match + "`"
	}
	var labels string
	switch len(quoted) {
	case 1:
		labels = quoted[0]
	case 2:
		labels = strings.Join(quoted, " and ")
	default:
		labels = strings.Join(quoted[:len(quoted)-1], ", ") + ", and " + quoted[len(quoted)-1]
	}
	noun, pronoun := "extensions", "them"
	if len(matches) == 1 {
		noun, pronoun = "extension", "it"
	}
	return fmt.Sprintf("A stack frame came from loaded %s %s, which may be involved. Try disabling %s with `%s config`, or run `%s -ne` to confirm.", noun, labels, pronoun, AppName, AppName)
}

// RecordCrash appends a crash, keeping the newest five. It is best effort for
// callers that are already crashing: it reports ok=false when nothing was
// written. Mirrors upstream recordCrash.
func RecordCrash(crash CrashInput, path string, now time.Time) (CrashRecord, bool) {
	record := CrashRecord{
		Timestamp: isoTimestamp(now),
		Version:   crash.Version,
		Kind:      crash.Kind,
		Message:   crash.Message,
		CWD:       crash.CWD,
	}
	if crash.Stack != "" {
		record.Stack = &crash.Stack
	}
	if crash.SessionFile != "" {
		record.SessionFile = &crash.SessionFile
	}
	records := append(ReadCrashLog(path), record)
	if len(records) > maxCrashRecords {
		records = records[len(records)-maxCrashRecords:]
	}
	if writeCrashLog(records, path) != nil {
		return CrashRecord{}, false
	}
	return record, true
}

// TakeUnnotifiedCrash returns the newest crash from the last seven days that
// has not been announced, and marks every record announced. Mirrors upstream
// takeUnnotifiedCrash.
func TakeUnnotifiedCrash(path string, now time.Time) (CrashRecord, bool) {
	records := ReadCrashLog(path)
	for _, record := range slices.Backward(records) {
		if record.Notified {
			continue
		}
		// now - Date.parse(record.timestamp) <= MAX_AGE: a time Date.parse does not read is NaN and never recent.
		if age := float64(now.UnixMilli()) - ai.DateParse(record.Timestamp); !(age <= float64(maxCrashAge.Milliseconds())) {
			continue
		}
		for j := range records {
			if !records[j].Notified {
				records[j] = records[j].markNotified()
			}
		}
		// Showing the notice again is harmless, so a write failure is ignored.
		_ = writeCrashLog(records, path)
		return record, true
	}
	return CrashRecord{}, false
}

// ClearCrashLog removes the crash log; a failure leaves the records for the
// next report. Mirrors upstream clearCrashLog.
func ClearCrashLog(path string) {
	if path != "" {
		_ = os.Remove(path)
	}
}

// crashNotice is the startup warning for an unannounced crash. Upstream
// formats the time with toLocaleString(); this uses its en-US form.
func crashNotice(crash CrashRecord) string {
	// new Date(crash.timestamp).toLocaleString()
	when := "Invalid Date"
	if ms := ai.DateParse(crash.Timestamp); !math.IsNaN(ms) && math.Abs(ms) <= 8.64e15 {
		when = time.UnixMilli(int64(ms)).Local().Format("1/2/2006, 3:04:05 PM")
	}
	return fmt.Sprintf("%s crashed on %s (%s). Run /bug to report it; the crash details are attached automatically.", AppName, when, crash.Message)
}

// crashReportInstructions mirrors upstream InteractiveMode.crashReportInstructions.
func crashReportInstructions(sessionFile string) string {
	resume := "start " + AppName + " and"
	if sessionFile != "" {
		resume = "run `" + AppName + " -r` to resume the session, then"
	}
	return "To report this crash: " + resume + " run /bug. The crash details are attached automatically."
}
