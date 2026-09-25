package codingagent

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	harnesssession "github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

// CurrentSessionVersion mirrors upstream `CURRENT_SESSION_VERSION = 3`.
// Bumping this requires a migration step in session_resume.go.
const CurrentSessionVersion = 3

// ─── Session entry types ──────────────────────────────────────────────────────
//
// Layout matches upstream (packages/coding-agent/src/core/session-manager.ts).
// The wire format is one JSON object per line; line 1 is always a
// SessionHeader and subsequent lines are entries. Every non-header
// entry has a stable `id` and a `parentId` pointing at the entry it
// extends: this is how forks share a single JSONL: the leaf pointer
// jumps back to an earlier id and new entries become its children
// (siblings in the entry stream, but logically a separate branch).

type SessionHeader struct {
	Type      string `json:"type"` // always "session"
	Version   int    `json:"version,omitempty"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	CWD       string `json:"cwd"`
	// ParentSession holds the absolute path of the SOURCE jsonl on a
	// clone (separate JSONL with linear path-to-leaf snapshot). Empty
	// on plain Create.
	ParentSession string `json:"parentSession,omitempty"`
}

// SessionEntryBase is the common prefix on every non-header entry.
// `ParentID` is `*string` (not "") so we can distinguish "root entry"
// (parentId: null) from "missing field". The wire format requires
// `"parentId": null` literally for roots.
type SessionEntryBase struct {
	Type      string  `json:"type"`
	ID        string  `json:"id"`
	ParentID  *string `json:"parentId"`
	Timestamp string  `json:"timestamp"`
}

type MessageEntry struct {
	SessionEntryBase
	Message agent.AgentMessage `json:"message"`
}

type ModelChangeEntry struct {
	SessionEntryBase
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
}

// UsageEntry is model-attributed usage that does not enter LLM context, such
// as a cache-warming refresh. Mirrors upstream session-manager.ts UsageEntry.
type UsageEntry struct {
	SessionEntryBase
	// Kind is an arbitrary usage category, such as "cache_warm".
	Kind     string   `json:"kind"`
	Provider string   `json:"provider"`
	Model    string   `json:"model"`
	Usage    ai.Usage `json:"usage"`
	// Note is an optional human-readable qualifier for usage notices.
	Note string `json:"note,omitempty"`
}

// CompactionEntry field order matches Pi's appendCompaction object literal so
// Pig-written entries serialize with the same key order.
type CompactionEntry struct {
	SessionEntryBase
	Summary          string    `json:"summary"`
	FirstKeptEntryID string    `json:"firstKeptEntryId"`
	TokensBefore     int       `json:"tokensBefore"`
	Details          any       `json:"details,omitempty"`
	Usage            *ai.Usage `json:"usage,omitempty"`
	FromHook         bool      `json:"fromHook"`
	// SystemMessage is the complete prompt and tool state at this compaction
	// boundary. It is absent when the projected context has no system state.
	SystemMessage json.RawMessage `json:"systemMessage,omitempty"`
}

type BranchSummaryEntry struct {
	SessionEntryBase
	FromID   string    `json:"fromId"`
	Summary  string    `json:"summary"`
	Details  any       `json:"details,omitempty"`
	FromHook bool      `json:"fromHook,omitempty"`
	Usage    *ai.Usage `json:"usage,omitempty"`
}

type CustomEntry struct {
	SessionEntryBase
	CustomType string `json:"customType"`
	Data       any    `json:"data,omitempty"`
}

type CustomMessageEntry struct {
	SessionEntryBase
	CustomType string `json:"customType"`
	Content    any    `json:"content"` // string | []ContentBlock
	Display    bool   `json:"display"`
	Details    any    `json:"details,omitempty"`
}

// LabelEntry is upstream's "user-renamed this branch" marker. Carries
// nil Label to delete a previously-set label.
type LabelEntry struct {
	SessionEntryBase
	TargetID string  `json:"targetId"`
	Label    *string `json:"label"`
}

// SessionInfoEntry stores a session-level name (set via `/name`).
// Persisted as type=session_info so it survives across resumes.
type SessionInfoEntry struct {
	SessionEntryBase
	Name string `json:"name,omitempty"`
}

// BashExecutionEntry persists a `!cmd` invocation. Mirrors
// upstream `BashExecutionMessage` (.upstream/v0.69.0/.../core/messages.ts:29-43
// + agent-session.ts:2585-2614).
//
// Wire shape, matching upstream session-manager.ts:834:
//
//	{
//	  "type": "message",          // outer SessionMessageEntry discriminator
//	  "id": "...",
//	  "parentId": "...",
//	  "timestamp": "...",
//	  "message": {
//	    "role": "bashExecution",  // inner discriminator
//	    "command": "ls",
//	    "output": "...",
//	    "exitCode": 0,
//	    "cancelled": false,
//	    "truncated": false,
//	    "fullOutputPath": "...",
//	    "timestamp": 1234567890,
//	    "excludeFromContext": false
//	  }
//	}
//
// In-memory we synthesize `Base.Type = "bash_execution"` after parsing
// so all existing exhaustive switches continue to work; this is a pure
// internal label, not on the wire. Older PiG sessions with top-level
// `type: "bash_execution"` still load through the legacy branch in
// UnmarshalJSON.
//
// `ExcludeFromContext` is set when the user invoked `!!cmd`: the entry
// persists for transcript purposes but is filtered out of agent
// hand-off so the LLM doesn't see it.
type BashExecutionEntry struct {
	SessionEntryBase
	Role               string `json:"-"` // always "bashExecution"; set on read
	Command            string `json:"-"`
	Output             string `json:"-"`
	ExitCode           *int   `json:"-"`
	Cancelled          bool   `json:"-"`
	Truncated          bool   `json:"-"`
	FullOutputPath     string `json:"-"`
	ExcludeFromContext bool   `json:"-"`
}

// BashExecutionMessage is the inner `message` payload upstream uses for
// bash entries. Mirrors messages.ts:29-43 byte-for-byte.
type BashExecutionMessage struct {
	Role               string `json:"role"` // "bashExecution"
	Command            string `json:"command"`
	Output             string `json:"output"`
	ExitCode           *int   `json:"exitCode"`
	Cancelled          bool   `json:"cancelled"`
	Truncated          bool   `json:"truncated"`
	FullOutputPath     string `json:"fullOutputPath,omitempty"`
	Timestamp          int64  `json:"timestamp,omitempty"`
	ExcludeFromContext bool   `json:"excludeFromContext,omitempty"`
}

// bashExecutionWireEntry is the on-disk wrapper (= upstream's SessionMessageEntry
// for bashExecution variants). Used solely for Marshal/Unmarshal.
type bashExecutionWireEntry struct {
	SessionEntryBase
	Message BashExecutionMessage `json:"message"`
}

// MarshalJSON serializes to upstream's wire shape (type:"message" + nested).
func (b BashExecutionEntry) MarshalJSON() ([]byte, error) {
	wire := bashExecutionWireEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "message", // wire discriminator (NOT "bash_execution")
			ID:        b.ID,
			ParentID:  b.ParentID,
			Timestamp: b.Timestamp,
		},
		Message: BashExecutionMessage{
			Role:               "bashExecution",
			Command:            b.Command,
			Output:             b.Output,
			ExitCode:           b.ExitCode,
			Cancelled:          b.Cancelled,
			Truncated:          b.Truncated,
			FullOutputPath:     b.FullOutputPath,
			ExcludeFromContext: b.ExcludeFromContext,
		},
	}
	return json.Marshal(wire)
}

// UnmarshalJSON accepts BOTH the new wire shape (type:"message" with
// inner role:"bashExecution") AND the legacy pig shape (type:"bash_execution"
// with flat top-level fields), so older session files still load.
func (b *BashExecutionEntry) UnmarshalJSON(data []byte) error {
	// Peek base to decide which path.
	var probe struct {
		Type    string          `json:"type"`
		ID      string          `json:"id"`
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	if probe.Type == "message" && len(probe.Message) > 0 {
		// New shape (upstream parity).
		var wire bashExecutionWireEntry
		if err := json.Unmarshal(data, &wire); err != nil {
			return err
		}
		b.SessionEntryBase = wire.SessionEntryBase
		b.Type = "bash_execution" // synthetic in-memory label
		b.Role = wire.Message.Role
		b.Command = wire.Message.Command
		b.Output = wire.Message.Output
		b.ExitCode = wire.Message.ExitCode
		b.Cancelled = wire.Message.Cancelled
		b.Truncated = wire.Message.Truncated
		b.FullOutputPath = wire.Message.FullOutputPath
		b.ExcludeFromContext = wire.Message.ExcludeFromContext
		return nil
	}
	// Legacy PiG shape with top-level fields.
	var legacy struct {
		SessionEntryBase
		Role               string `json:"role"`
		Command            string `json:"command"`
		Output             string `json:"output"`
		ExitCode           *int   `json:"exitCode"`
		Cancelled          bool   `json:"cancelled"`
		Truncated          bool   `json:"truncated"`
		FullOutputPath     string `json:"fullOutputPath,omitempty"`
		ExcludeFromContext bool   `json:"excludeFromContext,omitempty"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	b.SessionEntryBase = legacy.SessionEntryBase
	b.Type = "bash_execution"
	b.Role = legacy.Role
	b.Command = legacy.Command
	b.Output = legacy.Output
	b.ExitCode = legacy.ExitCode
	b.Cancelled = legacy.Cancelled
	b.Truncated = legacy.Truncated
	b.FullOutputPath = legacy.FullOutputPath
	b.ExcludeFromContext = legacy.ExcludeFromContext
	return nil
}

// ThinkingLevelEntry records a user-initiated thinking level change (Shift+Tab).
// Mirrors upstream appendThinkingLevelChange in agent-session.ts. Persisted as
// type="thinking_level_change" so the session tree can display [thinking: level].
type ThinkingLevelEntry struct {
	SessionEntryBase
	ThinkingLevel string `json:"thinkingLevel"`
}

// SessionEntry is the union of all entry types. We keep the raw JSON
// alongside parsed-base fields so unknown fields survive round-trips
// (forward-compat: future versions of upstream may add fields we don't
// know about; we must not silently drop them).
type SessionEntry struct {
	raw  json.RawMessage
	Base SessionEntryBase
}

func (e SessionEntry) MarshalJSON() ([]byte, error) { return e.raw, nil }

// NewSessionEntry constructs a SessionEntry from raw JSON and a pre-decoded base.
// Used primarily by tests and external packages that need to fabricate entries.
func NewSessionEntry(raw json.RawMessage, base SessionEntryBase) SessionEntry {
	return SessionEntry{raw: raw, Base: base}
}

// Raw returns the entry's on-disk JSON as immutable bytes.
func (e SessionEntry) Raw() json.RawMessage { return e.raw }

// AsMessage decodes the entry as a MessageEntry. Returns (zero, false)
// if the entry isn't type=message.
func (e SessionEntry) AsMessage() (MessageEntry, bool) {
	if e.Base.Type != "message" {
		return MessageEntry{}, false
	}
	var me MessageEntry
	if err := json.Unmarshal(e.raw, &me); err != nil {
		return MessageEntry{}, false
	}
	return me, true
}

// messageFor is AsMessage memoized on the session. Callers must clone the
// contained AgentMessage before handing it to a mutable pipeline.
func (s *Session) messageFor(e SessionEntry) (MessageEntry, bool) {
	if e.Base.Type != "message" {
		return MessageEntry{}, false
	}
	id := e.Base.ID
	if id == "" {
		return e.AsMessage()
	}
	s.msgMu.Lock()
	if cached, hit := s.msgCache[id]; hit {
		s.msgMu.Unlock()
		return cached.entry, cached.ok
	}
	s.msgMu.Unlock()

	me, ok := e.AsMessage()

	s.msgMu.Lock()
	if s.msgCache == nil {
		s.msgCache = make(map[string]parsedMessage)
	}
	s.msgCache[id] = parsedMessage{entry: me, ok: ok}
	if !ok {
		if s.undecodable == nil {
			s.undecodable = make(map[string]struct{})
		}
		s.undecodable[id] = struct{}{}
	}
	s.msgMu.Unlock()
	return me, ok
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
	// leafAppendMu makes reading the leaf and appending its child one step,
	// so a background appender (cache warming) cannot fork the active chain.
	leafAppendMu sync.Mutex
	header       SessionHeader
	entries      []SessionEntry
	// byID indexes entries for O(1) parent walks. Built on Load and
	// kept in sync by AppendEntry.
	byID   map[string]SessionEntry
	path   string
	leafID *string
	// flushed reports whether the session file on disk holds the
	// header + buffered entries. Mirrors upstream SessionManager.flushed:
	// a fresh session is not written to disk until the first assistant
	// message arrives, so abandoned sessions (opened, never answered)
	// leave no empty .jsonl file. Loaded/forked sessions start flushed.
	flushed bool
	// hasAssistant tracks whether any assistant message has been
	// appended; the gate that triggers the first flush (upstream
	// _persist hasAssistant check).
	hasAssistant bool

	// msgCache memoizes AsMessage by entry id. Entries are immutable and
	// append-only, so a parse never goes stale and needs no invalidation.
	// It removes the repeated json.Unmarshal that /tree navigation,
	// resume, and per-turn context building otherwise pay on every walk
	// of a long session's branch. Bounded by the session's own entry
	// count and freed when the session is dropped.
	//
	// Unparseable entries are cached too. An entry a build cannot decode
	// is decoded again by every later walk unless the failure is recorded,
	// and a failure costs a full json scan of the entry before it reports
	// the bad discriminator. Sessions written by a pig that emitted a
	// content-block type this build no longer accepts are entirely made of
	// such entries, which turned each /tree traversal into a re-scan of the
	// whole transcript.
	msgMu    sync.Mutex
	msgCache map[string]parsedMessage

	// undecodable holds the ids of message entries this build could not
	// decode. They are dropped from BuildContext, so the model would
	// otherwise be handed a transcript with turns silently missing;
	// callers surface the count so the loss is visible rather than
	// inferred from a model that has forgotten what it did.
	undecodable map[string]struct{}

	// stats is updated with the entry list while loading or appending so /session reads a bounded snapshot instead of reparsing durable history on the input loop.
	stats sessionAccountingAccumulator
}

// parsedMessage is a memoized AsMessage outcome, including a failed parse.
type parsedMessage struct {
	entry MessageEntry
	ok    bool
}

// NewSession creates a new in-memory session with an ISO millisecond header timestamp (not yet persisted).
func NewSession(id, cwd string) *Session {
	return &Session{
		header: SessionHeader{
			Type:      "session",
			Version:   CurrentSessionVersion,
			ID:        id,
			Timestamp: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
			CWD:       cwd,
		},
		byID: make(map[string]SessionEntry),
	}
}

func (s *Session) ID() string   { return s.header.ID }
func (s *Session) CWD() string  { return s.header.CWD }
func (s *Session) Path() string { return s.path }
func (s *Session) Header() SessionHeader {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.header
}

func (s *Session) ParentSession() string { return s.header.ParentSession }
func (s *Session) SetPath(p string)      { s.path = p }

// Entries returns a copy of all session entries (in append order).
func (s *Session) Entries() []SessionEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SessionEntry, len(s.entries))
	copy(out, s.entries)
	return out
}

// LatestCompactionTimestampMs returns the epoch-millisecond timestamp of the
// most recent compaction entry on the active branch (the path from the current
// leaf root-ward), and true when such an entry exists. Mirrors upstream
// getLatestCompactionEntry(getBranch()) (session-manager.ts:311), used by
// _checkCompaction to skip a stale pre-compaction usage reading that would
// otherwise re-trigger compaction on the first prompt after a resume.
func (s *Session) LatestCompactionTimestampMs() (int64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.leafID == nil {
		return 0, false
	}
	cur, ok := s.byID[*s.leafID]
	for ok {
		if cur.Base.Type == "compaction" {
			t, err := time.Parse(time.RFC3339Nano, cur.Base.Timestamp)
			if err != nil {
				return 0, false
			}
			return t.UnixMilli(), true
		}
		if cur.Base.ParentID == nil {
			return 0, false
		}
		cur, ok = s.byID[*cur.Base.ParentID]
	}
	return 0, false
}

// LeafID returns the ID of the current leaf entry (the parent of the
// next AppendEntry). nil = "no entries yet, next append is a root".
func (s *Session) LeafID() *string {
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

// AppendEntry appends an entry to the session (memory + file). The
// caller must have already filled in `id`, `parentId`, `timestamp`
// fields on the entry struct: but if `parentId` is nil-ptr AND
// session has a current leafID, we wire it up automatically so callers
// can skip the boilerplate.
//
// Returns an error if marshalling, parsing, or file write fails.
func (s *Session) AppendEntry(entry any) error {
	raw, err := marshalSessionLine(entry)
	if err != nil {
		return fmt.Errorf("session: marshal entry: %w", err)
	}
	var base SessionEntryBase
	if err := json.Unmarshal(raw, &base); err != nil {
		return fmt.Errorf("session: parse entry base: %w", err)
	}
	wireType := base.Type

	// Note: auto-wire of parentId from current leaf was intentionally removed.
	// All callers set parentId explicitly (from s.LeafID() or a target ID).
	// Auto-wiring conflated nil-as-"please infer" with nil-as-"root entry",
	// which caused branch_summary entries written at the root level to inherit
	// the current leaf as parent: the entry would land on the wrong branch.
	// See: AppendBranchSummary + navigateTree 3.2l bug fix.
	s.mu.Lock()
	base.Type = s.stats.add(raw, wireType)
	se := SessionEntry{raw: raw, Base: base}
	s.entries = append(s.entries, se)
	if s.byID == nil {
		s.byID = make(map[string]SessionEntry)
	}
	s.byID[base.ID] = se
	id := base.ID
	s.leafID = &id
	// Detect the first assistant message: the gate that flushes the
	// buffered session to disk (upstream _persist hasAssistant check).
	if base.Type == "message" && !s.hasAssistant {
		var probe struct {
			Message struct {
				Role string `json:"role"`
			} `json:"message"`
		}
		if json.Unmarshal(raw, &probe) == nil && probe.Message.Role == "assistant" {
			s.hasAssistant = true
		}
	}
	path := s.path
	hasAssistant := s.hasAssistant
	flushed := s.flushed
	// When the assistant gate just opened on an unflushed session,
	// snapshot header + all buffered entries for a single fresh write.
	var fullFlush [][]byte
	if path != "" && hasAssistant && !flushed {
		hdr, _ := marshalSessionLine(s.header)
		fullFlush = make([][]byte, 0, len(s.entries)+1)
		fullFlush = append(fullFlush, hdr)
		for _, e := range s.entries {
			fullFlush = append(fullFlush, e.raw)
		}
	}
	s.mu.Unlock()

	if path == "" {
		return nil
	}

	// Mirror upstream SessionManager._persist: a fresh session is not
	// written until an assistant message exists, so abandoned sessions
	// leave no empty file. Once an assistant arrives, flush header +
	// buffered entries; thereafter append each entry.
	if !hasAssistant {
		if !flushed {
			return nil // buffered only: nothing on disk yet
		}
		return appendSessionLine(path, raw) // resumed session, pre-assistant
	}
	if !flushed {
		if err := writeSessionLines(path, fullFlush); err != nil {
			return err
		}
		s.mu.Lock()
		s.flushed = true
		s.mu.Unlock()
		return nil
	}
	return appendSessionLine(path, raw)
}

// appendSessionLine appends a single JSONL record, creating the file if
// it does not exist (a resumed session always already exists).
func appendSessionLine(path string, raw []byte) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("session: open for append: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := fmt.Fprintf(f, "%s\n", raw); err != nil {
		return fmt.Errorf("session: append write: %w", err)
	}
	return nil
}

// writeSessionLines writes header + all buffered entries as a fresh file.
// Mirrors upstream's openSync(file, "wx") flush on the first assistant
// message.
func writeSessionLines(path string, lines [][]byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("session: open for flush: %w", err)
	}
	defer func() { _ = f.Close() }()
	for _, line := range lines {
		if _, err := fmt.Fprintf(f, "%s\n", line); err != nil {
			return fmt.Errorf("session: flush write: %w", err)
		}
	}
	return nil
}

// AppendMessage persists one upstream AgentMessage, generating a fresh entry id
// and timestamp and linking it to the current leaf.
func (s *Session) AppendMessage(msg agent.AgentMessage) (string, error) {
	// Custom messages round-trip as "custom_message" entries, not generic
	// "message" entries: messagesFromSession only reconstructs them from that
	// type. Routing them here keeps OnMessagePersist the single persistence
	// site for everything the agent delivers, so a queued custom message is
	// recorded at the point it reached the model rather than the point the
	// extension created it.
	if msg.Custom != nil {
		customType, _ := msg.Custom["customType"].(string)
		display, _ := msg.Custom["display"].(bool)
		return s.AppendCustomMessage(customType, msg.Custom["content"], display, msg.Custom["details"])
	}
	if _, err := json.Marshal(msg); err != nil {
		return "", fmt.Errorf("session: AppendMessage: %w", err)
	}
	id, err := generateEntryID()
	if err != nil {
		return "", err
	}
	s.leafAppendMu.Lock()
	defer s.leafAppendMu.Unlock()
	parent := s.LeafID()
	entry := MessageEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "message",
			ID:        id,
			ParentID:  parent,
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		},
		Message: msg,
	}
	if err := s.AppendEntry(entry); err != nil {
		return "", err
	}
	return id, nil
}

// AppendCustomMessage persists an extension-injected custom message as a
// "custom_message" entry and returns the new entry id.
func (s *Session) AppendCustomMessage(customType string, content any, display bool, details any) (string, error) {
	id, err := generateEntryID()
	if err != nil {
		return "", err
	}
	s.leafAppendMu.Lock()
	defer s.leafAppendMu.Unlock()
	entry := CustomMessageEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "custom_message",
			ID:        id,
			ParentID:  s.LeafID(),
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		},
		CustomType: customType,
		Content:    content,
		Display:    display,
		Details:    details,
	}
	if err := s.AppendEntry(entry); err != nil {
		return "", err
	}
	return id, nil
}

// AppendBashExecution persists a `!cmd` invocation.
// Returns the new entry id (the new leaf). The caller has already
// chosen excludeFromContext (true for `!!cmd`).
func (s *Session) AppendBashExecution(command, output string, exitCode *int, cancelled, truncated bool, fullOutputPath string, excludeFromContext bool) (string, error) {
	id, err := generateEntryID()
	if err != nil {
		return "", err
	}
	s.leafAppendMu.Lock()
	defer s.leafAppendMu.Unlock()
	parent := s.LeafID()
	entry := BashExecutionEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "bash_execution",
			ID:        id,
			ParentID:  parent,
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		},
		Role:               "bashExecution",
		Command:            command,
		Output:             output,
		ExitCode:           exitCode,
		Cancelled:          cancelled,
		Truncated:          truncated,
		FullOutputPath:     fullOutputPath,
		ExcludeFromContext: excludeFromContext,
	}
	if err := s.AppendEntry(entry); err != nil {
		return "", err
	}
	return id, nil
}

// EntryByID looks up a session entry by its hex ID. Returns false if
// the ID isn't present. Used by the chat layer to render markers (e.g.
// branch-summary chip on fork) without exporting the byID map.
func (s *Session) EntryByID(id string) (SessionEntry, bool) {
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
		if v.Base.Type != "session_info" {
			continue
		}
		var si SessionInfoEntry
		// upstream: coding-agent/src/core/session-manager.ts:parseSessionEntryLine
		if err := json.Unmarshal(v.raw, &si); err != nil {
			continue
		}
		return strings.TrimSpace(si.Name)
	}
	return ""
}

// AppendModelSwitch persists a `model_change` audit entry recording
// that the user switched models mid-session. Mirrors upstream
// session-manager.ts:appendModelChange(provider, modelId).
func (s *Session) AppendModelSwitch(provider, modelID, displayName string) error {
	id, err := generateEntryID()
	if err != nil {
		return err
	}
	s.leafAppendMu.Lock()
	defer s.leafAppendMu.Unlock()
	parent := s.LeafID()
	entry := ModelChangeEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "model_change",
			ID:        id,
			ParentID:  parent,
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		},
		Provider: provider,
		ModelID:  modelID,
	}
	// displayName is not part of the upstream schema for model_change
	// entries; we omit it from the persisted JSON to keep round-trip
	// parity. Status line resolves it from the registry on resume.
	_ = displayName
	return s.AppendEntry(entry)
}

// AppendThinkingLevelChange persists a thinking_level_change audit entry.
// Mirrors upstream agentSession.appendThinkingLevelChange (agent-session.ts).
// Called when the user cycles the thinking level via Shift+Tab.
func (s *Session) AppendThinkingLevelChange(level string) error {
	id, err := generateEntryID()
	if err != nil {
		return err
	}
	s.leafAppendMu.Lock()
	defer s.leafAppendMu.Unlock()
	entry := ThinkingLevelEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "thinking_level_change",
			ID:        id,
			ParentID:  s.LeafID(),
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		},
		ThinkingLevel: level,
	}
	return s.AppendEntry(entry)
}

// AppendUsage appends model-attributed usage that does not participate in LLM
// context and returns the appended entry. Mirrors upstream
// SessionManager.appendUsage (session-manager.ts).
func (s *Session) AppendUsage(kind, provider, model string, usage ai.Usage, note string) (UsageEntry, error) {
	id, err := generateEntryID()
	if err != nil {
		return UsageEntry{}, err
	}
	s.leafAppendMu.Lock()
	defer s.leafAppendMu.Unlock()
	entry := UsageEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "usage",
			ID:        id,
			ParentID:  s.LeafID(),
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
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
	id, err := generateEntryID()
	if err != nil {
		return "", err
	}
	s.leafAppendMu.Lock()
	defer s.leafAppendMu.Unlock()
	if firstKeptEntryID == "" {
		firstKeptEntryID = id
	}
	entry := CompactionEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "compaction",
			ID:        id,
			ParentID:  s.LeafID(),
			Timestamp: now.Format(time.RFC3339Nano),
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

// AppendBranchSummary starts a new branch at parentID with a summary of the
// abandoned path. parentID is the explicit parent: nil means the entry is a
// root-level node (no parent). fromId records the leaf being abandoned ("root"
// when there is none). The new entry becomes the leaf.
//
// Mirrors upstream branchWithSummary(branchFromId: string | null, ...) in
// session-manager.ts.
func (s *Session) AppendBranchSummary(parentID *string, summary string, details any, fromHook bool, usage *ai.Usage) (string, error) {
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
	id, err := generateEntryID()
	if err != nil {
		return "", err
	}
	entry := BranchSummaryEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "branch_summary",
			ID:        id,
			ParentID:  parentID, // nil = root-level entry
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
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

// AppendLabelChange writes a label entry for targetID. label=nil clears
// any existing label. Mirrors upstream appendLabelChange in session-manager.ts
// (line 1006).
func (s *Session) AppendLabelChange(targetID string, label *string) error {
	s.mu.RLock()
	_, ok := s.byID[targetID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("session: AppendLabelChange: entry %q not found", targetID)
	}
	id, err := generateEntryID()
	if err != nil {
		return err
	}
	s.leafAppendMu.Lock()
	defer s.leafAppendMu.Unlock()
	entry := LabelEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "label",
			ID:        id,
			ParentID:  s.LeafID(),
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		},
		TargetID: targetID,
		Label:    label,
	}
	return s.AppendEntry(entry)
}

// Fork moves the leaf pointer to forkFromID so the next AppendEntry
// becomes a sibling branch off that point. Existing entries are not
// touched: abandoned tail simply becomes an orphan subtree.
//
// Mirrors upstream `branch(id)`. Use SetLeafID(nil) for the "fork
// before first entry" case (root-reset).
func (s *Session) Fork(forkFromID string) error {
	s.mu.RLock()
	_, ok := s.byID[forkFromID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("session: Fork: entry %q not found", forkFromID)
	}
	id := forkFromID
	return s.SetLeafID(&id)
}

// Branch returns the path-to-root chain (in append order: root first,
// leaf last) ending at leafID. Returns nil if leafID isn't an entry.
func (s *Session) Branch(leafID string) []SessionEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pathToLocked(leafID)
}

// GetBranch returns the root-to-leaf path of the current leaf. Mirrors
// upstream SessionManager.getBranch() without an argument.
func (s *Session) GetBranch() []SessionEntry {
	leaf := s.LeafID()
	if leaf == nil {
		return nil
	}
	return s.Branch(*leaf)
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
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.leafID == nil {
		return SessionProjection{}
	}
	return buildSessionProjection(s.pathToLocked(*s.leafID), s.messageFor)
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
	cur := &leaf
	for cur != nil {
		path = append(path, *cur)
		if cur.Base.ParentID == nil {
			break
		}
		next, ok := s.byID[*cur.Base.ParentID]
		if !ok {
			break
		}
		cur = &next
	}
	slices.Reverse(path)
	return path
}

// SessionTreeNode is a defensive copy of the session's branch
// structure for the /tree overlay (follow-up).
type SessionTreeNode struct {
	Entry    SessionEntry
	Children []*SessionTreeNode
	Label    string
	// LabelTimestamp is the on-disk timestamp of the LabelEntry that
	// set Label, in upstream's ISO-8601-ish wire format. Empty when
	// no label is set. Used by the /tree picker to render `hh:mm`
	// next to the label when the user toggles label timestamps on
	// (mirrors upstream tree-selector.ts:678-682).
	LabelTimestamp string
}

// Tree builds a SessionTreeNode rooted at the (synthetic) root.
// Children sorted by timestamp (oldest first). When multiple entries
// have parentId=nil we group them under a synthetic root with empty
// Entry.
func (s *Session) Tree() *SessionTreeNode {
	s.mu.RLock()
	defer s.mu.RUnlock()

	root := &SessionTreeNode{}
	nodes := make(map[string]*SessionTreeNode, len(s.entries))
	for _, e := range s.entries {
		nodes[e.Base.ID] = &SessionTreeNode{Entry: e}
	}
	// Wire children. Resolve labels from LabelEntry rows. Last
	// LabelEntry for a given target wins (upstream behavior -
	// later entries overwrite earlier ones, including a nil Label
	// which clears the label).
	for _, e := range s.entries {
		if e.Base.Type == "label" {
			var le LabelEntry
			if err := json.Unmarshal(e.raw, &le); err == nil {
				if n, ok := nodes[le.TargetID]; ok {
					if le.Label != nil {
						n.Label = *le.Label
						n.LabelTimestamp = e.Base.Timestamp
					} else {
						n.Label = ""
						n.LabelTimestamp = ""
					}
				}
			}
		}
	}
	for _, e := range s.entries {
		n := nodes[e.Base.ID]
		if e.Base.ParentID == nil {
			root.Children = append(root.Children, n)
			continue
		}
		parent, ok := nodes[*e.Base.ParentID]
		if !ok {
			root.Children = append(root.Children, n)
			continue
		}
		parent.Children = append(parent.Children, n)
	}
	// Sort children by timestamp (chronological).
	var visit func(*SessionTreeNode)
	visit = func(n *SessionTreeNode) {
		slices.SortFunc(n.Children, func(a, b *SessionTreeNode) int {
			return cmp.Compare(a.Entry.Base.Timestamp, b.Entry.Base.Timestamp)
		})
		for _, c := range n.Children {
			visit(c)
		}
	}
	visit(root)
	return root
}

// ─── ID generators ────────────────────────────────────────────────────────────

// generateEntryID returns a 16-char hex random ID for an entry.
func generateEntryID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// generateSessionID returns a time-ordered UUIDv7 using Pi's shared generator.
func generateSessionID() (string, error) {
	return harnesssession.UUIDv7(nil)
}

// GenerateSessionID returns a time-ordered UUIDv7 for a new session.
func GenerateSessionID() (string, error) { return generateSessionID() }

// GenerateEntryID returns a random hexadecimal entry ID.
func GenerateEntryID() (string, error) { return generateEntryID() }

// RFC3339NowNano returns the current UTC time as a nanosecond-precision
// RFC3339 string: the timestamp format used in every session entry's
// header. Centralised here so coding/ doesn't have to duplicate the
// formatting choice.
func RFC3339NowNano() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// RenderTreeASCII renders a SessionTreeNode as an ASCII tree. Exported
// wrapper so the coding package can produce /tree output without
// duplicating the renderer.
func RenderTreeASCII(root *SessionTreeNode) string { return renderTreeASCII(root) }
