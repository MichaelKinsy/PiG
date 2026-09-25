package codingagent

// crash_log.go ports upstream core/crash-log.ts: crash persistence plus loaded
// extension attribution for JavaScript-style and Go panic stack frames.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
)

// upstream: coding-agent/src/core/crash-log.ts:MAX_AGE
const (
	maxCrashRecords = 5
	maxCrashAge     = 7 * 24 * time.Hour
)

// CrashRecord is one crashes.json entry. Mirrors upstream CrashRecord; Kind is
// "uncaught_exception" or "fatal_error".
type CrashRecord struct {
	Timestamp   string  `json:"timestamp"`
	Version     string  `json:"version"`
	Kind        string  `json:"kind"`
	Message     string  `json:"message"`
	Stack       *string `json:"stack"`
	SessionFile *string `json:"sessionFile"`
	CWD         string  `json:"cwd"`
	Notified    bool    `json:"notified,omitempty"`
}

// CrashLogPath returns the crash log location for an agent directory.
func CrashLogPath(agentDir string) string {
	return filepath.Join(agentDir, "crashes.json")
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
		var fields map[string]any
		if json.Unmarshal(item, &fields) != nil {
			continue
		}
		if _, ok := fields["timestamp"].(string); !ok {
			continue
		}
		if _, ok := fields["message"].(string); !ok {
			continue
		}
		var record CrashRecord
		if json.Unmarshal(item, &record) != nil {
			continue
		}
		records = append(records, record)
	}
	return records
}

func writeCrashLog(records []CrashRecord, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(records); err != nil {
		return err
	}
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
	for _, line := range lines {
		if !isStackFrame(line) {
			continue
		}
		frames = append(frames, decodeStackFrameURI(line))
	}
	normalizedStack := strings.ReplaceAll(strings.Join(frames, "\n"), `\`, "/")
	matches := make([]string, 0)
	seen := make(map[string]struct{})
	for _, extension := range extensions {
		resolvedPath := normalizeStackPath(extension.ResolvedPath)
		singleFilePackage := extension.SourceInfo.Origin == "package" &&
			!hasRemotePackageScheme(extension.SourceInfo.Source) && isJSSourceFile(extension.SourceInfo.Source)
		packageRoot := ""
		if extension.SourceInfo.Origin == "package" && !singleFilePackage {
			packageRoot = extension.SourceInfo.BaseDir
		}
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
		if unicode.IsSpace(rune(haystack[next])) {
			return true
		}
		offset = index + len(needle)
	}
}

func isStackFrame(line string) bool {
	trimmed := strings.TrimLeftFunc(line, unicode.IsSpace)
	if trimmed == line {
		return false
	}
	if after, ok := strings.CutPrefix(trimmed, "at"); ok {
		return after != "" && unicode.IsSpace(rune(after[0]))
	}
	// Go panic stacks put source locations on indented lines immediately below
	// function names rather than prefixing them with "at".
	colon := strings.LastIndexByte(trimmed, ':')
	if colon == -1 || colon == len(trimmed)-1 {
		return false
	}
	rest := trimmed[colon+1:]
	digits := 0
	for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
		digits++
	}
	return digits > 0 && (digits == len(rest) || unicode.IsSpace(rune(rest[digits])))
}

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
		at, err := time.Parse(time.RFC3339Nano, record.Timestamp)
		if err != nil || now.Sub(at) > maxCrashAge {
			continue
		}
		for j := range records {
			records[j].Notified = true
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
	when := crash.Timestamp
	if at, err := time.Parse(time.RFC3339Nano, crash.Timestamp); err == nil {
		when = at.Local().Format("1/2/2006, 3:04:05 PM")
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
