package codingagent

import (
	"bufio"
	"bytes"
	"cmp"
	"compress/flate"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"os"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// Pi's SessionManager keeps every parsed entry of the file in memory (session-manager.ts fileEntries/byId). A persisted Session keeps an index
// of every entry (its id, parent, type, message role and API, and where its line lies in the file) and holds decoded only the entries the
// active context needs: the latest compaction's kept range to the leaf, the entries that are not messages, and the newest assistant message of
// each API before the context. Every other entry is read back from its line on demand with the decoding the load used, so each accessor returns
// the values Pi's in-memory entries hold while the file's earlier lines are unchanged.

// entrySlot is one entry of the log, in append order.
type entrySlot struct {
	id string
	// entry is the decoded entry, nil when it is not resident.
	entry SessionEntry
	// ts is Date.parse of the entry's timestamp, the key getTree sorts siblings by.
	ts float64
	// kind names the entry's type and, for a message entry, its message.role and (for an assistant) message.api in the process's slot kinds
	// (internSlotKind), so the settings and model a branch carries can be found without decoding the messages that are not resident.
	kind uint32
	// facts locates an assistant message's /tree facts in Session.factArena, plus one; 0 until a tree selector or the open first reads them.
	facts uint32
	// parent locates the entry's parentId without a string of its own: noParent for a null or absent one, the index of a slot whose id it is,
	// or danglingParent when no slot had that id when the entry was added (Session.danglingParents holds it). A walk looks the id up in
	// byID when it walks, so the newest entry with that id is the parent, as in Pi's byId.
	parent int32
	// cold locates the members only some slots have (Session.coldPart), 0 when the slot has none of them.
	cold uint32
}

const (
	noParent       int32 = -1
	danglingParent int32 = -2
)

// slotCold is the part of a slot that a resident entry with no line in a file and no search text lacks, which is every entry
// of a session without a file until its first compaction: such an entry costs only its slot. Session.coldLocked gives a slot one.
type slotCold struct {
	// off and size locate the entry's line in the Session's backing file; off is -1 when the entry has no line there, and -2 minus the
	// record's offset in block when block holds it.
	off  int64
	size int32
	// searchAt locates, in the Session's searchTexts, the /tree search text without its branch label (tree-selector.ts getSearchableText),
	// JavaScript-lowercased, of a message or custom message that is not resident, so a search reads no entry; it is 0 when the slot has
	// none, which an open and an eviction leave for indexSearchTexts to fill on the first search.
	searchAt uint32
	// hash is recordHash of the record at off, or in block at at, which a read back verifies.
	hash uint32
	// block locates, in Session.blocks, plus one, the compressed records holding this entry's when it is not resident and has no line in a
	// file, at blockAt with length size; 0 for none.
	block uint32
}

// noCold is the cold part of a slot that has none.
var noCold = slotCold{off: -1}

// coldPart is the slot's cold part, or noCold, which is read-only. The caller holds s.mu.
func (s *Session) coldPart(slot *entrySlot) *slotCold {
	if slot.cold == 0 {
		return &noCold
	}
	chunk, at := coldChunkOf(slot.cold - 1)
	return &s.coldChunks[chunk][at]
}

// coldLocked returns slot's cold part, giving it one from the Session's chunks when it has none. A chunk is never moved, so a cold part
// stays where it is. The caller holds s.mu for writing.
func (s *Session) coldLocked(slot *entrySlot) *slotCold {
	if slot.cold == 0 {
		chunk, _ := coldChunkOf(s.coldCount)
		if chunk == len(s.coldChunks) {
			s.coldChunks = append(s.coldChunks, make([]slotCold, 0, coldChunkCap(chunk)))
		}
		s.coldChunks[chunk] = append(s.coldChunks[chunk], noCold)
		s.coldCount++
		slot.cold = s.coldCount
	}
	return s.coldPart(slot)
}

// The Session's cold parts are numbered in the order slots get them. The first coldSmallChunks chunks hold 8, 16, 32, ... parts, so a
// session with few costs little; every later chunk holds coldChunkSize.
// pig additive (D112): Pi keeps every entry as one object in its entries array; how the cold parts are chunked changes allocation, never which part a slot reads.
const (
	coldSmallChunks = 7
	coldSmallParts  = 8 * (1<<coldSmallChunks - 1)
	coldChunkSize   = 1024
)

// coldChunkOf locates cold part number ref: its chunk and its place in it.
func coldChunkOf(ref uint32) (chunk int, at uint32) {
	if ref < coldSmallParts {
		k := bits.Len32(ref/8+1) - 1
		return k, ref - 8*(1<<k-1)
	}
	ref -= coldSmallParts
	return coldSmallChunks + int(ref/coldChunkSize), ref % coldChunkSize
}

// coldChunkCap is the number of cold parts chunk holds.
func coldChunkCap(chunk int) int {
	if chunk < coldSmallChunks {
		return 8 << chunk
	}
	return coldChunkSize
}

// blockAt is the offset of the cold part's record in its block.
func (c *slotCold) blockAt() int64 { return -2 - c.off }

// recordBlock is the compressed records of the entries one eviction took out of a session without a file, back to back. They are written
// to the Session's spill file, so a long session without a file holds only where each block lies; data holds them instead when the spill
// file could not be created or written.
// pig additive (D112): Pi holds every session entry in memory; a session without a file keeps their records in an unnamed temporary file.
type recordBlock struct {
	data  []byte
	spill *sessionSnapshot
	off   int64
	size  int
	cache *blockCache
}

// packed returns the block's compressed records.
func (b *recordBlock) packed() ([]byte, error) {
	if b.spill == nil {
		return b.data, nil
	}
	buf := make([]byte, b.size)
	if _, err := b.spill.ReadAt(buf, b.off); err != nil {
		return nil, err
	}
	return buf, nil
}

// messageKind is a message's role and, for an assistant, its api.
type messageKind struct{ role, api string }

// slotKind is what a slot's kind code names: the entry's type and, for a message entry whose role is a string, its message kind.
type slotKind struct {
	typ    string
	msg    messageKind
	hasMsg bool
}

// slotKinds interns slot kinds for the process: a slot keeps a four-byte code where two unique handles took sixteen bytes. The kinds of a
// session are its entry types and the roles and apis of its messages, so the table stays as small as the distinct combinations seen.
var slotKinds struct {
	codes sync.Map // slotKind -> uint32
	mu    sync.Mutex
	table atomic.Pointer[[]slotKind]
}

// internSlotKind returns the code of kind.
func internSlotKind(kind slotKind) uint32 {
	if code, ok := slotKinds.codes.Load(kind); ok {
		return code.(uint32)
	}
	slotKinds.mu.Lock()
	defer slotKinds.mu.Unlock()
	if code, ok := slotKinds.codes.Load(kind); ok {
		return code.(uint32)
	}
	var table []slotKind
	if old := slotKinds.table.Load(); old != nil {
		table = *old
	}
	table = append(slices.Clip(table), kind)
	code := uint32(len(table) - 1)
	slotKinds.table.Store(&table)
	slotKinds.codes.Store(kind, code)
	return code
}

// kindOf is the kind the slot's code names.
func (slot *entrySlot) kindOf() slotKind { return (*slotKinds.table.Load())[slot.kind] }

// typ is the slot's entry type.
func (slot *entrySlot) typ() string { return slot.kindOf().typ }

// setMessageKind gives the slot of a message entry its message kind; ok false leaves it none.
func (slot *entrySlot) setMessageKind(msg messageKind, ok bool) {
	slot.kind = internSlotKind(slotKind{typ: slot.typ(), msg: msg, hasMsg: ok})
}

// role is the slot's message.role, "" for an entry that is no message or whose role is no string.
func (slot *entrySlot) role() string { return slot.kindOf().msg.role }

// api is the slot's assistant message.api.
func (slot *entrySlot) api() string { return slot.kindOf().msg.api }

const (
	typeMessage       = "message"
	typeCustomMessage = "custom_message"
	typeCompaction    = "compaction"
	typeLabel         = "label"
)

// evictable reports whether an entry of this type is resident only while the active context needs it: message bodies are the size of a session.
func (slot *entrySlot) evictable() bool {
	typ := slot.typ()
	return typ == typeMessage || typ == typeCustomMessage
}

// baseSlot is the slot of an entry with the given base members.
func baseSlot(base SessionEntryBase) entrySlot {
	return entrySlot{id: base.ID, parent: noParent, kind: internSlotKind(slotKind{typ: base.Type}), ts: ai.DateParse(base.Timestamp)}
}

// newSlot is the slot of an entry decoded or appended in memory.
func newSlot(entry SessionEntry) entrySlot {
	slot := baseSlot(entry.Base())
	slot.entry = entry
	slot.setMessageKind(messageKindOf(entry))
	return slot
}

// addSlotLocked appends slot, whose entry has parent as its parentId, to the log and indexes it. A compaction's firstKeptEntryId is read
// once, here. The caller holds s.mu for writing.
func (s *Session) addSlotLocked(slot entrySlot, parent *string, raw []byte) int {
	if slot.entry == nil {
		s.indexSearchTextLocked(&slot, raw)
	}
	return s.appendSlotLocked(slot, parent, raw)
}

// appendSlotLocked is addSlotLocked of a slot whose search text is indexed already. The caller holds s.mu for writing.
func (s *Session) appendSlotLocked(slot entrySlot, parent *string, raw []byte) int {
	idx := len(s.slots)
	slot.parent = noParent
	if parent != nil {
		if p, ok := s.slotOf(*parent); ok {
			slot.parent = int32(p)
		} else if *parent == slot.id {
			slot.parent = int32(idx)
		} else {
			slot.parent = danglingParent
			if s.danglingParents == nil {
				s.danglingParents = map[int32]string{}
			}
			s.danglingParents[int32(idx)] = *parent
		}
	}
	s.slots = append(s.slots, slot)
	s.byID.set(s.slots, idx)
	if slot.typ() == typeCompaction {
		var compaction struct {
			FirstKeptEntryID string `json:"firstKeptEntryId"`
		}
		_ = json.Unmarshal(raw, &compaction)
		if s.firstKept == nil {
			s.firstKept = map[int]string{}
		}
		s.firstKept[idx] = compaction.FirstKeptEntryID
	}
	if slot.typ() == typeLabel {
		var label LabelEntry
		if json.Unmarshal(raw, &label) == nil {
			if s.labels == nil {
				s.labels = map[string]entryLabel{}
			}
			if label.Label != nil && *label.Label != "" {
				s.labels[label.TargetID] = entryLabel{label: *label.Label, timestamp: label.Timestamp}
			} else {
				delete(s.labels, label.TargetID)
			}
		}
	}
	return idx
}

// entryLabel is an entry's label and the timestamp of the label entry that set it.
type entryLabel struct {
	label, timestamp string
}

// messageKindOf reads message.role and message.api of a message entry as the raw JSON holds them, as getSessionContextSettings reads the role.
func messageKindOf(entry SessionEntry) (messageKind, bool) {
	if entry.Base().Type != "message" {
		return messageKind{}, false
	}
	if message, ok := asMessage(entry); ok {
		kind := messageKind{role: message.Message.Role()}
		if message.Message.Assistant != nil {
			kind.api = string(message.Message.Assistant.API)
		}
		return kind, true
	}
	return rawMessageKind(entry.Raw())
}

// messageKindFrom is rawMessageKind from an already decoded role and raw api.
func messageKindFrom(role string, rawAPI json.RawMessage) (messageKind, bool) {
	var api string
	_ = json.Unmarshal(rawAPI, &api)
	return messageKind{role: role, api: api}, true
}

func rawMessageKind(raw []byte) (messageKind, bool) {
	var probe struct {
		Message struct {
			Role json.RawMessage `json:"role"`
			API  json.RawMessage `json:"api"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &probe) != nil {
		return messageKind{}, false
	}
	var r, a string
	if json.Unmarshal(probe.Message.Role, &r) != nil {
		return messageKind{}, false
	}
	_ = json.Unmarshal(probe.Message.API, &a)
	return messageKind{role: r, api: a}, true
}

// entryAtLocked returns the entry of slot i, reading it from the backing file when it is not resident. The caller holds s.mu.
func (s *Session) entryAtLocked(i int) SessionEntry {
	if entry := s.slots[i].entry; entry != nil {
		return entry
	}
	entries := s.readSlotsLocked([]int32{int32(i)})
	return entries[0]
}

// entriesAtLocked returns the entries of the given slots, in the given order, reading the ones that are not resident from the backing file in
// file order. The caller holds s.mu.
func (s *Session) entriesAtLocked(indexes []int32) []SessionEntry {
	out := make([]SessionEntry, len(indexes))
	var missing []int
	for k, i := range indexes {
		if entry := s.slots[i].entry; entry != nil {
			out[k] = entry
		} else {
			missing = append(missing, k)
		}
	}
	if len(missing) == 0 {
		return out
	}
	need := make([]int32, len(missing))
	for j, k := range missing {
		need[j] = indexes[k]
	}
	read := s.readSlotsLocked(need)
	for j, k := range missing {
		out[k] = read[j]
	}
	return out
}

// retainReadLocked makes resident the entries just read for indexes: a full-context read (a prompt) would otherwise read and decode every
// older message from the file again on each turn. What a turn reads is the context the window holds, so the resident set stays bounded by it.
// The caller holds s.mu for writing.
func (s *Session) retainReadLocked(indexes []int32, entries []SessionEntry) {
	var scratch []byte
	for k, idx := range indexes {
		slot := &s.slots[idx]
		if slot.entry != nil || !slot.evictable() || entries[k] == nil {
			continue
		}
		slot.entry = residentEntryOf(entries[k], &scratch)
		if cold := s.coldPart(slot); cold.searchAt != 0 {
			s.searchTexts.drop(cold.searchAt)
			cold.searchAt = 0
		}
	}
	s.compactSearchTextsLocked()
}

// residentEntryOf is the entry a slot keeps resident for entry, which was read back from its record. A message entry that encodes to its
// record again holds no record, as an appended one does: Pi holds the parsed object and no text of it (session-manager.ts
// loadEntriesFromFile), and every reader of the entry gets the record's bytes. Any other entry keeps a record of its own, not a slice of the
// buffer a run of records was read into, which would keep that whole buffer alive. scratch is reused from one entry to the next.
func residentEntryOf(entry SessionEntry, scratch *[]byte) SessionEntry {
	raw := entry.Raw()
	if raw == nil {
		return entry
	}
	message, ok := asMessage(entry)
	if !ok {
		return sessionentry.DecodeSessionEntry(bytes.Clone(raw))
	}
	bare := sessionentry.WithRecord(message, nil)
	if recordIsEncodingOf(bare, raw, scratch) {
		return bare
	}
	return sessionentry.WithRecord(message, bytes.Clone(raw))
}

// recordIsEncodingOf reports, without encoding it, that bare, a decoded message entry without its record, encodes to raw (MessageEntry
// AppendStringify): raw is JSON.stringify text holding exactly bare's base members, in the order the entry writes them, and then the message
// as its last member.
// The message decoded from JSON.stringify text encodes to that text: AgentMessage.UnmarshalJSON keeps the text it read whenever its fields
// would encode to anything else, and a message that keeps it writes it. A base member that needs an escape, or is no string, is left to keep
// the record.
func recordIsEncodingOf(bare MessageEntry, raw []byte, scratch *[]byte) bool {
	prefix := (*scratch)[:0]
	prefix = append(prefix, `{"type":`...)
	var ok bool
	if prefix, ok = appendPlainJSONString(prefix, bare.Type); !ok {
		return false
	}
	prefix = append(prefix, `,"id":`...)
	if prefix, ok = appendPlainJSONString(prefix, bare.ID); !ok {
		return false
	}
	prefix = append(prefix, `,"parentId":`...)
	if bare.ParentID == nil {
		prefix = append(prefix, "null"...)
	} else if prefix, ok = appendPlainJSONString(prefix, *bare.ParentID); !ok {
		return false
	}
	prefix = append(prefix, `,"timestamp":`...)
	if prefix, ok = appendPlainJSONString(prefix, bare.Timestamp); !ok {
		return false
	}
	prefix = append(prefix, `,"message":`...)
	*scratch = prefix
	return len(raw) > len(prefix)+1 && bytes.HasPrefix(raw, prefix) && raw[len(raw)-1] == '}' && skipJSONValue(raw, len(prefix)) == len(raw)-1 &&
		jsonstringify.IsCanonical(raw)
}

// appendPlainJSONString appends s quoted, and true, when JSON.stringify writes it with no escape: printable ASCII other than '"' and '\\'.
func appendPlainJSONString(dst []byte, s string) ([]byte, bool) {
	for i := range len(s) {
		if c := s[i]; c < 0x20 || c >= 0x7f || c == '"' || c == '\\' {
			return dst, false
		}
	}
	dst = append(dst, '"')
	dst = append(dst, s...)
	return append(dst, '"'), true
}

// readSlotsLocked reads the lines of slots that are not resident and decodes them as the load did. A line that no longer holds its entry (the
// file was rewritten under the Session) yields the entry's base members alone, and the failure is counted in staleReads. A large read decodes
// its lines on several goroutines, each a contiguous run of the file, joined before it returns.
func (s *Session) readSlotsLocked(indexes []int32) []SessionEntry {
	s.slotReads.Add(int64(len(indexes)))
	out := make([]SessionEntry, len(indexes))
	order := make([]int, len(indexes))
	for k := range order {
		order[k] = k
	}
	slices.SortFunc(order, func(a, b int) int {
		return cmp.Compare(s.coldPart(&s.slots[indexes[a]]).off, s.coldPart(&s.slots[indexes[b]]).off)
	})
	file, opened := s.openRecordsLocked()
	if opened != nil {
		defer func() { _ = opened.Close() }()
	}
	read := func(order []int) {
		reader := recordReader{file: file, blocks: s.blockList}
		var run runBuffer
		for _, k := range order {
			slot := &s.slots[indexes[k]]
			if slot.entry != nil {
				out[k] = slot.entry
				continue
			}
			var entry SessionEntry
			var record []byte
			var ok, owned bool
			if cold := s.coldPart(slot); cold.block == 0 && file != nil && cold.off >= 0 {
				// Records close together share one read; the record is a slice of that buffer, which the decoded entry may keep.
				record, owned = run.record(file, s, indexes, order, k)
				ok = owned
			}
			if !ok {
				record, ok = reader.record(s.coldPart(slot))
			}
			if ok {
				if !owned {
					record = bytes.Clone(record)
				}
				// An indexed line is never a header (the load skips records of type "session"), so it decodes as DecodeFileEntry decodes it.
				if decoded := sessionentry.DecodeSessionEntry(record); decoded.Base().ID == slot.id && decoded.Base().Type == slot.typ() {
					entry = decoded
				}
			}
			if entry == nil {
				s.staleReads.Add(1)
				entry = RawEntry{SessionEntryBase: SessionEntryBase{Type: slot.typ(), ID: slot.id, ParentID: s.parentPtrLocked(int(indexes[k]))}}
			}
			out[k] = entry
		}
	}
	parallelOver(order, read)
	return out
}

// parallelReadMinLines is the number of lines each goroutine of a parallel read decodes at least, so a small read stays on the caller.
// pig additive (D112): Pi holds every entry decoded (session-manager.ts _buildIndex); the split is a Go CPU choice and the result is in append order.
const parallelReadMinLines = 256

// allEntriesLocked returns every entry in append order, decoded. The caller holds s.mu.
func (s *Session) allEntriesLocked() []SessionEntry {
	return s.entriesAtLocked(s.allIndexesLocked())
}

// allIndexesLocked is the index of every slot, in append order. The caller holds s.mu.
func (s *Session) allIndexesLocked() []int32 {
	indexes := make([]int32, len(s.slots))
	for i := range indexes {
		indexes[i] = int32(i)
	}
	return indexes
}

// pathIndexesLocked returns the slot indexes of the root-to-leaf path ending at leafID (session-manager.ts getBranch walks parentId through
// byId). The caller holds s.mu.
func (s *Session) pathIndexesLocked(leafID string) []int32 {
	if !sessionentry.Truthy(leafID) {
		// getBranch starts at `startId ? byId.get(startId) : undefined`.
		return nil
	}
	if s.branchLeaf >= 0 && s.slots[s.branchLeaf].id == leafID && s.slotOrZero(leafID) == s.branchLeaf {
		return s.branch
	}
	return s.walkPathLocked(leafID)
}

func (s *Session) walkPathLocked(leafID string) []int32 {
	cur, ok := s.slotOf(leafID)
	if !ok {
		return nil
	}
	var path []int32
	for {
		path = append(path, int32(cur))
		parentID, has := s.parentIDLocked(cur)
		// The walks follow `current.parentId ? byId.get(current.parentId) : undefined`.
		if !has || !sessionentry.Truthy(parentID) {
			break
		}
		next, ok := s.slotOf(parentID)
		if !ok {
			break
		}
		cur = next
	}
	slices.Reverse(path)
	return path
}

// pathToLocked returns the root-to-leaf entry path ending at leafID. The caller holds s.mu.
func (s *Session) pathToLocked(leafID string) []SessionEntry {
	path := s.pathIndexesLocked(leafID)
	if path == nil {
		return nil
	}
	return s.entriesAtLocked(path)
}

// contextStartIn returns the index in path of the first entry the active context of path can contain: the latest compaction's first kept
// entry when it lies before the compaction (buildContextEntries' firstKeptEntryId scan), else the compaction, else 0.
func (s *Session) contextStartIn(path []int32) int {
	for c, p := range slices.Backward(path) {
		if s.slots[p].typ() != typeCompaction {
			continue
		}
		firstKept := s.firstKept[int(p)]
		for i := range path[:c] {
			if s.slots[path[i]].id == firstKept {
				return i
			}
		}
		return c
	}
	return 0
}

// contextPathIn returns the entries of path that the projection-level reads of the branch can see: every entry from the context start, every
// entry before it that is not a message, and before it the newest assistant message of each API. A path with no compaction is returned whole.
func (s *Session) contextPathIn(path []int32) []int32 {
	start := s.contextStartIn(path)
	if start == 0 {
		return path
	}
	newest := map[string]int{}
	for i := start - 1; i >= 0; i-- {
		slot := &s.slots[path[i]]
		if slot.role() == "assistant" {
			if _, seen := newest[slot.api()]; !seen {
				newest[slot.api()] = i
			}
		}
	}
	out := make([]int32, 0, len(path)-start+len(newest)+16)
	for i, idx := range path[:start] {
		slot := &s.slots[idx]
		if !slot.evictable() || (slot.role() == "assistant" && newest[slot.api()] == i) {
			out = append(out, idx)
		}
	}
	return append(out, path[start:]...)
}

// setBranchLocked caches the path of the current leaf, and its context path, after the leaf moved anywhere but to a new child of the cached
// leaf, and evicts what the new context does not need. The caller holds s.mu for writing.
func (s *Session) setBranchLocked() {
	s.contextReplacedLocked()
	s.branch, s.branchLeaf, s.ctxPath = nil, -1, nil
	if s.leafID != nil {
		if idx, ok := s.slotOf(*s.leafID); ok {
			s.branch, s.branchLeaf = s.walkPathLocked(*s.leafID), idx
			s.ctxPath = s.contextPathIn(s.branch)
		}
	}
	s.retainLocked()
}

// advanceBranchLocked extends the cached path by the slot just appended at idx as the new leaf. The caller holds s.mu for writing.
func (s *Session) advanceBranchLocked(idx int) {
	parent, has := s.parentIDLocked(idx)
	if s.branchLeaf < 0 || !has || !sessionentry.Truthy(parent) || s.slots[s.branchLeaf].id != parent || s.slotOrZero(parent) != s.branchLeaf || s.slotOrZero(s.slots[idx].id) != idx {
		s.setBranchLocked()
		return
	}
	s.branch = append(s.branch, int32(idx))
	s.branchLeaf = idx
	if s.slots[idx].typ() == typeCompaction {
		s.contextReplacedLocked()
		s.ctxPath = s.contextPathIn(s.branch)
		s.retainLocked()
		return
	}
	s.ctxPath = append(s.ctxPath, int32(idx))
}

// contextIndexesLocked returns contextPathIn of the path ending at leafID, from the cache for the current leaf. The caller holds s.mu.
func (s *Session) contextIndexesLocked(leafID string) []int32 {
	if s.branchLeaf >= 0 && s.slots[s.branchLeaf].id == leafID && s.slotOrZero(leafID) == s.branchLeaf {
		return s.ctxPath
	}
	path := s.walkPathLocked(leafID)
	if path == nil {
		return nil
	}
	return s.contextPathIn(path)
}

// retainTailEntries is how many of the newest context-path entries retainLocked keeps decoded.
const retainTailEntries = 256

// retainLocked keeps resident exactly the entries the active context needs and every entry that is not a message, reading the needed ones
// that are not resident from the backing file. Entries without a line in the file always stay resident. The caller holds s.mu for writing.
func (s *Session) retainLocked() {
	// Only the tail of the context path stays resident; older messages keep their lazy slots and are read from the file when first used (the transcript, a prompt, /tree), which yields the same decoded entries.
	keep := make(map[int32]struct{}, retainTailEntries)
	for _, idx := range s.ctxPath[max(0, len(s.ctxPath)-retainTailEntries):] {
		keep[idx] = struct{}{}
	}
	var load []int32
	var pack []packedRecord
	for i := range s.slots {
		slot := &s.slots[i]
		_, needed := keep[int32(i)]
		switch {
		case !slot.evictable():
		case needed && slot.entry == nil:
			load = append(load, int32(i))
		case !needed && slot.entry != nil && s.coldPart(slot).off < 0:
			// An entry the session holds without its record encodes it here, once, for its block. Its search text is read from the block on
			// the first search, as an open leaves it (indexSearchTexts): a session that is never searched keeps none.
			raw := slot.entry.Raw()
			s.noteTimestampLocked(i, slot.entry.Base().Timestamp)
			pack = append(pack, packedRecord{index: i, raw: raw})
		case !needed && slot.entry != nil:
			s.noteTimestampLocked(i, slot.entry.Base().Timestamp)
			slot.entry = nil
		}
	}
	var scratch []byte
	for k, entry := range s.readSlotsLocked(load) {
		slot := &s.slots[load[k]]
		slot.entry = residentEntryOf(entry, &scratch)
		// A resident entry's search text is read from the entry.
		if cold := s.coldPart(slot); cold.searchAt != 0 {
			s.searchTexts.drop(cold.searchAt)
			cold.searchAt = 0
		}
	}
	s.compactSearchTextsLocked()
	s.packLocked(pack)
}

// packedRecord is a resident entry that packLocked takes out of the decoded log, with its record.
type packedRecord struct {
	index int
	raw   []byte
}

// packLocked takes the given resident entries, which have no line in a file (a session without one, or entries not yet written), out of
// the decoded log and keeps their records in one compressed block. The caller holds s.mu for writing.
func (s *Session) packLocked(records []packedRecord) {
	if len(records) == 0 {
		return
	}
	size := 0
	for _, record := range records {
		size += len(record.raw)
	}
	plain := make([]byte, 0, size)
	for _, record := range records {
		plain = append(plain, record.raw...)
	}
	data, err := packRecord(plain)
	if err != nil {
		return
	}
	if s.blocks == nil {
		s.blocks = &blockCache{}
	}
	block := &recordBlock{data: data, cache: s.blocks}
	if s.spill == nil && !s.spillFailed {
		if file, err := anonymousFile(os.TempDir()); err == nil {
			s.spill = &sessionSnapshot{file: file}
		} else {
			s.spillFailed = true
		}
	}
	if s.spill != nil {
		if off, err := s.spill.addRaw(data); err == nil {
			block.data, block.spill, block.off, block.size = nil, s.spill, off, len(data)
		}
	}
	s.blockList = append(s.blockList, block)
	ref := uint32(len(s.blockList))
	at := 0
	for _, record := range records {
		slot := &s.slots[record.index]
		raw := record.raw
		size := len(raw)
		cold := s.coldLocked(slot)
		cold.block, cold.off, cold.size, cold.hash = ref, -2-int64(at), int32(size), uint32(recordHash(raw))
		slot.entry = nil
		at += size
	}
}

// recordReader reads the records of slots that are not resident: from their compressed block, or their line in the backing file. A reader
// with window set reads the file in windows of at least recordWindow bytes and serves the records within one from it, for a pass over many
// slots in file order.
type recordReader struct {
	file io.ReaderAt
	// blocks are the Session's compressed blocks (slotCold.block).
	blocks []*recordBlock
	window bool
	buf    []byte
	// at is the file offset of buf when window is set.
	at    int64
	block *recordBlock
	plain []byte
}

// recordWindow is the least a windowed recordReader reads from the file at once.
// pig additive (D112): Pi holds every entry in memory; a record that does not fit in the window is read whole, and every read returns the record's own bytes.
const recordWindow = 1 << 20

// record returns the record cold locates, valid until the next call; ok is false when it cannot be read.
func (r *recordReader) record(cold *slotCold) (record []byte, ok bool) {
	if cold.block != 0 {
		if block := r.blocks[cold.block-1]; r.block != block {
			packed, err := block.packed()
			if err != nil {
				return nil, false
			}
			plain, err := unpackRecord(packed)
			if err != nil {
				return nil, false
			}
			r.block, r.plain = block, plain
		}
		end := cold.blockAt() + int64(cold.size)
		if end > int64(len(r.plain)) {
			return nil, false
		}
		return r.plain[cold.blockAt():end], true
	}
	if r.file == nil || cold.off < 0 {
		return nil, false
	}
	if !r.window {
		r.buf = slices.Grow(r.buf[:0], int(cold.size))[:cold.size]
		if _, err := r.file.ReadAt(r.buf, cold.off); err != nil {
			return nil, false
		}
		return r.buf, true
	}
	end := cold.off + int64(cold.size)
	if cold.off < r.at || end > r.at+int64(len(r.buf)) {
		r.buf = slices.Grow(r.buf[:0], max(int(cold.size), recordWindow))
		n, err := r.file.ReadAt(r.buf[:cap(r.buf)], cold.off)
		if n < int(cold.size) || err != nil && !errors.Is(err, io.EOF) {
			r.buf = r.buf[:0]
			return nil, false
		}
		r.buf, r.at = r.buf[:n], cold.off
	}
	return r.buf[cold.off-r.at : end-r.at], true
}

// packRecord compresses the records of entries kept out of the decoded log.
func packRecord(raw []byte) ([]byte, error) {
	var out bytes.Buffer
	w, err := flate.NewWriter(&out, flate.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(raw); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return bytes.Clone(out.Bytes()), nil
}

// unpackRecord is the records packRecord compressed.
func unpackRecord(packed []byte) ([]byte, error) {
	return io.ReadAll(flate.NewReader(bytes.NewReader(packed)))
}

// lineReader yields the lines of a session file with the offset each starts at, splitting on "\n" and dropping a trailing "\r", as
// forEachJSONLLine does.
func forEachJSONLLineOffset(r io.Reader, fn func(line []byte, off int64) error) error {
	reader := bufio.NewReaderSize(r, 64*1024)
	var long []byte
	var off int64
	for {
		chunk, err := reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			long = append(long, chunk...)
			continue
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("session: read: %w", err)
		}
		line := chunk
		if len(long) > 0 {
			long = append(long, chunk...)
			line = long
		}
		start := off
		off += int64(len(line))
		if len(line) > 0 {
			line = bytes.TrimSuffix(bytes.TrimSuffix(line, []byte{'\n'}), []byte{'\r'})
			if fnErr := fn(line, start); fnErr != nil {
				return fnErr
			}
		}
		long = long[:0]
		if err != nil {
			return nil
		}
	}
}

// openRecordsLocked returns where the records of entries that are not resident are read: the snapshot, or else the backing file, opened;
// opened is the file the caller closes. file is nil when there is neither. The caller holds s.mu.
func (s *Session) openRecordsLocked() (file io.ReaderAt, opened *os.File) {
	if s.snapshot != nil {
		return s.snapshot, nil
	}
	if s.backing == "" {
		return nil, nil
	}
	opened, err := os.Open(s.backing)
	if err != nil {
		return nil, nil
	}
	return opened, opened
}

// recordSource opens where the records of entries that are not resident are read when a read first needs it. The caller holds s.mu.
type recordSource struct {
	s      *Session
	file   io.ReaderAt
	opened *os.File
	tried  bool
}

func (r *recordSource) get() io.ReaderAt {
	if !r.tried {
		r.tried = true
		r.file, r.opened = r.s.openRecordsLocked()
	}
	return r.file
}

func (r *recordSource) close() {
	if r.opened != nil {
		_ = r.opened.Close()
	}
}

// noteFlushedLocked records where the first write of a session file put each entry: lines are the header and then the entries of the first
// len(lines)-1 slots, also written to snapshot when it is not nil. The file becomes the Session's backing file, and snapshot its snapshot.
// The caller holds s.mu for writing.
func (s *Session) noteFlushedLocked(path string, lines [][]byte, snapshot *sessionSnapshot) {
	if s.backing != "" && s.backing != path || len(lines) == 0 || len(lines)-1 > len(s.slots) {
		if snapshot != nil {
			snapshot.close()
		}
		return
	}
	s.snapshot = snapshot
	off := int64(len(lines[0]) + 1)
	for i, line := range lines[1:] {
		slot := &s.slots[i]
		if s.coldPart(slot).off < 0 && slot.entry != nil && len(slot.entry.Raw()) == len(line) {
			s.locateLocked(slot, off, line)
		}
		off += int64(len(line) + 1)
	}
	s.backing = path
}

// noteAppendedLocked records where an append put entry's line raw, at off in the snapshot or else the backing file, when it went to the
// backing file and entry is still the one its id names. The caller holds s.mu for writing.
func (s *Session) noteAppendedLocked(path string, entry SessionEntry, off int64, raw []byte) {
	if path != s.backing || off < 0 {
		return
	}
	base := entry.Base()
	idx, ok := s.slotOf(base.ID)
	if !ok {
		return
	}
	if slot := &s.slots[idx]; s.coldPart(slot).off < 0 && slot.entry != nil && sameBase(slot.entry.Base(), base) {
		s.locateLocked(slot, off, raw)
	}
}

// sameBase reports whether a and b are the same base members.
func sameBase(a, b SessionEntryBase) bool {
	return a.Type == b.Type && a.ID == b.ID && a.Timestamp == b.Timestamp && (a.ParentID == nil) == (b.ParentID == nil) &&
		(a.ParentID == nil || *a.ParentID == *b.ParentID)
}

// KeptRangeOnBranch reports whether the current branch has no compaction, or its latest compaction's first kept entry lies before it on the
// branch: then the branch from the kept entry on is the same in GetContextBranch as in GetBranch.
func (s *Session) KeptRangeOnBranch() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	path := s.branch
	for c, p := range slices.Backward(path) {
		if s.slots[p].typ() == typeCompaction {
			return s.contextStartIn(path) < c
		}
	}
	return true
}

// CountEntriesOfType returns how many entries of the log have the given type, without reading their bodies.
func (s *Session) CountEntriesOfType(entryType string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	want := entryType
	n := 0
	for i := range s.slots {
		if s.slots[i].typ() == want {
			n++
		}
	}
	return n
}

// GetMessageEntriesWithRole returns the message entries whose message.role is role, in append order, reading only those entries.
func (s *Session) GetMessageEntriesWithRole(role string) []SessionEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var indexes []int32
	for i := range s.slots {
		if s.slots[i].typ() == typeMessage && s.slots[i].role() == role {
			indexes = append(indexes, int32(i))
		}
	}
	return s.entriesAtLocked(indexes)
}

// branchMessagesWithRole returns the message entries with the given message.role on the branch ending at leafID, in branch order, reading no
// other entry of the branch.
func (s *Session) branchMessagesWithRole(leafID, role string) []SessionEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var indexes []int32
	for _, i := range s.pathIndexesLocked(leafID) {
		if s.slots[i].typ() == typeMessage && s.slots[i].role() == role {
			indexes = append(indexes, i)
		}
	}
	return s.entriesAtLocked(indexes)
}

// RawEntriesPage returns the records of the entries from cursor on whose lines (with their newline) fit in maxBytes, at least one, the cursor
// after them and the number of entries (a nil page at the end); a cursor out of range starts at 0. Records of entries that are not resident are read without decoding.
// A record with no JSON is skipped but counted.
func (s *Session) RawEntriesPage(cursor, maxBytes int) (page []json.RawMessage, next, total int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total = len(s.slots)
	if cursor < 0 || cursor > total {
		cursor = 0
	}
	if cursor == total {
		return nil, cursor, total
	}
	page = make([]json.RawMessage, 0)
	records := recordSource{s: s}
	defer records.close()
	reader := recordReader{blocks: s.blockList}
	size := 0
	next = cursor
	for next < total {
		slot := &s.slots[next]
		var raw []byte
		if slot.entry != nil {
			raw = slot.entry.Raw()
		} else {
			reader.file = records.get()
			if record, ok := reader.record(s.coldPart(slot)); ok {
				raw = bytes.Clone(record)
			}
		}
		entryBytes := len(raw) + 1
		if len(page) > 0 && size+entryBytes > maxBytes {
			break
		}
		if len(raw) > 0 {
			page = append(page, json.RawMessage(raw))
			size += entryBytes
		}
		next++
	}
	return page, next, total
}

// getEntriesOfType returns the entries of one type in append order, reading only those entries.
func (s *Session) getEntriesOfType(entryType string) []SessionEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	want := entryType
	var indexes []int32
	for i := range s.slots {
		if s.slots[i].typ() == want {
			indexes = append(indexes, int32(i))
		}
	}
	return s.entriesAtLocked(indexes)
}

// getChildrenOf returns the entries whose parentId is parentID, or null when parentID is nil, in append order.
func (s *Session) getChildrenOf(parentID *string) []SessionEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var indexes []int32
	for i := range s.slots {
		parent, has := s.parentIDLocked(i)
		if (!has && parentID == nil) || (has && parentID != nil && parent == *parentID) {
			indexes = append(indexes, int32(i))
		}
	}
	return s.entriesAtLocked(indexes)
}

// searchTextOf is the lowercased search text without the label of the message or custom message line raw.
func searchTextOf(typ string, raw []byte) string {
	body, ok := searchBody{}, false
	if typ == "message" {
		body, ok = scanSearchBody(raw)
	}
	if !ok {
		body = treeSearchBody(RawEntry{SessionEntryBase: SessionEntryBase{Type: typ}}, raw)
	}
	return jsstring.ToLower(body.text)
}

// searchTextArena holds search texts back to back in chunks that are never moved or written again, so the index keeps no pointer per
// text and no slack per allocation.
type searchTextArena struct {
	chunks [][]byte
	// size is the bytes added, and dead the bytes of texts no slot refers to any more.
	size, dead uint64
}

const searchChunkBits = 18

// add stores text, which is at most maxIndexedSearchText bytes, after its length in two bytes, and returns its reference, or 0 when the
// arena has no reference left.
func (a *searchTextArena) add(text string) uint32 {
	last := len(a.chunks) - 1
	if last < 0 || len(a.chunks[last])+2+len(text) > cap(a.chunks[last]) {
		if len(a.chunks) >= 1<<(32-searchChunkBits) {
			// The references are spent; the slot's search reads its record.
			return 0
		}
		a.chunks = append(a.chunks, make([]byte, 0, 1<<searchChunkBits))
		last++
	}
	at := len(a.chunks[last])
	a.chunks[last] = append(a.chunks[last], byte(len(text)), byte(len(text)>>8))
	a.chunks[last] = append(a.chunks[last], text...)
	a.size += uint64(2 + len(text))
	return uint32(last)<<searchChunkBits | uint32(at) + 1
}

// stored is the stored bytes of the text at ref: its length and the text.
func (a *searchTextArena) stored(ref uint32) []byte {
	ref--
	chunk, at := a.chunks[ref>>searchChunkBits], ref&(1<<searchChunkBits-1)
	n := uint32(chunk[at]) | uint32(chunk[at+1])<<8
	return chunk[at : at+2+n]
}

func (a *searchTextArena) text(ref uint32) string { return string(a.stored(ref)[2:]) }

// drop notes that no slot refers to the text at ref any more.
func (a *searchTextArena) drop(ref uint32) { a.dead += uint64(len(a.stored(ref))) }

// maxIndexedSearchText bounds the search text the index keeps per entry. It holds a message's role and its 200 cut characters; a longer
// text (a bash command or a custom message's string content, which getSearchableText keeps whole) stays in the record, which a search reads.
// pig additive (D112): Pi computes the text from its in-memory entry on each search (tree-selector.ts getSearchableText); a longer text is read back, so a search finds the same.
const maxIndexedSearchText = 1 << 10

// indexSearchTextLocked gives a message or custom message slot its search text from its record raw, once, when it is at most
// maxIndexedSearchText bytes. The caller holds s.mu for writing.
func (s *Session) indexSearchTextLocked(slot *entrySlot, raw []byte) {
	if !slot.evictable() || s.coldPart(slot).searchAt != 0 {
		return
	}
	s.storeSearchTextLocked(slot, searchTextOf(slot.typ(), raw))
}

// indexSearchTexts gives every message and custom message that is not resident and has no search text its search text. It reads the
// records on several goroutines without holding s.mu, from copies of the slots' locations, and stores each text only when its slot still
// locates the record read, so an append during the read is not held up by it.
func (s *Session) indexSearchTexts() {
	type located struct {
		index int
		slot  entrySlot
		cold  slotCold
	}
	s.mu.RLock()
	var missing []located
	for i := range s.slots {
		if slot := &s.slots[i]; slot.entry == nil && slot.evictable() && slot.cold != 0 && s.coldPart(slot).searchAt == 0 {
			missing = append(missing, located{index: i, slot: *slot, cold: *s.coldPart(slot)})
		}
	}
	var file io.ReaderAt
	var opened *os.File
	if len(missing) > 0 {
		file, opened = s.openRecordsLocked()
	}
	// The blocks are read below without s.mu; a block is never changed once listed.
	blocks := s.blockList
	s.mu.RUnlock()
	if len(missing) == 0 {
		return
	}
	if opened != nil {
		defer func() { _ = opened.Close() }()
	}
	texts := make([]string, len(missing))
	read := make([]bool, len(missing))
	positions := make([]int, len(missing))
	for k := range positions {
		positions[k] = k
	}
	parallelOver(positions, func(part []int) {
		reader := recordReader{file: file, blocks: blocks, window: true}
		for _, k := range part {
			m := &missing[k]
			if record, ok := reader.record(&m.cold); ok {
				texts[k], read[k] = searchTextOf(m.slot.typ(), record), true
			}
		}
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, m := range missing {
		if !read[k] || m.index >= len(s.slots) {
			continue
		}
		slot := &s.slots[m.index]
		if cold := s.coldPart(slot); slot.id == m.slot.id && slot.entry == nil && slot.cold != 0 && cold.searchAt == 0 && cold.off == m.cold.off && cold.block == m.cold.block {
			s.storeSearchTextLocked(slot, texts[k])
		}
	}
}

// storeSearchTextLocked gives slot the search text text, when it is at most maxIndexedSearchText bytes. The caller holds s.mu for writing.
func (s *Session) storeSearchTextLocked(slot *entrySlot, text string) {
	if len(text) <= maxIndexedSearchText {
		s.coldLocked(slot).searchAt = s.searchTexts.add(text)
	}
}

// compactSearchTextsLocked copies the live search texts into a new arena once at least half of the arena's bytes are dead, so the arena
// stays within twice the texts the slots refer to. The caller holds s.mu for writing.
func (s *Session) compactSearchTextsLocked() {
	if s.searchTexts.dead == 0 || s.searchTexts.dead*2 < s.searchTexts.size {
		return
	}
	var live searchTextArena
	for i := range s.slots {
		if cold := s.coldPart(&s.slots[i]); cold.searchAt != 0 {
			cold.searchAt = live.add(s.searchTexts.text(cold.searchAt))
		}
	}
	s.searchTexts = live
}

// runBuffer joins the file reads of the records one worker decodes: a read covers the following records of its order that lie within
// fileRunGap of each other, up to fileRunMax bytes, and each record is handed out as a slice of it.
type runBuffer struct {
	buf  []byte
	base int64
	pos  map[int]int
}

// record returns the record of slot indexes[k] from the joined read, reading the next run when it is not covered; ok is false when the read failed.
func (r *runBuffer) record(file io.ReaderAt, s *Session, indexes []int32, order []int, k int) ([]byte, bool) {
	cold := s.coldPart(&s.slots[indexes[k]])
	from, to := cold.off, cold.off+int64(cold.size)
	if r.buf == nil || from < r.base || to > r.base+int64(len(r.buf)) {
		r.fill(file, s, indexes, order, k)
	}
	if r.buf == nil || from < r.base || to > r.base+int64(len(r.buf)) {
		return nil, false
	}
	return r.buf[from-r.base : to-r.base : to-r.base], true
}

// fill reads the run starting at record k; the records after it in order are sorted by offset, so the run is a prefix of them.
func (r *runBuffer) fill(file io.ReaderAt, s *Session, indexes []int32, order []int, k int) {
	r.buf = nil
	if r.pos == nil {
		r.pos = make(map[int]int, len(order))
		for p, j := range order {
			r.pos[j] = p
		}
	}
	first := s.coldPart(&s.slots[indexes[k]])
	r.base = first.off
	end := first.off + int64(first.size)
	for _, j := range order[r.pos[k]+1:] {
		c := s.coldPart(&s.slots[indexes[j]])
		if s.slots[indexes[j]].entry != nil || c.block != 0 || c.off < 0 {
			continue
		}
		if c.off-end > fileRunGap || c.off+int64(c.size)-r.base > fileRunMax {
			break
		}
		end = max(end, c.off+int64(c.size))
	}
	buf := make([]byte, end-r.base)
	if n, err := file.ReadAt(buf, r.base); n == len(buf) || err == nil {
		r.buf = buf
	}
}

// parentIDLocked is the parentId of the entry at slot i, and false for a null or absent one. The caller holds s.mu.
func (s *Session) parentIDLocked(i int) (string, bool) {
	switch p := s.slots[i].parent; p {
	case noParent:
		return "", false
	case danglingParent:
		return s.danglingParents[int32(i)], true
	default:
		return s.slots[p].id, true
	}
}

// parentPtrLocked is parentIDLocked as an entry base holds it: nil for a null or absent parentId. The caller holds s.mu.
func (s *Session) parentPtrLocked(i int) *string {
	if id, ok := s.parentIDLocked(i); ok {
		return &id
	}
	return nil
}
