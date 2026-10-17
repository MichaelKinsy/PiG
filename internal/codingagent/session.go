package codingagent

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding"
	"encoding/hex"
	"fmt"
	"os"
	"reflect"
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

// LatestCompactionEntryOf is GetLatestCompactionEntry of the branch the projection was built from, read from the projection:
// buildContextEntries puts the newest compaction of the branch first, and a branch without one starts with an entry of another
// type, so the answer costs one entry and not a walk of the whole branch.
func LatestCompactionEntryOf(projection SessionProjection) *CompactionEntry {
	if len(projection.Entries) == 0 {
		return nil
	}
	return GetLatestCompactionEntry([]SessionEntry{projection.Entries[0].SourceEntry})
}

// asMessage is the entry as a MessageEntry, false for any other entry and for a message entry whose record does not decode. A
// sessionentry.LazyEntry is read and decoded; one whose record changed on disk panics with its *sessionentry.RecordChangedError.
func asMessage(e SessionEntry) (MessageEntry, bool) {
	return mustMessage(e)
}

// AsMessage is asMessage for the packages that read a Session's entries.
func AsMessage(e SessionEntry) (MessageEntry, bool) { return asMessage(e) }

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
	// slots holds every entry in append order; byID maps an id to its newest slot (session-manager.ts byId).
	slots []entrySlot
	byID  idIndex
	// danglingParents holds the parentId of each slot whose parent no slot had when it was added (entrySlot.parent).
	danglingParents map[int32]string
	// factArena holds the /tree facts of assistant messages (entrySlot.facts, storeFactsLocked).
	factArena []byte
	// coldChunks hold the slots' cold parts (entrySlot.cold), coldCount of them.
	coldChunks [][]slotCold
	coldCount  uint32
	// backing is the session file; "" keeps every entry resident. The slot offsets locate lines in snapshot, its private copy, or in the
	// backing file when it has none.
	backing  string
	snapshot *sessionSnapshot
	// branch is the slot path of the current leaf (branchLeaf, -1 when there is none) and ctxPath its contextPathIn, maintained as the leaf moves.
	branch     []int32
	branchLeaf int
	ctxPath    []int32
	// ctxGen changes whenever ctxPath is replaced rather than extended at its leaf; projView, the view projection of ctxPath's first
	// projCovered entries at generation projGen, is extended while it holds and built again when it moves. projMu guards the three, which
	// BuildSessionProjectionView updates under a read lock of mu; a writer of mu touches them without it.
	ctxGen      uint64
	projMu      sync.Mutex
	projView    *sessionProjector
	projGen     uint64
	projCovered int
	// firstKept holds each compaction slot's firstKeptEntryId.
	firstKept map[int]string
	// labels is the label each entry carries, from the latest label entry that targets it (session-manager.ts labelsById,
	// labelTimestampsById): a label entry with an empty or missing label clears it.
	labels map[string]entryLabel
	// oddTimestamps holds, by slot, the timestamps of evicted entries that are not the toISOString of their time value.
	oddTimestamps map[int]string
	// blocks holds the last compressed record block read.
	blocks *blockCache
	// blockList holds the compressed blocks eviction made, which slotCold.block names; a block holds the only copy of its records.
	blockList []*recordBlock
	// spill is the unnamed temporary file the blocks are written to, made by the first block; spillFailed records that it could not be
	// made, so later blocks stay in memory without retrying.
	spill       *sessionSnapshot
	spillFailed bool
	// handles holds, by slot, the LazyEntry whole-history reads return for a message that is not resident, made on the first read that
	// returns it; records is the source of the ones in the backing file. handleMu guards both under a read lock of mu.
	handleMu sync.Mutex
	handles  []sessionentry.LazyEntry
	records  *fileRecords
	// searchTexts holds the /tree search texts of the slots that have one.
	searchTexts searchTextArena
	// staleReads counts entries whose line no longer held them when read back.
	staleReads atomic.Int64
	// slotReads counts entries read back and decoded from their records (readSlotsLocked).
	slotReads  atomic.Int64
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

	// name is the name of the newest session_info entry that parses, maintained as entries are appended or loaded, so getSessionName, which every extension event's state carries, does not scan the log.
	name string

	// revision changes, under mu, whenever the entries or the leaf change, so a caller can reuse a value derived from the branch until the log moves.
	revision uint64
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
		branchLeaf: -1,
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
	return s.allEntriesLocked()
}

// entryHandles is GetEntries with each message entry that is not resident a sessionentry.LazyEntry, for this module's whole-history
// readers, which read entries through the sessionentry funnel.
func (s *Session) entryHandles() []SessionEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.entryHandlesLocked(s.allIndexesLocked())
}

// Revision identifies the current entries and leaf: it changes whenever either changes, so a value derived from the branch stays valid while the revision does.
func (s *Session) Revision() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.revision
}

// GetEntryCount returns the number of distinct session entry IDs, excluding the header, without copying entries like GetEntries.
// A loaded file that repeats an ID counts it once. Mirrors upstream getEntryCount, which returns byId.size (session-manager.ts:1511-1513).
func (s *Session) GetEntryCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.byID.n
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
		s.revision++
		s.setBranchLocked()
		return nil
	}
	if _, ok := s.slotOf(*id); !ok {
		return fmt.Errorf("session: SetLeafID: entry %q not found", *id)
	}
	cp := *id
	s.leafID = &cp
	s.revision++
	s.setBranchLocked()
	return nil
}

// AppendEntry appends a complete entry to memory and the session file. The caller supplies id, parentId and timestamp; a nil parent denotes a root and is never inferred from the current leaf.
// Returns an error if marshalling, parsing, or file write fails. Persistence errors expose Node's filesystem message while retaining the underlying Go error for errors.Is and errors.As.
func (s *Session) AppendEntry(entry any) error {
	raw, err := marshalSessionLine(entry)
	if err != nil {
		return fmt.Errorf("session: marshal entry: %w", err)
	}
	return s.appendEntryRecord(raw, false)
}

// appendEntryRecord adds the entry whose encoded record is raw. ownMessage reports that raw is the record this session just encoded from a message entry, so the decoded entry stands for it in the accounting and in the conversation gate and the record is not parsed again for them.
func (s *Session) appendEntryRecord(raw []byte, ownMessage bool) error {
	return s.appendEntryRecordOf(compactionWithoutRecord(sessionentry.DecodeSessionEntry(raw), raw), raw, ownMessage)
}

// compactionWithoutRecord is entry, decoded from raw, without its record when it is a compaction that encodes to raw again: a compaction stays
// resident for the session's life (a context read takes every entry before its kept range), and its record repeats its summary, escaped. Pi
// holds the parsed entry and no text of it. Every reader of the entry gets raw's bytes.
func compactionWithoutRecord(entry SessionEntry, raw []byte) SessionEntry {
	compaction, ok := entry.(CompactionEntry)
	if !ok {
		return entry
	}
	bare := compaction.WithoutRecord()
	if encoded, err := marshalSessionLine(bare); err != nil || !bytes.Equal(encoded, raw) {
		return entry
	}
	return bare
}

// appendEntryRecordOf is appendEntryRecord of the entry se whose record is raw.
func (s *Session) appendEntryRecordOf(se SessionEntry, raw []byte, ownMessage bool) error {
	base := se.Base()
	if isUndescribed(se) {
		var strict SessionEntryBase
		if err := json.Unmarshal(raw, &strict); err != nil {
			return fmt.Errorf("session: parse entry base: %w", err)
		}
	}
	decoded, isMessage := asMessage(se)
	isMessage = isMessage && ownMessage
	s.mu.Lock()
	if isMessage {
		s.stats.addMessageEntry(decoded.Message)
	} else {
		s.stats.add(raw, base.Type)
	}
	s.noteNameLocked(raw, base.Type)
	_, duplicate := s.slotOf(base.ID)
	s.addSlotLocked(newSlot(se), base.ParentID, raw)
	s.leafID = leafOf(base.ID)
	s.revision++
	if duplicate {
		s.setBranchLocked()
	} else {
		s.advanceBranchLocked(len(s.slots) - 1)
	}
	// A user or assistant message opens the gate that creates the file
	// (upstream _hasConversation, session-manager.ts:1166).
	if isMessage && !s.hasConversation {
		if role := decoded.Message.Role(); role == "user" || role == "assistant" {
			s.hasConversation = true
		}
	} else if base.Type == "message" && !s.hasConversation {
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
	return s.persistRecord(se, raw)
}

// Persist writes one entry the manager already holds to the session file, as upstream SessionManager._persist does (session-manager.ts:1172).
// An unflushed session is not written until it holds a user or assistant message, so setup entries alone leave no file. The first write creates the file with the header and every buffered entry; later calls append the entry.
// A session without a file persists nothing.
func (s *Session) Persist(entry SessionEntry) error {
	return s.persistRecord(entry, entry.Raw())
}

// persistRecord is Persist of entry, whose record is raw.
func (s *Session) persistRecord(entry SessionEntry, raw []byte) error {
	s.mu.Lock()
	path := s.path
	hasConversation := s.hasConversation
	flushed := s.flushed
	snapshot := s.snapshot
	// When the conversation gate just opened on an unflushed session,
	// snapshot header + all buffered entries for a single fresh write.
	var fullFlush [][]byte
	if path != "" && hasConversation && !flushed {
		hdr, _ := marshalSessionLine(s.header)
		fullFlush = make([][]byte, 0, len(s.slots)+1)
		fullFlush = append(fullFlush, hdr)
		for _, e := range s.allEntriesLocked() {
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
		snapshot, err := newSessionSnapshot(path)
		if err == nil {
			if _, err = snapshot.add(fullFlush...); err != nil {
				snapshot.close()
				snapshot = nil
			}
		}
		s.mu.Lock()
		s.flushed = true
		s.noteFlushedLocked(path, fullFlush, snapshot)
		s.mu.Unlock()
		return nil
	}
	end, err := appendSessionLine(path, raw)
	if err != nil {
		return err
	}
	// The line's place in the snapshot locates its record; without a snapshot, its place in the file does. An entry whose line is in neither
	// stays resident.
	off := end - int64(len(raw)) - 1
	if snapshot != nil {
		if off, err = snapshot.add(raw); err != nil {
			off = -1
		}
	}
	s.mu.Lock()
	if s.snapshot == snapshot {
		s.noteAppendedLocked(path, entry, off, raw)
	}
	s.mu.Unlock()
	return nil
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

// appendSessionLine appends a single JSONL record, creating the file if it does not exist (a resumed session always already exists), and
// returns the file offset after the record, -1 when it is unknown.
func appendSessionLine(path string, raw []byte) (int64, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return -1, sessionPersistenceError(err, "open", path)
	}
	defer func() { _ = f.Close() }()
	line := make([]byte, 0, len(raw)+1)
	line = append(append(line, raw...), '\n')
	if _, err := f.Write(line); err != nil {
		return -1, sessionPersistenceError(err, "write", "")
	}
	// The file's size after the write locates the line on every platform; the file position after an O_APPEND write is not specified on all.
	info, err := f.Stat()
	if err != nil {
		return -1, nil
	}
	return info.Size(), nil
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
	w := bufio.NewWriterSize(f, sessionWriteBufferFor(lines))
	for _, line := range lines {
		_, _ = w.Write(line)
		_ = w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		return sessionPersistenceError(err, "write", "")
	}
	return nil
}

// sessionWriteBuffer is the largest buffer a whole session file is written through.
// pig additive (D112): Pi writes the whole file with one writeFileSync; the buffer size changes how many writes carry the same bytes, never the bytes.
const sessionWriteBuffer = 64 << 10

// sessionWriteBufferFor is the buffer size for writing lines: their bytes and newlines, at most sessionWriteBuffer, so a file that fits
// is still one write and a new session's first records (a few hundred bytes) do not allocate 64 KB.
func sessionWriteBufferFor(lines [][]byte) int {
	size := 0
	for _, line := range lines {
		size += len(line) + 1
		if size >= sessionWriteBuffer {
			return sessionWriteBuffer
		}
	}
	return max(size, 1)
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
			//portlint:allow mapkeyorder the encoding is only decoded into BashExecutionMessage below, so member order cannot reach the record AppendBashExecution writes
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
	// The entry is encoded once: its record is what the session stores, counts, decodes and writes. A message that cannot be encoded is reported as the message's own error.
	raw, err := marshalSessionLine(entry)
	if err != nil {
		if _, messageErr := json.Marshal(msg); messageErr != nil {
			return "", fmt.Errorf("session: AppendMessage: %w", messageErr)
		}
		return "", fmt.Errorf("session: marshal entry: %w", err)
	}
	// An entry held as appended is held without its record: Pi's session holds the appended object and no encoding of it (session-manager.ts
	// appendMessage). The record is the encoding of entry's own value, so entry encodes to it again (MessageEntry.MarshalJSON), and every reader
	// of the entry, eviction and a first write of the file get the record's bytes.
	held, appended := heldMessageEntry(entry, raw)
	if appended {
		held = entry
	}
	if err := s.appendEntryRecordOf(held, raw, true); err != nil {
		return "", err
	}
	return id, nil
}

// heldMessageEntry returns the entry the session holds for entry, whose record is raw, and whether that is entry itself (with raw as its
// record). Pi's session holds the object the agent appended
// (session-manager.ts appendMessage); the record's decode is held instead when it differs from that message in Go: an absent usage, a
// streaming view. A message whose decoded form is proven, or a tool result whose decoded form is proven apart from its Details (a tool's typed
// details), needs no decode of the record just encoded.
func heldMessageEntry(entry MessageEntry, raw json.RawMessage) (SessionEntry, bool) {
	shared := sessionentry.WithRecord(entry, raw)
	var se SessionEntry
	if form, ok := entry.Message.DecodedForm(); ok {
		if decoded, ok := sessionentry.NewDecodedMessageEntry(entry.SessionEntryBase, form, raw); ok {
			se = decoded
		}
	} else if form, ok := decodedFormBesideDetails(entry.Message); ok {
		// The decode is form with the Details' JSON decoded into it, so it holds what entry holds exactly when form does with entry's Details
		// left out, which is decodesToAppended's rule for Details.
		if decoded, ok := sessionentry.NewDecodedMessageEntry(entry.SessionEntryBase, form, raw); ok {
			result := *entry.Message.ToolResult
			result.Details = nil
			stripped := entry
			stripped.Message.ToolResult = &result
			if reflect.DeepEqual(SessionEntry(decoded), SessionEntry(sessionentry.WithRecord(stripped, raw))) {
				return shared, true
			}
		}
	}
	if se == nil {
		se = sessionentry.DecodeSessionEntry(raw)
	}
	if decodesToAppended(se, shared) {
		return shared, true
	}
	return se, false
}

// decodedFormBesideDetails returns, for a tool result whose Details is a Go value that encodes as a JSON value other than null, the message that
// decoding its encoding yields with its Details left nil, and true when DecodedForm proves the rest. The decode holds that form with Details
// set to the decode of their JSON, which is not nil, and DetailsNull false: a tool result is decoded member by member, so the details member
// changes no other field, and its JSON is the encoder's valid output. A tool result's own decoded source sits on its ToolResult, which the copy
// keeps and DecodedForm refuses. It reports false for Details that are nil, a nil map, slice or pointer, or a value whose type, or pointer to
// it, has its own JSON or text encoding (which may write null).
func decodedFormBesideDetails(msg agent.AgentMessage) (agent.AgentMessage, bool) {
	if msg.ToolResult == nil || msg.User != nil || msg.Assistant != nil || msg.System != nil || msg.Custom != nil {
		return agent.AgentMessage{}, false
	}
	if !encodesAsNonNull(reflect.ValueOf(msg.ToolResult.Details)) {
		return agent.AgentMessage{}, false
	}
	stripped := *msg.ToolResult
	stripped.Details, stripped.DetailsNull = nil, false
	return agent.AgentMessage{ToolResult: &stripped}.DecodedForm()
}

var (
	jsonMarshalerType = reflect.TypeFor[json.Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
)

// encodesAsNonNull reports whether the JSON encoder writes value as an object, an array or a string: a struct, a non-nil map, slice or array,
// or a non-nil pointer to one, none of whose types (or pointers to them) carry their own JSON or text encoding.
func encodesAsNonNull(value reflect.Value) bool {
	for value.IsValid() {
		t := value.Type()
		p := reflect.PointerTo(t)
		if t.Implements(jsonMarshalerType) || p.Implements(jsonMarshalerType) || t.Implements(textMarshalerType) || p.Implements(textMarshalerType) {
			return false
		}
		switch value.Kind() {
		case reflect.Struct, reflect.Array:
			return true
		case reflect.Map, reflect.Slice:
			return !value.IsNil()
		case reflect.Pointer:
			if value.IsNil() {
				return false
			}
			value = value.Elem()
		default:
			return false
		}
	}
	return false
}

// decodesToAppended reports whether decoded, the decode of appended's record, holds what appended holds. A tool result's Details decode to
// their JSON whatever Go value the tool gave them (a tool's typed details, an extension's map), and the record was encoded from that value,
// so a tool result that differs from its decode only in Details holds the same JSON there.
func decodesToAppended(decoded SessionEntry, appended MessageEntry) bool {
	if reflect.DeepEqual(decoded, SessionEntry(appended)) {
		return true
	}
	message, ok := asMessage(decoded)
	if !ok || message.Message.ToolResult == nil || appended.Message.ToolResult == nil {
		return false
	}
	result := *appended.Message.ToolResult
	result.Details = message.Message.ToolResult.Details
	appended.Message.ToolResult = &result
	return reflect.DeepEqual(decoded, SessionEntry(appended))
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
	appended := s.entryAtLocked(s.slotOrZero(id))
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
	idx, ok := s.slotOf(id)
	if !ok {
		return nil, false
	}
	return s.entryAtLocked(idx), true
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
	return s.name
}

// noteNameLocked keeps name equal to the result of scanning the entries backwards for the first session_info that parses (session-manager.ts getSessionName): a later entry wins, so each appended or loaded one replaces it. s.mu must be held.
func (s *Session) noteNameLocked(raw []byte, entryType string) {
	if entryType != "session_info" {
		return
	}
	if name, ok := sessionInfoName(raw); ok {
		s.name = name
	}
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
	// compactionSystemMessage only reads the context's latest system message, so it reads the kept projection rather than a copy of the
	// whole context projected again.
	systemMessage, err := compactionSystemMessage(s.BuildSessionProjectionView().Messages, now.UnixMilli())
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
		_, parentFound = s.slotOf(*parentID)
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
	_, ok := s.slotOf(targetID)
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
	_, ok := s.slotOf(forkFromID)
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

// branchHandles is GetBranch with each message entry that is not resident a sessionentry.LazyEntry, for this module's whole-history
// readers, which read entries through the sessionentry funnel.
func (s *Session) branchHandles(fromID ...string) []SessionEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	leaf := s.leafID
	if len(fromID) > 0 {
		leaf = &fromID[0]
	}
	if leaf == nil {
		return nil
	}
	path := s.pathIndexesLocked(*leaf)
	if path == nil {
		return nil
	}
	return s.entryHandlesLocked(path)
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
	return buildSessionProjection(s.contextPathToLocked(*target), s.messageFor, true).Messages
}

// BuildSessionProjection returns the provenance-preserving projection of the
// current branch (session-manager.ts SessionManager.buildSessionProjection).
func (s *Session) BuildSessionProjection() SessionProjection {
	s.projectionCalls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leafID == nil {
		return SessionProjection{ThinkingLevel: "off"}
	}
	indexes := s.contextIndexesLocked(*s.leafID)
	if indexes == nil {
		return buildSessionProjection(nil, s.messageFor, true)
	}
	entries := s.entriesAtLocked(indexes)
	if s.branchLeaf >= 0 && s.slots[s.branchLeaf].id == *s.leafID && s.slotOrZero(*s.leafID) == s.branchLeaf {
		s.retainReadLocked(indexes, entries)
	}
	return buildSessionProjection(entries, s.messageFor, true)
}

// BuildSessionProjectionView is BuildSessionProjection with the entries' own messages in place of copies. The caller reads them and never modifies them or hands them to code that might: an estimate over a long conversation then costs a walk of the branch and not a deep copy of every message in it.
// The projection of the current leaf is kept and extended by the entries appended to it, so a turn projects only its own entries; a
// leaf move, a compaction or a context edit projects the branch again. Entries are immutable once appended, so the result equals a new
// projection of the branch.
func (s *Session) BuildSessionProjectionView() SessionProjection {
	s.projectionCalls.Add(1)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.leafID == nil {
		return SessionProjection{ThinkingLevel: "off"}
	}
	if s.branchLeaf < 0 || s.slots[s.branchLeaf].id != *s.leafID || s.slotOrZero(*s.leafID) != s.branchLeaf {
		return buildSessionProjection(s.contextPathToLocked(*s.leafID), s.messageFor, false)
	}
	s.projMu.Lock()
	defer s.projMu.Unlock()
	path := s.ctxPath
	if s.projView != nil && s.projGen == s.ctxGen && s.projCovered <= len(path) &&
		(s.projCovered == len(path) || s.projView.extend(s.entriesAtLocked(path[s.projCovered:]))) {
		s.projCovered = len(path)
		return s.projView.view()
	}
	s.projView = newSessionProjector(s.entriesAtLocked(path), s.messageFor, false, 0)
	s.projGen, s.projCovered = s.ctxGen, len(path)
	return s.projView.view()
}

// contextReplacedLocked notes that ctxPath was replaced, not extended at its leaf, and drops the projection kept for it. The caller holds
// s.mu for writing.
func (s *Session) contextReplacedLocked() {
	s.ctxGen++
	s.projView = nil
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
	return buildContextEntries(s.contextPathToLocked(*s.leafID), s.messageFor)
}

// GetContextBranch returns the branch ending at the current leaf as the projection-level reads see it: every entry from the start of the
// latest compaction's kept range, every earlier entry that is not a message, and before the kept range the newest assistant message of each
// API. buildSessionProjection, getSessionContextSettings, prepareCompaction, the newest assistant usage and the newest compaction or model
// selection read the same from it as from GetBranch, without reading history the context no longer holds. Nil when there is no leaf.
func (s *Session) GetContextBranch() []SessionEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.leafID == nil {
		return nil
	}
	return s.contextPathToLocked(*s.leafID)
}

// contextPathToLocked returns the entries of contextPathIn for the path ending at leafID. The caller holds s.mu.
func (s *Session) contextPathToLocked(leafID string) []SessionEntry {
	indexes := s.contextIndexesLocked(leafID)
	if indexes == nil {
		return nil
	}
	return s.entriesAtLocked(indexes)
}

// GetTree builds the session's tree and returns its roots (session-manager.ts getTree): an entry without a parent, one that
// is its own parent and an orphan whose parent is missing are roots, in entry order. Children are sorted oldest first by
// timestamp (a stable sort, so an unparsable timestamp keeps its place); the roots are not sorted. A label resolves from the
// latest label entry for its target.
func (s *Session) GetTree() []*SessionTreeNode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.treeOfLocked(s.allEntriesLocked())
}

// treeHandles is GetTree with each message entry that is not resident a sessionentry.LazyEntry, for this module's whole-history readers,
// which read entries through the sessionentry funnel.
func (s *Session) treeHandles() []*SessionTreeNode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.treeOfLocked(s.entryHandlesLocked(s.allIndexesLocked()))
}

// treeOfLocked is getTree over entries, which are every entry in append order. The caller holds s.mu.
func (s *Session) treeOfLocked(entries []SessionEntry) []*SessionTreeNode {
	// Pi keys nodes by id, so an id the log repeats is one node, the last entry's: byID holds the last slot of each id.
	all := make([]SessionTreeNode, len(entries))
	for i, e := range entries {
		all[i].Entry = e
	}
	for id, label := range s.labels {
		if i, ok := s.slotOf(id); ok {
			all[i].Label, all[i].LabelTimestamp = label.label, label.timestamp
		}
	}
	// Children are gathered in entry order into one array, start[p] to start[p+1] for parent p.
	node := make([]int32, len(entries))
	parent := make([]int32, len(entries))
	start := make([]int32, len(entries)+1)
	var roots []*SessionTreeNode
	for i := range entries {
		slot := &s.slots[i]
		node[i], parent[i] = int32(s.slotOrZero(slot.id)), -1
		parentID, has := s.parentIDLocked(i)
		if !has || parentID == slot.id {
			roots = append(roots, &all[node[i]])
			continue
		}
		p, ok := s.slotOf(parentID)
		if !ok {
			roots = append(roots, &all[node[i]])
			continue
		}
		parent[i] = int32(p)
		start[p+1]++
	}
	for p := range entries {
		start[p+1] += start[p]
	}
	children := make([]int32, start[len(entries)])
	fill := slices.Clone(start[:len(entries)])
	for i, p := range parent {
		if p >= 0 {
			children[fill[p]] = node[i]
			fill[p]++
		}
	}
	pointers := make([]*SessionTreeNode, len(children))
	for p := range entries {
		kids := children[start[p]:start[p+1]]
		if len(kids) == 0 {
			continue
		}
		// getTree sorts each node's children oldest first by Date.parse of their timestamps, keeping the order of unparsable ones.
		slices.SortStableFunc(kids, func(a, b int32) int { return cmpDates(s.slots[a].ts, s.slots[b].ts) })
		out := pointers[start[p]:start[p+1]:start[p+1]]
		for k, c := range kids {
			out[k] = &all[c]
		}
		all[p].Children = out
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
	return generateUniqueEntryID(func(id string) bool { _, exists := s.slotOf(id); return exists })
}

// entryIDPool serves the random bytes of entry IDs a pool at a time: every message appended to a Session takes an ID, and one getrandom
// system call per ID is the cost of the ID, not of the randomness. A pool is 64 IDs of four bytes, drawn from fill.
type entryIDPool struct {
	fill func([]byte) (int, error)
	mu   sync.Mutex
	buf  [256]byte
	next int
}

// read fills b, at most the pool's size, from the pool and refills the pool from fill when it runs dry.
func (pool *entryIDPool) read(b []byte) error {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if pool.next == 0 || pool.next+len(b) > len(pool.buf) {
		pool.next = 0
		if _, err := pool.fill(pool.buf[:]); err != nil {
			return err
		}
	}
	pool.next += copy(b, pool.buf[pool.next:])
	return nil
}

// entryIDRandom is the pool of this process's entry IDs, drawn from crypto/rand.
var entryIDRandom = entryIDPool{fill: rand.Read}

// systemRandomType is the type of crypto/rand.Reader as the process starts: the system source. A different type is a source installed by a
// test, which supplies the bytes of each ID itself, so the pool does not stand between the source and the ID.
var systemRandomType = reflect.TypeOf(rand.Reader)

// generateEntryID returns an eight-character lowercase hexadecimal entry ID.
func generateEntryID() (string, error) {
	var b [4]byte
	if reflect.TypeOf(rand.Reader) == systemRandomType {
		if err := entryIDRandom.read(b[:]); err != nil {
			return "", err
		}
	} else if _, err := rand.Read(b[:]); err != nil {
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
