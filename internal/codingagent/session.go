package codingagent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// CurrentSessionVersion mirrors upstream `CURRENT_SESSION_VERSION = 3`.
// Pi schema migrations are applied by session_restore.go.
const CurrentSessionVersion = sessionentry.CurrentSessionVersion

// ─── Session entry types ──────────────────────────────────────────────────────
//
// Layout matches upstream (packages/coding-agent/src/core/session-manager.ts).
// The wire format is one JSON object per line; line 1 is always a
// SessionHeader and subsequent lines are entries. Every non-header
// entry has a stable `id` and a `parentId` pointing at the entry it
// extends: this is how forks share a single JSONL: the leaf pointer
// jumps back to an earlier id and new entries become its children
// (siblings in the entry stream, but logically a separate branch).

// The entry values live in internal/sessionentry so coding/extension can name them without an import cycle.
type (
	SessionHeader          = sessionentry.SessionHeader
	SessionTreeNode        = sessionentry.SessionTreeNode
	SessionContextModel    = sessionentry.SessionContextModel
	SessionContext         = sessionentry.SessionContext
	ContextEditReplacement = sessionentry.ContextEditReplacement
	ContextEditableContent = sessionentry.ContextEditableContent
	NewSessionOptions      = sessionentry.NewSessionOptions
	ProjectedSessionEntry  = sessionentry.ProjectedSessionEntry
	SessionProjection      = sessionentry.SessionProjection
	SessionEntryBase       = sessionentry.SessionEntryBase
	MessageEntry           = sessionentry.MessageEntry
	ModelChangeEntry       = sessionentry.ModelChangeEntry
	UsageEntry             = sessionentry.UsageEntry
	CompactionEntry        = sessionentry.CompactionEntry
	BranchSummaryEntry     = sessionentry.BranchSummaryEntry
	CustomEntry            = sessionentry.CustomEntry
	CustomMessageEntry     = sessionentry.CustomMessageEntry
	LabelEntry             = sessionentry.LabelEntry
	SessionInfoEntry       = sessionentry.SessionInfoEntry
	BashExecutionEntry     = sessionentry.BashExecutionEntry
	BashExecutionMessage   = sessionentry.BashExecutionMessage
	ThinkingLevelEntry     = sessionentry.ThinkingLevelEntry
	SessionEntry           = sessionentry.SessionEntry
	RawEntry               = sessionentry.RawEntry
	ContextEditEntry       = sessionentry.ContextEditEntry
)

// GetLatestCompactionEntry returns the newest compaction entry, or nil when there is none. As upstream casts the newest entry of that type without validating it, a compaction entry whose JSON does not decode is still the newest: only its base members are set.
// Mirrors upstream packages/coding-agent/src/core/session-manager.ts getLatestCompactionEntry.
func GetLatestCompactionEntry(entries []SessionEntry) *CompactionEntry {
	for _, entry := range slices.Backward(entries) {
		if entry.Base().Type != "compaction" {
			continue
		}
		compaction, ok := entry.(CompactionEntry)
		if !ok {
			compaction = CompactionEntry{SessionEntryBase: entry.Base()}
		}
		return &compaction
	}
	return nil
}

// asMessage is the entry as a MessageEntry, false for any other entry and for a message entry whose record does not decode.
func asMessage(e SessionEntry) (MessageEntry, bool) {
	message, ok := e.(MessageEntry)
	return message, ok
}

// messageFor is asMessage that records a message entry which did not decode, so UndecodableCount can report it. Callers must clone the
// contained AgentMessage before handing it to a mutable pipeline.
func (s *Session) messageFor(e SessionEntry) (MessageEntry, bool) {
	message, ok := asMessage(e)
	if ok || e.Base().Type != "message" {
		return message, ok
	}
	s.msgMu.Lock()
	if s.undecodable == nil {
		s.undecodable = make(map[string]struct{})
	}
	s.undecodable[e.Base().ID] = struct{}{}
	s.msgMu.Unlock()
	return MessageEntry{}, false
}

// UndecodableCount returns how many distinct message entries failed to
// decode. Such entries are omitted from BuildContext, so a non-zero count
// means the reconstructed conversation is missing turns. Only entries
// already visited by messageFor are counted, so callers should read it
// after a full walk such as BuildContext.
func (s *Session) UndecodableCount() int {
	s.msgMu.Lock()
	defer s.msgMu.Unlock()
	return len(s.undecodable)
}

// ─── Session ──────────────────────────────────────────────────────────────────

// Session manages a single JSONL session file. All mutations append
// entries; no entry is ever rewritten or deleted (the parentId/leafID
// dance is how branches and undo work: abandoned entries simply
// become orphaned subtrees in the same file).
type Session struct {
	mu sync.RWMutex
	// projectionCalls counts BuildSessionProjection calls.
	projectionCalls atomic.Int64
	// leafAppendMu makes reading the leaf and appending its child one step,
	// so a background appender (cache warming) cannot fork the active chain.
	leafAppendMu sync.Mutex
	header       SessionHeader
	effectiveCWD *string
	entries      []SessionEntry
	// byID indexes entries for O(1) parent walks. Built on Load and
	// kept in sync by AppendEntry.
	byID       map[string]SessionEntry
	path       string
	sessionDir string
	leafID     *string
	// flushed reports whether the session file on disk holds the
	// header + buffered entries. Mirrors upstream SessionManager.flushed:
	// a fresh session is not written to disk until it holds a user or
	// assistant message, so opening and closing without chatting leaves no
	// file. Loaded sessions start flushed.
	flushed bool
	// hasConversation reports whether a user or assistant message entry has
	// been appended or loaded: the condition that creates the file
	// (upstream SessionManager._hasConversation).
	hasConversation bool

	msgMu sync.Mutex

	// undecodable holds the ids of message entries this build could not
	// decode. They are dropped from BuildContext, so the model would
	// otherwise be handed a transcript with turns silently missing;
	// callers surface the count so the loss is visible rather than
	// inferred from a model that has forgotten what it did.
	undecodable map[string]struct{}

	// stats is updated with the entry list while loading or appending so /session reads a bounded snapshot instead of reparsing durable history on the input loop.
	stats sessionAccountingAccumulator
}

// NewSession creates an in-memory session with an ISO millisecond header timestamp and an optional parent session path.
func NewSession(id, cwd string, parentSession ...string) *Session {
	parent := ""
	if len(parentSession) > 0 {
		parent = parentSession[0]
	}
	return &Session{
		header: SessionHeader{
			ParentSession: parent,
			Type:          "session",
			Version:       CurrentSessionVersion,
			ID:            id,
			Timestamp:     time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
			CWD:           cwd,
		},
		byID: make(map[string]SessionEntry),
	}
}

func (s *Session) ID() string { return s.header.ID }
func (s *Session) CWD() string {
	if s.effectiveCWD != nil {
		return *s.effectiveCWD
	}
	return s.header.CWD
}
func (s *Session) Path() string { return s.path }
func (s *Session) GetHeader() SessionHeader {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.header
}

func (s *Session) ParentSession() string { return s.header.ParentSession }
func (s *Session) SetPath(p string)      { s.path = p }

// GetEntries returns a copy of all session entries (in append order).
func (s *Session) GetEntries() []SessionEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SessionEntry, len(s.entries))
	copy(out, s.entries)
	return out
}

// GetEntryCount returns the number of distinct session entry IDs, excluding the header, without copying entries like GetEntries.
// A loaded file that repeats an ID counts it once. Mirrors upstream getEntryCount, which returns byId.size (session-manager.ts:1511-1513).
func (s *Session) GetEntryCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byID)
}

// GetLeafID returns the ID of the current leaf entry (the parent of the
// next AppendEntry). nil = "no entries yet, next append is a root".
func (s *Session) GetLeafID() *string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.leafID == nil {
		return nil
	}
	id := *s.leafID
	return &id
}

// SetLeafID moves the leaf pointer. Pass nil to reset to "before first
// entry" (next append becomes a new root). Returns an error if the
// supplied id isn't an existing entry.
func (s *Session) SetLeafID(id *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == nil {
		s.leafID = nil
		return nil
	}
	if _, ok := s.byID[*id]; !ok {
		return fmt.Errorf("session: SetLeafID: entry %q not found", *id)
	}
	cp := *id
	s.leafID = &cp
	return nil
}

// AppendEntry appends a complete entry to memory and the session file. The caller supplies id, parentId and timestamp; a nil parent denotes a root and is never inferred from the current leaf.
// Returns an error if marshalling, parsing, or file write fails. Persistence errors expose Node's filesystem message while retaining the underlying Go error for errors.Is and errors.As.
func (s *Session) AppendEntry(entry any) error {
	raw, err := marshalSessionLine(entry)
	if err != nil {
		return fmt.Errorf("session: marshal entry: %w", err)
	}
	se := sessionentry.DecodeSessionEntry(raw)
	base := se.Base()
	if _, undescribed := se.(RawEntry); undescribed {
		if err := json.Unmarshal(raw, &base); err != nil {
			return fmt.Errorf("session: parse entry base: %w", err)
		}
	}
	s.mu.Lock()
	s.stats.add(raw, base.Type)
	s.entries = append(s.entries, se)
	if s.byID == nil {
		s.byID = make(map[string]SessionEntry)
	}
	s.byID[base.ID] = se
	id := base.ID
	s.leafID = &id
	// A user or assistant message opens the gate that creates the file
	// (upstream _hasConversation, session-manager.ts:1166).
	if base.Type == "message" && !s.hasConversation {
		var probe struct {
			Message struct {
				Role string `json:"role"`
			} `json:"message"`
		}
		if json.Unmarshal(raw, &probe) == nil && (probe.Message.Role == "user" || probe.Message.Role == "assistant") {
			s.hasConversation = true
		}
	}
	s.mu.Unlock()
	return s.Persist(se)
}

// Persist writes one entry the manager already holds to the session file, as upstream SessionManager._persist does (session-manager.ts:1172).
// An unflushed session is not written until it holds a user or assistant message, so setup entries alone leave no file. The first write creates the file with the header and every buffered entry; later calls append the entry.
// A session without a file persists nothing.
func (s *Session) Persist(entry SessionEntry) error {
	raw := entry.Raw()
	s.mu.Lock()
	path := s.path
	hasConversation := s.hasConversation
	flushed := s.flushed
	// When the conversation gate just opened on an unflushed session,
	// snapshot header + all buffered entries for a single fresh write.
	var fullFlush [][]byte
	if path != "" && hasConversation && !flushed {
		hdr, _ := marshalSessionLine(s.header)
		fullFlush = make([][]byte, 0, len(s.entries)+1)
		fullFlush = append(fullFlush, hdr)
		for _, e := range s.entries {
			fullFlush = append(fullFlush, e.Raw())
		}
	}
	s.mu.Unlock()

	if path == "" {
		return nil
	}

	if !flushed && !hasConversation {
		return nil
	}
	if !flushed {
		if err := createSessionFile(path, fullFlush); err != nil {
			return err
		}
		s.mu.Lock()
		s.flushed = true
		s.mu.Unlock()
		return nil
	}
	return appendSessionLine(path, raw)
}

// sessionFileError preserves Go errno/path inspection while exposing the Node filesystem rejection from SessionManager._persist.
type sessionFileError struct {
	cause   error
	message string
}

func (e sessionFileError) Error() string { return e.message }
func (e sessionFileError) Unwrap() error { return e.cause }

func sessionPersistenceError(err error, operation, path string) error {
	return sessionFileError{cause: err, message: tools.NodeFSError(err, operation, path)}
}

// appendSessionLine appends a single JSONL record, creating the file if
// it does not exist (a resumed session always already exists).
func appendSessionLine(path string, raw []byte) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return sessionPersistenceError(err, "open", path)
	}
	defer func() { _ = f.Close() }()
	if _, err := fmt.Fprintf(f, "%s\n", raw); err != nil {
		return sessionPersistenceError(err, "write", "")
	}
	return nil
}

// writeSessionLines writes header + all entries to the file, replacing its
// content. Mirrors upstream's openSync(file, "w") in _rewriteFile.
func writeSessionLines(path string, lines [][]byte) error {
	return writeSessionFile(path, lines, os.O_TRUNC)
}

// createSessionFile writes header + all buffered entries as a new file and
// fails, leaving an existing file untouched, when the path exists. Mirrors
// upstream's openSync(file, "wx") flush on the first user or assistant
// message (session-manager.ts:1175).
func createSessionFile(path string, lines [][]byte) error {
	return writeSessionFile(path, lines, os.O_EXCL)
}

func writeSessionFile(path string, lines [][]byte, mode int) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|mode, 0o644)
	if err != nil {
		return sessionPersistenceError(err, "open", path)
	}
	defer func() { _ = f.Close() }()
	for _, line := range lines {
		if _, err := fmt.Fprintf(f, "%s\n", line); err != nil {
			return sessionPersistenceError(err, "write", "")
		}
	}
	return nil
}

// SessionMessage is the union appendMessage takes (session-manager.ts: Message | CustomMessage | BashExecutionMessage): an
// agent.AgentMessage, which carries the LLM messages and the custom messages in AgentMessage.Custom, or a BashExecutionMessage.
type SessionMessage = extension.SessionMessage

// AppendMessage persists one message (an agent.AgentMessage or a BashExecutionMessage), generating a fresh entry id and timestamp
// and linking it to the current leaf (session-manager.ts appendMessage). Any other SessionMessage is an error.
func (s *Session) AppendMessage(message SessionMessage) (string, error) {
	switch typed := message.(type) {
	case agent.AgentMessage:
		return s.appendAgentMessage(typed)
	case *agent.AgentMessage:
		if typed == nil {
			return "", fmt.Errorf("session: AppendMessage: nil message")
		}
		return s.appendAgentMessage(*typed)
	case BashExecutionMessage:
		return s.AppendBashExecution(typed)
	case extension.CustomMessage:
		// upstream: appendMessage stores a CustomMessage as a "message" entry (session-manager.ts:1204); only the agent loop's
		// map form routes to a custom_message entry.
		custom := map[string]any{"role": agent.RoleCustom, "customType": typed.CustomType, "content": typed.Content, "display": typed.Display, "timestamp": typed.Timestamp}
		if typed.Details != nil {
			custom["details"] = typed.Details
		}
		return s.appendMessageEntry(agent.AgentMessage{Custom: custom})
	default:
		return "", fmt.Errorf("session: AppendMessage: unsupported message %T", message)
	}
}

func (s *Session) appendAgentMessage(msg agent.AgentMessage) (string, error) {
	// Custom messages round-trip as "custom_message" entries, not generic
	// "message" entries: messagesFromSession only reconstructs them from that
	// type. Routing them here keeps OnMessagePersist the single persistence
	// site for everything the agent delivers, so a queued custom message is
	// recorded at the point it reached the model rather than the point the
	// extension created it.
	if msg.Custom != nil {
		if msg.Custom["role"] == agent.RoleBashExecution {
			// The map form of a bashExecution message is stored as AppendBashExecution stores it.
			wire, err := json.Marshal(msg.Custom)
			if err != nil {
				return "", fmt.Errorf("session: AppendMessage: %w", err)
			}
			var bash BashExecutionMessage
			if err := json.Unmarshal(wire, &bash); err != nil {
				return "", fmt.Errorf("session: AppendMessage: %w", err)
			}
			return s.AppendBashExecution(bash)
		}
		customType, _ := msg.Custom["customType"].(string)
		display, _ := msg.Custom["display"].(bool)
		return s.AppendCustomMessageEntry(customType, msg.Custom["content"], display, msg.Custom["details"])
	}
	return s.appendMessageEntry(msg)
}

// appendMessageEntry stores msg as a "message" entry linked to the current leaf.
func (s *Session) appendMessageEntry(msg agent.AgentMessage) (string, error) {
	if _, err := json.Marshal(msg); err != nil {
		return "", fmt.Errorf("session: AppendMessage: %w", err)
	}
	s.leafAppendMu.Lock()
	defer s.leafAppendMu.Unlock()
	id, err := s.generateEntryID()
	if err != nil {
		return "", err
	}
	parent := s.GetLeafID()
	entry := MessageEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "message",
			ID:        id,
			ParentID:  parent,
			Timestamp: RFC3339NowNano(),
		},
		Message: msg,
	}
	if err := s.AppendEntry(entry); err != nil {
		return "", err
	}
	return id, nil
}

// AppendCustomMessageEntry persists an extension-injected custom message as a
// "custom_message" entry and returns the new entry id.
func (s *Session) AppendCustomMessageEntry(customType string, content any, display bool, details any) (string, error) {
	entry, err := s.customMessageEntry(customType, content, display, details, true)
	return entry.ID, err
}

func (s *Session) customMessageEntry(customType string, content any, display bool, details any, persist bool) (CustomMessageEntry, error) {
	s.leafAppendMu.Lock()
	defer s.leafAppendMu.Unlock()
	id, err := s.generateEntryID()
	if err != nil {
		return CustomMessageEntry{}, err
	}
	entry := CustomMessageEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "custom_message",
			ID:        id,
			ParentID:  s.GetLeafID(),
			Timestamp: RFC3339NowNano(),
		},
		CustomType: customType,
		Content:    content,
		Display:    display,
		Details:    details,
	}
	if persist {
		if err := s.AppendEntry(entry); err != nil {
			return CustomMessageEntry{}, err
		}
	}
	return entry, nil
}

// AppendSessionInfo records a sanitized name change with a collision-checked ID on the active branch.
func (s *Session) AppendSessionInfo(name string) (string, error) {
	id, _, err := s.AppendSessionInfoName(name)
	return id, err
}

// AppendSessionInfoName is AppendSessionInfo that also returns the name GetSessionName reports for the appended entry. Concurrent appends cannot change the returned name, so a caller publishes the name its own entry set.
func (s *Session) AppendSessionInfoName(name string) (id, current string, err error) {
	// upstream: packages/coding-agent/src/core/session-manager.ts:appendSessionInfo
	name = jsTrim(strings.Join(strings.FieldsFunc(name, func(r rune) bool { return r == '\r' || r == '\n' }), " "))
	s.leafAppendMu.Lock()
	defer s.leafAppendMu.Unlock()
	id, err = s.generateEntryID()
	if err != nil {
		return "", "", err
	}
	entry := SessionInfoEntry{SessionEntryBase: SessionEntryBase{Type: "session_info", ID: id, ParentID: s.GetLeafID(), Timestamp: RFC3339NowNano()}, Name: name}
	if err := s.AppendEntry(entry); err != nil {
		return "", "", err
	}
	s.mu.RLock()
	appended := s.byID[id]
	s.mu.RUnlock()
	current, _ = sessionInfoName(appended.Raw())
	return id, current, nil
}

// sessionInfoName decodes the name a persisted session_info entry establishes. ok is false for an undecodable entry, which GetSessionName skips.
func sessionInfoName(raw []byte) (name string, ok bool) {
	var si SessionInfoEntry
	// upstream: coding-agent/src/core/session-manager.ts:parseSessionEntryLine
	if err := json.Unmarshal(raw, &si); err != nil {
		return "", false
	}
	return jsTrim(si.Name), true
}

// AppendCustomEntry records extension-owned data with a collision-checked ID on the active branch.
func (s *Session) AppendCustomEntry(customType string, data any) (string, error) {
	s.leafAppendMu.Lock()
	defer s.leafAppendMu.Unlock()
	id, err := s.generateEntryID()
	if err != nil {
		return "", err
	}
	entry := CustomEntry{SessionEntryBase: SessionEntryBase{Type: "custom", ID: id, ParentID: s.GetLeafID(), Timestamp: RFC3339NowNano()}, CustomType: customType, Data: data}
	if err := s.AppendEntry(entry); err != nil {
		return "", err
	}
	return id, nil
}

// AppendBashExecution persists a completed user bash message, retaining the timestamp captured before any deferred append.
// Returns the new entry id (the new leaf).
func (s *Session) AppendBashExecution(message BashExecutionMessage) (string, error) {
	s.leafAppendMu.Lock()
	defer s.leafAppendMu.Unlock()
	id, err := s.generateEntryID()
	if err != nil {
		return "", err
	}
	parent := s.GetLeafID()
	entry := BashExecutionEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "message",
			ID:        id,
			ParentID:  parent,
			Timestamp: RFC3339NowNano(),
		},
		Role:               "bashExecution",
		Command:            message.Command,
		Output:             message.Output,
		ExitCode:           message.ExitCode,
		Cancelled:          message.Cancelled,
		Truncated:          message.Truncated,
		FullOutputPath:     message.FullOutputPath,
		ExcludeFromContext: message.ExcludeFromContext,
		MessageTimestamp:   message.Timestamp,
	}
	if err := s.AppendEntry(entry); err != nil {
		return "", err
	}
	return id, nil
}

// GetEntry looks up a session entry by its hex ID. Returns false if
// the ID isn't present. Used by the chat layer to render markers (e.g.
// branch-summary chip on fork) without exporting the byID map.
func (s *Session) GetEntry(id string) (SessionEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.byID[id]
	return e, ok
}

// GetSessionName returns the latest user-defined session name (set via
// `/name`), or empty string when none has been set. Mirrors upstream
// `core/session-manager.ts::getSessionName` (v0.69.0:923-933): walks
// entries in reverse, returns the trimmed name from the most recent
// `session_info` entry; an empty trimmed name explicitly clears the
// name (later session_info entries with empty `name` field shadow
// earlier non-empty ones).
func (s *Session) GetSessionName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, v := range slices.Backward(s.entries) {
		if v.Base().Type != "session_info" {
			continue
		}
		if name, ok := sessionInfoName(v.Raw()); ok {
			return name
		}
	}
	return ""
}

// AppendModelChange appends a model_change entry as a child of the current leaf, advances the leaf, and returns the entry id
// (session-manager.ts appendModelChange(provider, modelId)).
func (s *Session) AppendModelChange(provider, modelID string) (string, error) {
	s.leafAppendMu.Lock()
	defer s.leafAppendMu.Unlock()
	id, err := s.generateEntryID()
	if err != nil {
		return "", err
	}
	entry := ModelChangeEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "model_change",
			ID:        id,
			ParentID:  s.GetLeafID(),
			Timestamp: RFC3339NowNano(),
		},
		Provider: provider,
		ModelID:  modelID,
	}
	if err := s.AppendEntry(entry); err != nil {
		return "", err
	}
	return id, nil
}

// AppendThinkingLevelChange persists a thinking_level_change audit entry.
// Mirrors upstream agentSession.appendThinkingLevelChange (agent-session.ts).
// Called when the user cycles the thinking level via Shift+Tab. It returns the new entry's id.
func (s *Session) AppendThinkingLevelChange(level string) (string, error) {
	s.leafAppendMu.Lock()
	defer s.leafAppendMu.Unlock()
	id, err := s.generateEntryID()
	if err != nil {
		return "", err
	}
	entry := ThinkingLevelEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "thinking_level_change",
			ID:        id,
			ParentID:  s.GetLeafID(),
			Timestamp: RFC3339NowNano(),
		},
		ThinkingLevel: level,
	}
	return id, s.AppendEntry(entry)
}

// AppendUsage appends model-attributed usage that does not participate in LLM
// context and returns the appended entry. Mirrors upstream
// SessionManager.appendUsage (session-manager.ts).
func (s *Session) AppendUsage(kind, provider, model string, usage ai.Usage, note string) (UsageEntry, error) {
	s.leafAppendMu.Lock()
	defer s.leafAppendMu.Unlock()
	id, err := s.generateEntryID()
	if err != nil {
		return UsageEntry{}, err
	}
	entry := UsageEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "usage",
			ID:        id,
			ParentID:  s.GetLeafID(),
			Timestamp: RFC3339NowNano(),
		},
		Kind:     kind,
		Provider: provider,
		Model:    model,
		Usage:    usage,
		Note:     note,
	}
	if err := s.AppendEntry(entry); err != nil {
		return UsageEntry{}, err
	}
	return entry, nil
}

// AppendCompaction records a compaction event as a new leaf.
// Mirrors upstream SessionManager.appendCompaction (session-manager.ts). An
// empty firstKeptEntryID is Pi's null: the entry stores its own ID, so the
// compaction retains no preceding entries. The entry also records the current
// projected system state, when there is one, stamped with the entry time.
func (s *Session) AppendCompaction(summary, firstKeptEntryID string, tokensBefore int, details any, fromHook bool, usage *ai.Usage) (string, error) {
	now := time.Now().UTC()
	systemMessage, err := compactionSystemMessage(s.BuildSessionProjection().Messages, now.UnixMilli())
	if err != nil {
		return "", err
	}
	s.leafAppendMu.Lock()
	defer s.leafAppendMu.Unlock()
	id, err := s.generateEntryID()
	if err != nil {
		return "", err
	}
	if firstKeptEntryID == "" {
		firstKeptEntryID = id
	}
	entry := CompactionEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "compaction",
			ID:        id,
			ParentID:  s.GetLeafID(),
			Timestamp: now.Format("2006-01-02T15:04:05.000Z"),
		},
		Summary:          summary,
		FirstKeptEntryID: firstKeptEntryID,
		TokensBefore:     tokensBefore,
		Details:          details,
		Usage:            usage,
		FromHook:         fromHook,
		SystemMessage:    systemMessage,
	}
	if err := s.AppendEntry(entry); err != nil {
		return "", err
	}
	return id, nil
}

// BranchWithSummary starts a new branch at parentID with a summary of the
// abandoned path. parentID is the explicit parent: nil means the entry is a
// root-level node (no parent). fromId records the leaf being abandoned ("root"
// when there is none). The new entry becomes the leaf.
//
// Mirrors upstream branchWithSummary(branchFromId: string | null, ...) in
// session-manager.ts.
func (s *Session) BranchWithSummary(parentID *string, summary string, details any, fromHook bool, usage *ai.Usage) (string, error) {
	s.leafAppendMu.Lock()
	defer s.leafAppendMu.Unlock()
	s.mu.RLock()
	parentFound := true
	if parentID != nil {
		_, parentFound = s.byID[*parentID]
	}
	fromID := "root"
	if s.leafID != nil {
		fromID = *s.leafID
	}
	s.mu.RUnlock()
	if !parentFound {
		return "", fmt.Errorf("Entry %s not found", *parentID)
	}
	id, err := s.generateEntryID()
	if err != nil {
		return "", err
	}
	entry := BranchSummaryEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "branch_summary",
			ID:        id,
			ParentID:  parentID, // nil = root-level entry
			Timestamp: RFC3339NowNano(),
		},
		FromID:   fromID,
		Summary:  summary,
		Details:  details,
		FromHook: fromHook,
		Usage:    usage,
	}
	if err := s.AppendEntry(entry); err != nil {
		return "", err
	}
	return id, nil
}

// AppendLabelChange writes a label entry for targetID and returns its id. label=nil clears
// any existing label. Mirrors upstream appendLabelChange in session-manager.ts.
func (s *Session) AppendLabelChange(targetID string, label *string) (string, error) {
	s.mu.RLock()
	_, ok := s.byID[targetID]
	s.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("Entry %s not found", targetID)
	}
	s.leafAppendMu.Lock()
	defer s.leafAppendMu.Unlock()
	id, err := s.generateEntryID()
	if err != nil {
		return "", err
	}
	entry := LabelEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "label",
			ID:        id,
			ParentID:  s.GetLeafID(),
			Timestamp: RFC3339NowNano(),
		},
		TargetID: targetID,
		Label:    label,
	}
	return id, s.AppendEntry(entry)
}

// Branch moves the leaf pointer to forkFromID so the next AppendEntry
// becomes a sibling branch off that point. Existing entries are not
// touched: abandoned tail simply becomes an orphan subtree.
//
// Mirrors upstream `branch(id)`. Use SetLeafID(nil) for the "branch
// before first entry" case (root-reset).
func (s *Session) Branch(forkFromID string) error {
	s.mu.RLock()
	_, ok := s.byID[forkFromID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("Entry %s not found", forkFromID)
	}
	id := forkFromID
	return s.SetLeafID(&id)
}

// GetBranch returns the root-to-leaf path (root first) of the current leaf, or of fromID when it is given;
// nil when the entry does not exist. Mirrors upstream SessionManager.getBranch(fromId?).
func (s *Session) GetBranch(fromID ...string) []SessionEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(fromID) > 0 {
		return s.pathToLocked(fromID[0])
	}
	if s.leafID == nil {
		return nil
	}
	return s.pathToLocked(*s.leafID)
}

// BuildContext returns the model-visible message list for the path ending at
// leafID, or at the current leaf when leafID is nil. It is the Messages field
// of the canonical session projection (session-manager.ts
// buildSessionContext), so latest compaction, retained entries, and
// context_edit entries on that path all apply. An unset current leaf
// (SetLeafID(nil)) yields no messages.
func (s *Session) BuildContext(leafID *string) []agent.AgentMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	target := s.leafID
	if leafID != nil {
		target = leafID
	}
	if target == nil {
		return nil
	}
	return buildSessionProjection(s.pathToLocked(*target), s.messageFor).Messages
}

// BuildSessionProjection returns the provenance-preserving projection of the
// current branch (session-manager.ts SessionManager.buildSessionProjection).
func (s *Session) BuildSessionProjection() SessionProjection {
	s.projectionCalls.Add(1)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.leafID == nil {
		return SessionProjection{ThinkingLevel: "off"}
	}
	return buildSessionProjection(s.pathToLocked(*s.leafID), s.messageFor)
}

// BuildSessionContext returns the messages, thinking level and model of the branch ending at the current leaf (session-manager.ts buildSessionContext). An unset leaf yields no messages.
func (s *Session) BuildSessionContext() SessionContext {
	projection := s.BuildSessionProjection()
	messages := projection.Messages
	if messages == nil {
		messages = []agent.AgentMessage{}
	}
	return SessionContext{Messages: messages, ThinkingLevel: projection.ThinkingLevel, Model: projection.Model}
}

// BuildContextEntries returns the compaction-aware entry list of the branch ending at the current leaf (session-manager.ts buildContextEntries). An unset leaf yields no entries.
func (s *Session) BuildContextEntries() []SessionEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.leafID == nil {
		return []SessionEntry{}
	}
	return buildContextEntries(s.pathToLocked(*s.leafID), s.messageFor)
}

// pathToLocked returns the root-to-leaf entry path ending at leafID. The
// caller holds s.mu.
func (s *Session) pathToLocked(leafID string) []SessionEntry {
	leaf, ok := s.byID[leafID]
	if !ok {
		return nil
	}
	// Walk leaf to root, then reverse; prepending would be O(N^2) in depth.
	var path []SessionEntry
	cur := leaf
	for {
		path = append(path, cur)
		parentID := cur.Base().ParentID
		if parentID == nil {
			break
		}
		next, ok := s.byID[*parentID]
		if !ok {
			break
		}
		cur = next
	}
	slices.Reverse(path)
	return path
}

// GetTree builds the session's tree and returns its roots (session-manager.ts getTree): an entry without a parent, one that
// is its own parent and an orphan whose parent is missing are roots, in entry order. Children are sorted oldest first by
// timestamp (a stable sort, so an unparsable timestamp keeps its place); the roots are not sorted. A label resolves from the
// latest label entry for its target.
func (s *Session) GetTree() []*SessionTreeNode {
	s.mu.RLock()
	defer s.mu.RUnlock()

	nodes := make(map[string]*SessionTreeNode, len(s.entries))
	for _, e := range s.entries {
		nodes[e.Base().ID] = &SessionTreeNode{Entry: e}
	}
	// Last LabelEntry for a given target wins (later entries overwrite
	// earlier ones, including a nil Label which clears the label).
	for _, e := range s.entries {
		if e.Base().Type == "label" {
			var le LabelEntry
			if err := json.Unmarshal(e.Raw(), &le); err == nil {
				if n, ok := nodes[le.TargetID]; ok {
					if le.Label != nil && *le.Label != "" {
						n.Label = *le.Label
						n.LabelTimestamp = e.Base().Timestamp
					} else {
						n.Label = ""
						n.LabelTimestamp = ""
					}
				}
			}
		}
	}
	var roots []*SessionTreeNode
	for _, e := range s.entries {
		n := nodes[e.Base().ID]
		if e.Base().ParentID == nil || *e.Base().ParentID == e.Base().ID {
			roots = append(roots, n)
			continue
		}
		parent, ok := nodes[*e.Base().ParentID]
		if !ok {
			roots = append(roots, n)
			continue
		}
		parent.Children = append(parent.Children, n)
	}
	stack := slices.Clone(roots)
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		slices.SortStableFunc(n.Children, func(a, b *SessionTreeNode) int {
			delta := ai.DateParse(a.Entry.Base().Timestamp) - ai.DateParse(b.Entry.Base().Timestamp)
			switch {
			case delta < 0:
				return -1
			case delta > 0:
				return 1
			}
			return 0
		})
		stack = append(stack, n.Children...)
	}
	return roots
}

// treeRoot is the session's roots under one synthetic root node with an empty entry, the shape the tree selector and the
// ASCII renderer take.
func (s *Session) treeRoot() *SessionTreeNode { return &SessionTreeNode{Children: s.GetTree()} }

// ─── ID generators ────────────────────────────────────────────────────────────

// generateEntryID checks the Session index while the caller holds leafAppendMu through the subsequent append.
func (s *Session) generateEntryID() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return generateUniqueEntryID(func(id string) bool { _, exists := s.byID[id]; return exists })
}

// generateEntryID returns an eight-character lowercase hexadecimal entry ID.
func generateEntryID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// generateUniqueEntryID mirrors Pi's collision-checked short IDs and UUID fallback.
func generateUniqueEntryID(has func(string) bool) (string, error) {
	for range 100 {
		id, err := generateEntryID()
		if err != nil {
			return "", err
		}
		if !has(id) {
			return id, nil
		}
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// generateSessionID returns a time-ordered UUIDv7 using Pi's shared generator.
func generateSessionID() (string, error) {
	return ai.UUIDv7(nil)
}

// GenerateSessionID returns a time-ordered UUIDv7 for a new session.
func GenerateSessionID() (string, error) { return generateSessionID() }

// RFC3339NowNano returns the current UTC time in Pi's millisecond ISO format.
func RFC3339NowNano() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

// RenderTreeASCII renders a SessionTreeNode as an ASCII tree. Exported
// wrapper so the coding package can produce /tree output without
// duplicating the renderer.
func RenderTreeASCII(root *SessionTreeNode) string { return renderTreeASCII(root) }
