package codingagent

// Ports packages/coding-agent/src/core/session-manager.ts

// The host side of an extension's ctx.sessionManager reads. Upstream hands
// extensions its SessionManager (packages/coding-agent/src/core/
// session-manager.ts), typed as ReadonlySessionManager. The Node runtime
// answers the reads from its replicated session log with Pi's own projection
// code; the Go, Rust and Python SDKs ask the host, which answers here with the
// same algorithms over the same raw entries.

import (
	"fmt"
	"maps"
	"math"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// ExtensionSessionView names the session an extension reads and the facts
// the session file does not hold: the session manager's cwd, its session
// directory, and whether it persists (upstream SessionManager cwd,
// sessionDir and persist).
type ExtensionSessionView struct {
	Session    *Session
	CWD        string
	SessionDir string
}

// persisted mirrors upstream SessionManager.isPersisted: a --no-session
// session has no file.
func (v ExtensionSessionView) persisted() bool {
	return v.Session != nil && v.Session.Path() != ""
}

// sessionDir mirrors upstream SessionManager.getSessionDir: the configured or
// default directory of a persisted session, empty for an in-memory one.
func (v ExtensionSessionView) sessionDir() string {
	if !v.persisted() {
		return ""
	}
	if v.SessionDir != "" {
		return v.SessionDir
	}
	return defaultSessionDir(v.CWD)
}

// ExtensionSessionInfo is the header part of ReadonlySessionManager the
// Node runtime replicates with each state push.
func ExtensionSessionInfo(view ExtensionSessionView) map[string]any {
	info := map[string]any{
		"cwd":                   view.CWD,
		"sessionDir":            view.sessionDir(),
		"persisted":             view.persisted(),
		"usesDefaultSessionDir": view.sessionDir() == defaultSessionDir(view.CWD),
		"header":                nil,
	}
	if view.Session != nil {
		info["header"] = view.Session.Header()
	}
	return info
}

// sessionReadEntry is the part of a raw session entry the reads inspect.
type sessionReadEntry struct {
	raw       json.RawMessage
	Type      string  `json:"type"`
	ID        string  `json:"id"`
	ParentID  *string `json:"parentId"`
	Timestamp string  `json:"timestamp"`
	TargetID  string  `json:"targetId"`
	Label     *string `json:"label"`
}

type sessionReadLog struct {
	entries []*sessionReadEntry
	byID    map[string]*sessionReadEntry
	leafID  *string
}

func newSessionReadLog(sess *Session) (sessionReadLog, error) {
	log := sessionReadLog{byID: map[string]*sessionReadEntry{}}
	if sess == nil {
		return log, nil
	}
	for _, entry := range sess.Entries() {
		raw := entry.Raw()
		parsed := &sessionReadEntry{}
		if err := json.Unmarshal(raw, parsed); err != nil {
			return log, fmt.Errorf("decode retained session entry: %w", err)
		}
		parsed.raw = json.RawMessage(raw)
		log.entries = append(log.entries, parsed)
		log.byID[parsed.ID] = parsed
	}
	log.leafID = sess.LeafID()
	return log, nil
}

// labels mirrors upstream's labelsById and labelTimestampsById: the last
// label entry for a target wins, and an empty label clears it.
func (l sessionReadLog) labels() (map[string]string, map[string]string) {
	labels, timestamps := map[string]string{}, map[string]string{}
	for _, entry := range l.entries {
		if entry.Type != "label" {
			continue
		}
		if entry.Label != nil && *entry.Label != "" {
			labels[entry.TargetID] = *entry.Label
			timestamps[entry.TargetID] = entry.Timestamp
		} else {
			delete(labels, entry.TargetID)
			delete(timestamps, entry.TargetID)
		}
	}
	return labels, timestamps
}

// branch mirrors upstream SessionManager.getBranch(fromId).
func (l sessionReadLog) branch(fromID *string) []*sessionReadEntry {
	start := l.leafID
	if fromID != nil {
		start = fromID
	}
	var path []*sessionReadEntry
	if start == nil {
		return path
	}
	for current := l.byID[*start]; current != nil; {
		path = append(path, current)
		if current.ParentID == nil || *current.ParentID == "" {
			break
		}
		current = l.byID[*current.ParentID]
	}
	slices.Reverse(path)
	return path
}

// sessionPath mirrors upstream buildSessionPath for the current leaf: no
// leaf means an empty path.
func (l sessionReadLog) sessionPath() []*sessionReadEntry {
	if l.leafID == nil {
		return nil
	}
	leaf := l.byID[*l.leafID]
	if leaf == nil && len(l.entries) > 0 {
		leaf = l.entries[len(l.entries)-1]
	}
	if leaf == nil {
		return nil
	}
	return l.branch(&leaf.ID)
}

// contextEntries mirrors upstream buildContextEntries.
func (l sessionReadLog) contextEntries() []*sessionReadEntry {
	path := l.sessionPath()
	compactionIndex := -1
	for index, entry := range path {
		if entry.Type == "compaction" {
			compactionIndex = index
		}
	}
	if compactionIndex < 0 {
		return path
	}
	compaction := path[compactionIndex]
	var fields struct {
		FirstKeptEntryID string `json:"firstKeptEntryId"`
	}
	_ = json.Unmarshal(compaction.raw, &fields)
	out := []*sessionReadEntry{compaction}
	foundFirstKept := false
	for _, entry := range path[:compactionIndex] {
		if entry.ID == fields.FirstKeptEntryID {
			foundFirstKept = true
		}
		if foundFirstKept && !isRawSystemMessageEntry(entry) {
			out = append(out, entry)
		}
	}
	return append(out, path[compactionIndex+1:]...)
}

func isRawSystemMessageEntry(entry *sessionReadEntry) bool {
	if entry.Type != "message" {
		return false
	}
	var fields struct {
		Message struct {
			Role string `json:"role"`
		} `json:"message"`
	}
	_ = json.Unmarshal(entry.raw, &fields)
	return fields.Message.Role == "system"
}

// jsTimestamp is upstream's new Date(timestamp).getTime(): milliseconds, or
// NaN (JSON null) for a timestamp Date cannot parse.
func jsTimestamp(timestamp string) any {
	parsed, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return nil
	}
	return float64(parsed.UnixMilli())
}

func decodeObject(raw json.RawMessage) map[string]any {
	var object map[string]any
	_ = json.Unmarshal(raw, &object)
	if object == nil {
		object = map[string]any{}
	}
	return object
}

// contextMessages mirrors upstream sessionEntryToContextMessages.
func contextMessages(entry *sessionReadEntry) []any {
	object := decodeObject(entry.raw)
	switch entry.Type {
	case "message":
		message, _ := object["message"].(map[string]any)
		if message == nil {
			return []any{nil}
		}
		role, _ := message["role"].(string)
		if content, present := message["content"]; !present || content == nil {
			switch role {
			case "system":
				message["content"] = ""
			case "user", "assistant", "toolResult":
				message["content"] = []any{}
			}
		}
		return []any{message}
	case "custom_message":
		message := map[string]any{"role": "custom", "customType": object["customType"], "timestamp": jsTimestamp(entry.Timestamp)}
		if content, ok := object["content"]; ok && content != nil {
			message["content"] = content
		} else {
			message["content"] = []any{}
		}
		for _, key := range []string{"display", "details"} {
			if value, ok := object[key]; ok {
				message[key] = value
			}
		}
		return []any{message}
	case "branch_summary":
		if summary, _ := object["summary"].(string); summary != "" {
			message := map[string]any{"role": "branchSummary", "summary": summary, "timestamp": jsTimestamp(entry.Timestamp)}
			if fromID, ok := object["fromId"]; ok {
				message["fromId"] = fromID
			}
			return []any{message}
		}
	case "compaction":
		summary := map[string]any{"role": "compactionSummary", "summary": object["summary"], "timestamp": jsTimestamp(entry.Timestamp)}
		if tokensBefore, ok := object["tokensBefore"]; ok {
			summary["tokensBefore"] = tokensBefore
		}
		if system, ok := object["systemMessage"]; ok && system != nil {
			return []any{system, summary}
		}
		return []any{summary}
	}
	return []any{}
}

// projectRawContextEntry mirrors upstream projectContextEntry: a context_edit
// replaces the content of the messages its target contributes, or omits them.
func projectRawContextEntry(entry *sessionReadEntry, edit *sessionReadEntry) []any {
	messages := contextMessages(entry)
	if edit == nil {
		return messages
	}
	var fields struct {
		Replacement *struct {
			Content json.RawMessage `json:"content"`
		} `json:"replacement"`
	}
	_ = json.Unmarshal(edit.raw, &fields)
	if fields.Replacement == nil {
		return []any{}
	}
	var content any
	_ = json.Unmarshal(fields.Replacement.Content, &content)
	out := make([]any, len(messages))
	for index, value := range messages {
		message, ok := value.(map[string]any)
		role, _ := message["role"].(string)
		if !ok || (role != "user" && role != "assistant" && role != "toolResult" && role != "custom") {
			out[index] = value
			continue
		}
		replaced := make(map[string]any, len(message))
		maps.Copy(replaced, message)
		if text, isText := content.(string); isText && (role == "assistant" || role == "toolResult") {
			replaced["content"] = []any{map[string]any{"type": "text", "text": text}}
		} else {
			replaced["content"] = content
		}
		out[index] = replaced
	}
	return out
}

// projection mirrors upstream buildSessionProjection.
func (l sessionReadLog) projection() map[string]any {
	path := l.sessionPath()
	thinkingLevel, model := "off", any(nil)
	for _, entry := range path {
		object := decodeObject(entry.raw)
		switch entry.Type {
		case "thinking_level_change":
			thinkingLevel, _ = object["thinkingLevel"].(string)
		case "model_change":
			model = map[string]any{"provider": object["provider"], "modelId": object["modelId"]}
		case "message":
			if message, _ := object["message"].(map[string]any); message != nil && message["role"] == "assistant" {
				model = map[string]any{"provider": message["provider"], "modelId": message["model"]}
			}
		}
	}
	contextEntries := l.contextEntries()
	edits := map[string]*sessionReadEntry{}
	for _, entry := range contextEntries {
		if entry.Type == "context_edit" {
			edits[entry.TargetID] = entry
		}
	}
	projected := make([]any, 0, len(contextEntries))
	messages := []any{}
	for index, entry := range contextEntries {
		entryMessages := []any{}
		if entry.Type != "compaction" || index == 0 {
			entryMessages = projectRawContextEntry(entry, edits[entry.ID])
		}
		projected = append(projected, map[string]any{"sourceEntry": entry.raw, "messages": entryMessages})
		messages = append(messages, entryMessages...)
	}
	return map[string]any{"entries": projected, "messages": messages, "thinkingLevel": thinkingLevel, "model": model}
}

// tree mirrors upstream SessionManager.getTree.
func (l sessionReadLog) tree() []any {
	type node struct {
		entry    *sessionReadEntry
		children []*node
	}
	labels, timestamps := l.labels()
	nodes := make(map[string]*node, len(l.entries))
	for _, entry := range l.entries {
		nodes[entry.ID] = &node{entry: entry}
	}
	var roots []*node
	for _, entry := range l.entries {
		current := nodes[entry.ID]
		if entry.ParentID == nil || *entry.ParentID == entry.ID {
			roots = append(roots, current)
			continue
		}
		if parent := nodes[*entry.ParentID]; parent != nil {
			parent.children = append(parent.children, current)
		} else {
			roots = append(roots, current)
		}
	}
	timeOf := func(n *node) float64 {
		if value, ok := jsTimestamp(n.entry.Timestamp).(float64); ok {
			return value
		}
		return math.NaN()
	}
	var encode func(n *node) map[string]any
	encode = func(n *node) map[string]any {
		// JavaScript's sort compares with NaN as equal, keeping order.
		slices.SortStableFunc(n.children, func(a, b *node) int {
			left, right := timeOf(a), timeOf(b)
			switch {
			case left < right:
				return -1
			case left > right:
				return 1
			default:
				return 0
			}
		})
		children := make([]any, 0, len(n.children))
		for _, child := range n.children {
			children = append(children, encode(child))
		}
		out := map[string]any{"entry": n.entry.raw, "children": children}
		if label, ok := labels[n.entry.ID]; ok {
			out["label"] = label
			out["labelTimestamp"] = timestamps[n.entry.ID]
		}
		return out
	}
	out := make([]any, 0, len(roots))
	for _, root := range roots {
		out = append(out, encode(root))
	}
	return out
}

func rawEntries(entries []*sessionReadEntry) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.raw)
	}
	return out
}

// ExtensionSessionRead answers one ReadonlySessionManager read by its
// upstream method name. A result of nil is upstream's undefined or null.
func ExtensionSessionRead(view ExtensionSessionView, method string, args json.RawMessage) (any, error) {
	var params struct {
		ID       *string `json:"id"`
		FromID   *string `json:"fromId"`
		ParentID *string `json:"parentId"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &params); err != nil {
			return nil, fmt.Errorf("sessionRead %s: %w", method, err)
		}
	}
	switch method {
	case "info":
		return ExtensionSessionInfo(view), nil
	case "getCwd":
		return view.CWD, nil
	case "getSessionDir":
		return view.sessionDir(), nil
	case "isPersisted":
		return view.persisted(), nil
	case "usesDefaultSessionDir":
		return view.sessionDir() == defaultSessionDir(view.CWD), nil
	case "getSessionId":
		if view.Session == nil {
			return "", nil
		}
		return view.Session.ID(), nil
	case "getSessionFile":
		if !view.persisted() {
			return nil, nil
		}
		return view.Session.Path(), nil
	case "getSessionName":
		if view.Session != nil {
			if name := view.Session.GetSessionName(); name != "" {
				return name, nil
			}
		}
		return nil, nil
	case "getLeafId":
		if view.Session == nil {
			return nil, nil
		}
		return view.Session.LeafID(), nil
	case "getHeader":
		if view.Session == nil {
			return nil, nil
		}
		return view.Session.Header(), nil
	}
	log, err := newSessionReadLog(view.Session)
	if err != nil {
		return nil, err
	}
	switch method {
	case "getEntries":
		return rawEntries(log.entries), nil
	case "getLeafEntry":
		if log.leafID != nil {
			if entry := log.byID[*log.leafID]; entry != nil {
				return entry.raw, nil
			}
		}
		return nil, nil
	case "getEntry":
		if params.ID != nil {
			if entry := log.byID[*params.ID]; entry != nil {
				return entry.raw, nil
			}
		}
		return nil, nil
	case "getLabel":
		if params.ID == nil {
			return nil, nil
		}
		labels, _ := log.labels()
		if label, ok := labels[*params.ID]; ok {
			return label, nil
		}
		return nil, nil
	case "getChildren":
		children := []json.RawMessage{}
		for _, entry := range log.entries {
			if (entry.ParentID == nil && params.ParentID == nil) || (entry.ParentID != nil && params.ParentID != nil && *entry.ParentID == *params.ParentID) {
				children = append(children, entry.raw)
			}
		}
		return children, nil
	case "getBranch":
		return rawEntries(log.branch(params.FromID)), nil
	case "getTree":
		return log.tree(), nil
	case "buildContextEntries":
		return rawEntries(log.contextEntries()), nil
	case "buildSessionProjection":
		return log.projection(), nil
	case "buildSessionContext":
		projection := log.projection()
		return map[string]any{"messages": projection["messages"], "thinkingLevel": projection["thinkingLevel"], "model": projection["model"]}, nil
	}
	return nil, fmt.Errorf("sessionRead: unknown method %q", method)
}

// ExtensionSessionDir is the session directory a mode passes for its
// sessions: the --session-dir or configured directory as upstream
// SessionManager keeps it (utils/paths.ts normalizePath: a leading ~ and a
// file:// URL expand, any other path stays as given), or empty for the
// default.
func ExtensionSessionDir(override string) string {
	if strings.HasPrefix(override, "file://") {
		if parsed, err := url.Parse(override); err == nil {
			return filepath.FromSlash(parsed.Path)
		}
	}
	return ExpandTildePath(override)
}

// AppendExtensionEntry validates or allocates the identity and appends under one Session mutation lock. A rejected direct identity cannot overwrite an existing entry.
func AppendExtensionEntry(sess *Session, customType string, data any, direct *subprocess.DirectEntryAppend) (CustomEntry, error) {
	if sess == nil {
		return CustomEntry{}, fmt.Errorf("session not available")
	}
	sess.leafAppendMu.Lock()
	defer sess.leafAppendMu.Unlock()
	id, timestamp, err := ExtensionEntryIdentity(sess, direct)
	if err != nil {
		return CustomEntry{}, err
	}
	entry := CustomEntry{SessionEntryBase: SessionEntryBase{Type: "custom", ID: id, ParentID: sess.LeafID(), Timestamp: timestamp}, CustomType: customType, Data: data}
	if err := sess.AppendEntry(entry); err != nil {
		return CustomEntry{}, err
	}
	return entry, nil
}

// ExtensionEntryIdentity returns the id and timestamp of a custom entry an
// extension appends: those ctx.sessionManager.appendCustomEntry already
// generated in the extension process and returned, or new ones for
// pi.appendEntry. Upstream generates the id in the log's own process, checked
// against every existing id, so an id the log already holds is refused.
func ExtensionEntryIdentity(sess *Session, direct *subprocess.DirectEntryAppend) (string, string, error) {
	if direct == nil {
		if sess != nil {
			id, err := sess.generateEntryID()
			return id, RFC3339NowNano(), err
		}
		id, err := generateEntryID()
		return id, RFC3339NowNano(), err
	}
	if direct.ID == "" {
		return "", "", fmt.Errorf("entry id must not be empty")
	}
	if sess != nil {
		if _, exists := sess.EntryByID(direct.ID); exists {
			return "", "", fmt.Errorf("entry id %s already exists", direct.ID)
		}
	}
	if _, err := time.Parse(time.RFC3339Nano, direct.Timestamp); err != nil {
		return "", "", fmt.Errorf("entry timestamp %q: %w", direct.Timestamp, err)
	}
	return direct.ID, direct.Timestamp, nil
}
