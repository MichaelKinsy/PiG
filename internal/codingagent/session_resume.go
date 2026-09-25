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
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
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
// directory, newest mtime first. Files that fail header parsing are
// silently skipped (matches upstream: corrupted sessions shouldn't
// crash the picker).
func (sm *SessionManager) ListSessions() ([]SessionInfo, error) {
	return listSessionsInDir(sm.sessionDir)
}

// ListCurrentSessions returns the selector's Current Folder scope. A custom
// session directory may contain sessions from several projects, so it filters
// by header cwd; the default encoded directory is already cwd-scoped.
func (sm *SessionManager) ListCurrentSessions() ([]SessionInfo, error) {
	infos, err := sm.ListSessions()
	if err != nil || sm.sessionDirIsCwdScoped() {
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
// <agentDir>/sessions/*/*.jsonl, newest modified first.
// Mirrors upstream session selector "All" scope.
func (sm *SessionManager) ListAllSessions() ([]SessionInfo, error) {
	if !sm.sessionDirIsCwdScoped() {
		return listSessionsInDir(sm.sessionDir)
	}
	root := filepath.Join(AgentDir(), "sessions")
	return listSessionsAcrossRoot(root)
}

func listSessionsAcrossRoot(root string) ([]SessionInfo, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	files := make([]string, 0)
	for _, e := range entries {
		subdir := filepath.Join(root, e.Name())
		info, err := os.Stat(subdir)
		if err != nil || !info.IsDir() {
			continue
		}
		subentries, err := os.ReadDir(subdir)
		if err != nil {
			continue
		}
		for _, se := range subentries {
			if se.IsDir() || !strings.HasSuffix(se.Name(), ".jsonl") {
				continue
			}
			files = append(files, filepath.Join(subdir, se.Name()))
		}
	}
	infos := summarizeSessionFiles(files)
	slices.SortFunc(infos, compareSessionRecencyDesc)
	return infos, nil
}

func listSessionsInDir(dir string) ([]SessionInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		files = append(files, filepath.Join(dir, e.Name()))
	}
	infos := summarizeSessionFiles(files)
	slices.SortFunc(infos, compareSessionRecencyDesc)
	return infos, nil
}

// compareSessionRecencyDesc is a total order over sessions, newest-first.
// The primary key is filesystem mtime, matching upstream findMostRecentSession
// (which sorts by mtime alone via a stable JS sort). When two sessions share a
// coarse mtime (sub-second writes on a low-resolution filesystem, as on some
// CI runners) the header creation timestamp (RFC3339Nano, parsed into Created)
// breaks the tie. Path is the final key so the comparator is a strict total
// order: slices.SortFunc is not stable, so a comparator that returned 0 for
// distinct sessions would leave their order undefined. Upstream leaves
// equal-mtime order unspecified, so these secondary keys only refine an
// otherwise undefined case and never reorder sessions with distinct mtimes.
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
	if len(files) == 0 {
		return nil
	}
	results := make([]SessionInfo, len(files))
	ok := make([]bool, len(files))
	sem := make(chan struct{}, maxConcurrentSessionInfoLoads)
	var wg sync.WaitGroup
	for i, path := range files {
		wg.Add(1)
		go func(idx int, file string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			info, err := summarizeSessionFile(file)
			if err != nil {
				return
			}
			results[idx] = info
			ok[idx] = true
		}(i, path)
	}
	wg.Wait()
	infos := make([]SessionInfo, 0, len(files))
	for i := range files {
		if ok[i] {
			infos = append(infos, results[i])
		}
	}
	return infos
}

type sessionSummaryEntry struct {
	Type    string                `json:"type"`
	Name    string                `json:"name"`
	Message sessionSummaryMessage `json:"message"`
}

type sessionSummaryMessage struct {
	Role string
	Text string
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
		Content sessionSummaryContent `json:"content"`
	}
	if err := json.Unmarshal(data, &content); err != nil {
		return err
	}
	message.Text = string(content.Content)
	return nil
}

type sessionSummaryContent string

var (
	canonicalMessagePrefix     = []byte(`{"type":"message",`)
	canonicalSessionInfoPrefix = []byte(`{"type":"session_info",`)
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
	if !isCanonicalMessage && !bytes.HasPrefix(trimmed, canonicalSessionInfoPrefix) && bytes.HasPrefix(trimmed, []byte(`{"type":`)) {
		if json.Valid(trimmed) {
			return sessionSummaryEntry{}, true
		}
		return sessionSummaryEntry{}, false
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

// summarizeSessionFile streams the JSONL file and retains only picker metadata and searchable user/assistant text.
func summarizeSessionFile(path string) (SessionInfo, error) {
	st, err := os.Stat(path)
	if err != nil {
		return SessionInfo{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return SessionInfo{}, err
	}
	defer func() { _ = f.Close() }()

	info := SessionInfo{Path: path, Modified: st.ModTime()}
	var firstUserMsg string
	var allMessages []string
	haveHeader := false
	err = forEachJSONLLine(f, func(line []byte) error {
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
			if firstUserMsg == "" && entry.Message.Role == "user" {
				firstUserMsg = entry.Message.Text
			}
			if entry.Message.Text != "" {
				allMessages = append(allMessages, entry.Message.Text)
			}
		case "session_info":
			info.Name = strings.TrimSpace(entry.Name)
		}
		return nil
	})
	if err != nil {
		return SessionInfo{}, err
	}
	if !haveHeader {
		return SessionInfo{}, fmt.Errorf("session: empty file: %s", path)
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
	infos, err := sm.ListSessions()
	if err != nil || len(infos) == 0 {
		return ""
	}
	return infos[0].Path
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
	infos, err := sm.ListCurrentSessions()
	if err != nil || len(infos) == 0 {
		return ""
	}
	return infos[0].Path
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
	abs := p
	if !filepath.IsAbs(abs) {
		if a, err := filepath.Abs(abs); err == nil {
			abs = a
		}
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
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
	if !filepath.IsAbs(p) {
		if a, err := filepath.Abs(p); err == nil {
			p = a
		}
	}
	return filepath.Clean(p)
}

// FindByID locates a session jsonl whose header `id` matches the given
// id (anywhere in the manager's session dir). Returns "" when no match.
// Used by `--session <id>`.
func (sm *SessionManager) FindByID(id string) string {
	infos, _ := sm.ListSessions()
	for _, info := range infos {
		if info.ID == id {
			return info.Path
		}
	}
	return ""
}
