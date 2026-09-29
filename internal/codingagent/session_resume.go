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
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
)

// SessionInfo is the picker-display summary of a session JSONL.
// Returned by ListSessions; consumed by /resume picker (TUI overlay
// follow-up) and by --continue startup flag.
type SessionInfo struct {
	Path            string
	ID              string
	CWD             string
	Name            string // user-set name (via /name): empty when unset
	ParentSession   string // path of source jsonl when this session is a clone
	Created         time.Time
	Modified        time.Time
	MessageCount    int
	FirstMessage    string // first user message text: for picker preview
	AllMessagesText string // concatenated text: for fuzzy search
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

// compareSessionRecencyDesc orders listing metadata by activity and discovery metadata by mtime. Creation time and path break ties deterministically.
func compareSessionRecencyDesc(a, b SessionInfo) int {
	if c := cmp.Compare(b.Modified.UnixNano(), a.Modified.UnixNano()); c != 0 {
		return c
	}
	if c := cmp.Compare(b.Created.UnixNano(), a.Created.UnixNano()); c != 0 {
		return c
	}
	return cmp.Compare(a.Path, b.Path)
}

func summarizeSessionFiles(files []string) []SessionInfo {
	infos, _ := summarizeSessionFilesWithOptions(files, sessionListOptions(nil), 10, false)
	return infos
}

type sessionSummaryEntry struct {
	Type      string                `json:"type"`
	Name      string                `json:"name"`
	Timestamp string                `json:"timestamp"`
	Message   sessionSummaryMessage `json:"message"`
}

type sessionSummaryMessage struct {
	Role       string
	Text       string
	Timestamp  *float64
	HasContent bool
}

func (message *sessionSummaryMessage) UnmarshalJSON(data []byte) error {
	var metadata struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return err
	}
	message.Role = metadata.Role
	if message.Role != "user" && message.Role != "assistant" {
		return nil
	}
	var content struct {
		Content   json.RawMessage `json:"content"`
		Timestamp *float64        `json:"timestamp"`
	}
	if err := json.Unmarshal(data, &content); err != nil {
		return err
	}
	message.Timestamp = content.Timestamp
	message.HasContent = len(content.Content) != 0
	var text sessionSummaryContent
	if message.HasContent {
		if err := json.Unmarshal(content.Content, &text); err != nil {
			return err
		}
	}
	message.Text = string(text)
	return nil
}

type sessionSummaryContent string

var (
	canonicalMessagePrefix     = []byte(`{"type":"message",`)
	canonicalMessageRolePrefix = []byte(`"message":{"role":"`)
)

func summarizeSessionEntry(line []byte) (sessionSummaryEntry, bool) {
	trimmed := bytes.TrimSpace(line)
	isCanonicalMessage := bytes.HasPrefix(trimmed, canonicalMessagePrefix)
	if isCanonicalMessage {
		_, role, ok := bytes.Cut(trimmed, canonicalMessageRolePrefix)
		if ok {
			switch {
			case bytes.HasPrefix(role, []byte(`user"`)), bytes.HasPrefix(role, []byte(`assistant"`)):
				var entry sessionSummaryEntry
				if err := json.Unmarshal(trimmed, &entry); err != nil {
					return sessionSummaryEntry{}, false
				}
				return entry, true
			default:
				if json.Valid(trimmed) {
					return sessionSummaryEntry{Type: "message"}, true
				}
				return sessionSummaryEntry{}, false
			}
		}
	}
	var entry sessionSummaryEntry
	if err := json.Unmarshal(trimmed, &entry); err != nil {
		return sessionSummaryEntry{}, false
	}
	return entry, true
}

func (content *sessionSummaryContent) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil
	}
	if data[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		*content = sessionSummaryContent(text)
		return nil
	}
	if data[0] != '[' {
		return nil
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &blocks); err != nil {
		return err
	}
	texts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == "text" {
			texts = append(texts, block.Text)
		}
	}
	*content = sessionSummaryContent(strings.Join(texts, " "))
	return nil
}

// summarizeSessionFile streams picker metadata and searchable text without retaining entry objects.
func summarizeSessionFile(path string) (SessionInfo, error) {
	return summarizeSessionFileContext(context.Background(), path)
}

func summarizeSessionFileContext(ctx context.Context, path string) (SessionInfo, error) {
	if err := ctx.Err(); err != nil {
		return SessionInfo{}, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return SessionInfo{}, err
	}
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

	info := SessionInfo{Path: path, Modified: st.ModTime()}
	var firstUserMsg string
	var allMessages []string
	var lastActivityTime int64
	haveHeader := false
	err = forEachJSONLLineContext(ctx, f, func(line []byte) error {
		if len(line) == 0 {
			return nil
		}
		if !haveHeader {
			var header SessionHeader
			if err := json.Unmarshal(line, &header); err != nil {
				return fmt.Errorf("session: parse header: %w", err)
			}
			if header.Type != "session" {
				return fmt.Errorf("session: missing header in %s", path)
			}
			haveHeader = true
			info.ID = header.ID
			info.CWD = header.CWD
			info.ParentSession = header.ParentSession
			if t, err := time.Parse(time.RFC3339Nano, header.Timestamp); err == nil {
				info.Created = t
				info.Modified = t
			}
			return nil
		}
		entry, ok := summarizeSessionEntry(line)
		if !ok {
			return nil
		}
		switch entry.Type {
		case "message":
			info.MessageCount++
			if entry.Message.HasContent && (entry.Message.Role == "user" || entry.Message.Role == "assistant") {
				if entry.Message.Timestamp != nil {
					lastActivityTime = max(lastActivityTime, int64(*entry.Message.Timestamp))
				} else if timestamp, err := time.Parse(time.RFC3339Nano, entry.Timestamp); err == nil {
					lastActivityTime = max(lastActivityTime, timestamp.UnixMilli())
				}
			}
			if firstUserMsg == "" && entry.Message.Role == "user" {
				firstUserMsg = entry.Message.Text
			}
			if entry.Message.Text != "" {
				allMessages = append(allMessages, entry.Message.Text)
			}
		case "session_info":
			info.Name = jsTrim(entry.Name)
		}
		return nil
	})
	if ctx.Err() != nil {
		return SessionInfo{}, ctx.Err()
	}
	if err != nil {
		return SessionInfo{}, err
	}
	if !haveHeader {
		return SessionInfo{}, fmt.Errorf("session: empty file: %s", path)
	}
	if lastActivityTime > 0 {
		info.Modified = time.UnixMilli(lastActivityTime)
	}
	info.FirstMessage = truncate(firstUserMsg, 200)
	info.AllMessagesText = strings.Join(allMessages, " ")
	return info, nil
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// FindMostRecent returns the path to the most-recently-modified valid
// session jsonl in this manager's directory, or "" when none exist.
func (sm *SessionManager) FindMostRecent() string {
	return sm.findMostRecent(false)
}

// findMostRecent uses bounded header discovery and file mtime rather than the activity timestamps shown in the picker.
func (sm *SessionManager) findMostRecent(filterCwd bool) string {
	files, err := os.ReadDir(sm.sessionDir)
	if err != nil {
		return ""
	}
	candidates := make([]SessionInfo, 0, len(files))
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(sm.sessionDir, file.Name())
		stat, err := os.Stat(path)
		if err != nil {
			return ""
		}
		header := readSessionHeaderForDiscovery(path)
		if header == nil || filterCwd && !sessionCwdMatches(header.CWD, sm.cwd) {
			continue
		}
		created, _ := time.Parse(time.RFC3339Nano, header.Timestamp)
		candidates = append(candidates, SessionInfo{Path: path, Modified: stat.ModTime(), Created: created})
	}
	slices.SortFunc(candidates, compareSessionRecencyDesc)
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0].Path
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
	files, err := os.ReadDir(sm.sessionDir)
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
		return nil, err
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
			return nil, readErr
		}
		// upstream: coding-agent/src/core/session-manager.ts:parseSessionHeaderCandidate
		if json.Valid(line) {
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
