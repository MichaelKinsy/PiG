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
	"math"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
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
	parsed    *orderedjson.Object
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
		// Upstream's SessionManager holds JSON.parse of each line, so a consumer sees the entry as JSON.stringify writes it: members in insertion order with integer-like keys first, numbers and strings in their JavaScript form.
		canonical, err := jsonstringify.Canonicalize(raw)
		if err != nil {
			return log, fmt.Errorf("decode retained session entry: %w", err)
		}
		parsed.raw = json.RawMessage(canonical)
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

// object is the entry's JSON as an insertion-ordered object, parsed once per read; callers do not modify it.
func (e *sessionReadEntry) object() *orderedjson.Object {
	if e.parsed == nil {
		object, err := orderedjson.Parse(e.raw)
		if err != nil {
			object = orderedjson.New()
		}
		e.parsed = object
	}
	return e.parsed
}

// entryObject is raw JSON as an insertion-ordered object.
func entryObject(raw json.RawMessage) *orderedjson.Object {
	object, err := orderedjson.Parse(raw)
	if err != nil {
		return orderedjson.New()
	}
	return object
}

// member is one member's raw JSON; absent (undefined) is nil.
func member(object *orderedjson.Object, key string) json.RawMessage {
	raw, _ := object.Get(key)
	return raw
}

// isNullish is JavaScript's `== null` for a member: absent or JSON null.
func isNullish(raw json.RawMessage) bool { return len(raw) == 0 || string(raw) == "null" }

// jsTruthy is JavaScript truthiness of a parsed JSON value.
func jsTruthy(raw json.RawMessage) bool {
	switch string(raw) {
	case "", "null", "false", `""`, "0", "-0":
		return false
	}
	return true
}

func memberString(object *orderedjson.Object, key string) string {
	var value string
	_ = json.Unmarshal(member(object, key), &value)
	return value
}

// setIfDefined sets key to the raw value of source's member when source has it, as an object literal member whose value is not undefined.
func setIfDefined(target *orderedjson.Object, key string, raw json.RawMessage) {
	if len(raw) > 0 {
		target.Set(key, raw)
	}
}

func marshalObject(object *orderedjson.Object) json.RawMessage {
	encoded, _ := object.MarshalJSON()
	return encoded
}

func jsTimestampRaw(timestamp string) json.RawMessage {
	encoded, _ := json.Marshal(jsTimestamp(timestamp))
	return encoded
}

// contextMessages mirrors upstream sessionEntryToContextMessages. Each message is raw JSON written in the member order upstream's object literals and spreads give it (core/messages.ts:100-137).
func contextMessages(entry *sessionReadEntry) []json.RawMessage {
	object := entry.object()
	switch entry.Type {
	case "message":
		raw := member(object, "message")
		message, err := orderedjson.Parse(raw)
		if err != nil {
			// `entry.message` is not an object: upstream's message.role is not a role, so the value is the message.
			if len(raw) == 0 {
				raw = json.RawMessage("null")
			}
			return []json.RawMessage{raw}
		}
		if isNullish(member(message, "content")) {
			switch memberString(message, "role") {
			case "system":
				message.Set("content", json.RawMessage(`""`))
				return []json.RawMessage{marshalObject(message)}
			case "user", "assistant", "toolResult":
				message.Set("content", json.RawMessage(`[]`))
				return []json.RawMessage{marshalObject(message)}
			}
		}
		return []json.RawMessage{raw}
	case "custom_message":
		message := orderedjson.New()
		message.Set("role", json.RawMessage(`"custom"`))
		setIfDefined(message, "customType", member(object, "customType"))
		if content := member(object, "content"); !isNullish(content) {
			message.Set("content", content)
		} else {
			message.Set("content", json.RawMessage(`[]`))
		}
		setIfDefined(message, "display", member(object, "display"))
		setIfDefined(message, "details", member(object, "details"))
		message.Set("timestamp", jsTimestampRaw(entry.Timestamp))
		return []json.RawMessage{marshalObject(message)}
	case "branch_summary":
		// upstream tests `entry.summary` for truthiness (session-manager.ts:458), so a summary that is not a string still contributes.
		if summary := member(object, "summary"); jsTruthy(summary) {
			message := orderedjson.New()
			message.Set("role", json.RawMessage(`"branchSummary"`))
			message.Set("summary", summary)
			setIfDefined(message, "fromId", member(object, "fromId"))
			message.Set("timestamp", jsTimestampRaw(entry.Timestamp))
			return []json.RawMessage{marshalObject(message)}
		}
	case "compaction":
		summary := orderedjson.New()
		summary.Set("role", json.RawMessage(`"compactionSummary"`))
		setIfDefined(summary, "summary", member(object, "summary"))
		setIfDefined(summary, "tokensBefore", member(object, "tokensBefore"))
		summary.Set("timestamp", jsTimestampRaw(entry.Timestamp))
		if system := member(object, "systemMessage"); jsTruthy(system) {
			return []json.RawMessage{system, marshalObject(summary)}
		}
		return []json.RawMessage{marshalObject(summary)}
	}
	return []json.RawMessage{}
}

// projectRawContextEntry mirrors upstream projectContextEntry: a context_edit
// replaces the content of the messages its target contributes, or omits them.
func projectRawContextEntry(entry *sessionReadEntry, edit *sessionReadEntry) []json.RawMessage {
	messages := contextMessages(entry)
	if edit == nil {
		return messages
	}
	replacement := member(edit.object(), "replacement")
	if isNullish(replacement) {
		return []json.RawMessage{}
	}
	content := member(entryObject(replacement), "content")
	out := make([]json.RawMessage, len(messages))
	for index, raw := range messages {
		message, err := orderedjson.Parse(raw)
		role := memberString(message, "role")
		if err != nil || (role != "user" && role != "assistant" && role != "toolResult" && role != "custom") {
			out[index] = raw
			continue
		}
		// `{...message, content}`: the member keeps its position, or follows the message's; an undefined content is not written.
		switch {
		case len(content) == 0:
			message.Delete("content")
		case content[0] == '"' && (role == "assistant" || role == "toolResult"):
			block := orderedjson.New()
			block.Set("type", json.RawMessage(`"text"`))
			block.Set("text", content)
			message.Set("content", json.RawMessage("["+string(marshalObject(block))+"]"))
		default:
			message.Set("content", content)
		}
		out[index] = marshalObject(message)
	}
	return out
}

// sessionModel is upstream's `{ provider, modelId }`; an undefined member is not written.
type sessionModel struct {
	Provider json.RawMessage `json:"provider,omitempty"`
	ModelID  json.RawMessage `json:"modelId,omitempty"`
}

// projectedSessionEntry is upstream's ProjectedSessionEntry.
type projectedSessionEntry struct {
	SourceEntry json.RawMessage   `json:"sourceEntry"`
	Messages    []json.RawMessage `json:"messages"`
}

// sessionContext is upstream's SessionContext, in its member order.
type sessionContext struct {
	Messages      []json.RawMessage `json:"messages"`
	ThinkingLevel json.RawMessage   `json:"thinkingLevel,omitempty"`
	Model         *sessionModel     `json:"model"`
}

// sessionProjection is upstream's SessionProjection, in its member order.
type sessionProjection struct {
	Entries       []projectedSessionEntry `json:"entries"`
	Messages      []json.RawMessage       `json:"messages"`
	ThinkingLevel json.RawMessage         `json:"thinkingLevel,omitempty"`
	Model         *sessionModel           `json:"model"`
}

// projection mirrors upstream buildSessionProjection.
func (l sessionReadLog) projection() sessionProjection {
	path := l.sessionPath()
	thinkingLevel, model := json.RawMessage(`"off"`), (*sessionModel)(nil)
	for _, entry := range path {
		switch entry.Type {
		case "thinking_level_change":
			thinkingLevel = member(entry.object(), "thinkingLevel")
		case "model_change":
			object := entry.object()
			model = &sessionModel{Provider: member(object, "provider"), ModelID: member(object, "modelId")}
		case "message":
			message, err := orderedjson.Parse(member(entry.object(), "message"))
			if err == nil && memberString(message, "role") == "assistant" {
				model = &sessionModel{Provider: member(message, "provider"), ModelID: member(message, "model")}
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
	projected := make([]projectedSessionEntry, 0, len(contextEntries))
	messages := []json.RawMessage{}
	for index, entry := range contextEntries {
		entryMessages := []json.RawMessage{}
		if entry.Type != "compaction" || index == 0 {
			entryMessages = projectRawContextEntry(entry, edits[entry.ID])
		}
		projected = append(projected, projectedSessionEntry{SourceEntry: entry.raw, Messages: entryMessages})
		messages = append(messages, entryMessages...)
	}
	return sessionProjection{Entries: projected, Messages: messages, ThinkingLevel: thinkingLevel, Model: model}
}

// extensionSessionTreeNode is upstream's SessionTreeNode, in its member order.
type extensionSessionTreeNode struct {
	Entry          json.RawMessage             `json:"entry"`
	Children       []*extensionSessionTreeNode `json:"children"`
	Label          *string                     `json:"label,omitempty"`
	LabelTimestamp *string                     `json:"labelTimestamp,omitempty"`
}

// tree mirrors upstream SessionManager.getTree.
func (l sessionReadLog) tree() []*extensionSessionTreeNode {
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
	var encode func(n *node) *extensionSessionTreeNode
	encode = func(n *node) *extensionSessionTreeNode {
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
		out := &extensionSessionTreeNode{Entry: n.entry.raw, Children: make([]*extensionSessionTreeNode, 0, len(n.children))}
		for _, child := range n.children {
			out.Children = append(out.Children, encode(child))
		}
		if label, ok := labels[n.entry.ID]; ok {
			timestamp := timestamps[n.entry.ID]
			out.Label, out.LabelTimestamp = &label, &timestamp
		}
		return out
	}
	out := make([]*extensionSessionTreeNode, 0, len(roots))
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
		return sessionContext{Messages: projection.Messages, ThinkingLevel: projection.ThinkingLevel, Model: projection.Model}, nil
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
