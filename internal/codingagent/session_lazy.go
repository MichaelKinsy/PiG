package codingagent

import (
	"cmp"
	"errors"
	"fmt"
	"hash/maphash"
	"io"
	"math"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// A whole-history read (GetEntries, GetBranch, GetTree) returns each message entry that is not resident as a sessionentry.LazyEntry over its
// record: the index knows its base members, and the record is read, verified against the length and hash the index took, and decoded only
// when a caller reads it through the sessionentry funnel.

// recordSeed keys the hashes that verify a record read back; it lives as long as the process, as the index does.
var recordSeed = maphash.MakeSeed()

// recordHash is the hash of a record's bytes, without the line's newline.
func recordHash(record []byte) uint64 { return uint64(uint32(maphash.Bytes(recordSeed, record))) }

// locateLocked records that slot's record is line, at off in the backing file. The caller holds s.mu for writing.
func (s *Session) locateLocked(slot *entrySlot, off int64, line []byte) {
	s.locateHashedLocked(slot, off, len(line), recordHash(line))
}

// locateHashedLocked is locateLocked of a line of size bytes whose recordHash is hash.
func (s *Session) locateHashedLocked(slot *entrySlot, off int64, size int, hash uint64) {
	cold := s.coldLocked(slot)
	cold.off, cold.size, cold.hash, cold.block = off, int32(size), uint32(hash), 0
}

// errRecordChanged is the reason a read gives for a record whose bytes are not the indexed ones.
var errRecordChanged = errors.New("the bytes at its place are not the record that was read there")

// fileRecords reads records of one session file back, from its snapshot when it has one. Each must lie at its offset with its length, end
// its line (or the file), and hash as it did when it was indexed.
type fileRecords struct {
	path     string
	snapshot *sessionSnapshot
}

// fileRunGap and fileRunMax bound the coalescing of reads: records closer than fileRunGap share one read of at most fileRunMax bytes.
// pig additive (D112): Pi holds every record in memory; each record of a shared read is verified (length, newline, hash) as a lone read is.
const (
	fileRunGap = 64 << 10
	fileRunMax = 8 << 20
)

func (f *fileRecords) ReadRecords(ats []sessionentry.RecordAt, out []json.RawMessage) (int, error) {
	if f.snapshot != nil {
		return readRecordsAt(f.snapshot, ats, out)
	}
	file, err := os.Open(f.path)
	if err != nil {
		return -1, err
	}
	defer func() { _ = file.Close() }()
	return readRecordsAt(file, ats, out)
}

// readRecordsAt reads the records at ats from file into out, joining the reads of records that lie close together.
func readRecordsAt(file io.ReaderAt, ats []sessionentry.RecordAt, out []json.RawMessage) (int, error) {
	order := make([]int, len(ats))
	for k := range order {
		order[k] = k
	}
	slices.SortFunc(order, func(a, b int) int { return cmp.Compare(ats[a].Off, ats[b].Off) })
	for start := 0; start < len(order); {
		first := ats[order[start]]
		end := first.Off + int64(first.Size) + 1
		stop := start + 1
		for ; stop < len(order); stop++ {
			next := ats[order[stop]]
			nextEnd := max(end, next.Off+int64(next.Size)+1)
			if next.Off-end > fileRunGap || nextEnd-first.Off > fileRunMax {
				break
			}
			end = nextEnd
		}
		buf := make([]byte, end-first.Off)
		n, err := file.ReadAt(buf, first.Off)
		if err != nil && !errors.Is(err, io.EOF) {
			return order[start], err
		}
		buf = buf[:n]
		for _, k := range order[start:stop] {
			at := ats[k]
			from, to := at.Off-first.Off, at.Off-first.Off+int64(at.Size)
			// The record ends its line, or the file when its line has no newline.
			if to > int64(len(buf)) || to < int64(len(buf)) && buf[to] != '\n' && buf[to] != '\r' || recordHash(buf[from:to]) != at.Hash {
				return k, errRecordChanged
			}
			out[k] = buf[from:to:to]
		}
		start = stop
	}
	return -1, nil
}

// blockCache holds the records of the last compressed block a Session read, so reading the entries of one block one by one inflates it once.
type blockCache struct {
	mu    sync.Mutex
	block *recordBlock
	plain []byte
}

func (b *recordBlock) ReadRecords(ats []sessionentry.RecordAt, out []json.RawMessage) (int, error) {
	b.cache.mu.Lock()
	plain := b.cache.plain
	if b.cache.block != b {
		packed, err := b.packed()
		if err == nil {
			plain, err = unpackRecord(packed)
		}
		if err != nil {
			b.cache.mu.Unlock()
			return -1, err
		}
		b.cache.block, b.cache.plain = b, plain
	}
	b.cache.mu.Unlock()
	for k, at := range ats {
		end := at.Off + int64(at.Size)
		if end > int64(len(plain)) || recordHash(plain[at.Off:end]) != at.Hash {
			return k, errRecordChanged
		}
		out[k] = plain[at.Off:end:end]
	}
	return -1, nil
}

// dateISOString is Date.prototype.toISOString of a time value with a four-digit year, the form Pi writes every timestamp in; ok is false for
// any other value.
func dateISOString(ms float64) (string, bool) {
	var buf [24]byte
	if !formatDateISO(&buf, ms) {
		return "", false
	}
	return string(buf[:]), true
}

// formatDateISO writes dateISOString of ms into buf.
func formatDateISO(buf *[24]byte, ms float64) bool {
	if math.IsNaN(ms) || ms != math.Trunc(ms) || ms < -62167219200000 || ms > 253402300799999 {
		return false
	}
	t := time.UnixMilli(int64(ms)).UTC()
	year, month, day := t.Date()
	hour, minute, second := t.Clock()
	digits := func(at, width, n int) {
		for i := at + width - 1; i >= at; i-- {
			buf[i] = byte('0' + n%10)
			n /= 10
		}
	}
	digits(0, 4, year)
	digits(5, 2, int(month))
	digits(8, 2, day)
	digits(11, 2, hour)
	digits(14, 2, minute)
	digits(17, 2, second)
	digits(20, 3, t.Nanosecond()/1e6)
	buf[4], buf[7], buf[10], buf[13], buf[16], buf[19], buf[23] = '-', '-', 'T', ':', ':', '.', 'Z'
	return true
}

// noteTimestampLocked keeps the timestamp of slot i when it is not the one its time value writes, so a LazyEntry of the slot has the base the
// record decodes to. The caller holds s.mu for writing.
func (s *Session) noteTimestampLocked(i int, timestamp string) {
	var buf [24]byte
	if formatDateISO(&buf, s.slots[i].ts) && string(buf[:]) == timestamp {
		return
	}
	if s.oddTimestamps == nil {
		s.oddTimestamps = map[int]string{}
	}
	s.oddTimestamps[i] = timestamp
}

// lazyAtLocked reports whether a read returns slot i as a LazyEntry: a message that is not resident and whose record the index located.
func (s *Session) lazyAtLocked(i int) bool {
	slot := &s.slots[i]
	cold := s.coldPart(slot)
	return slot.typ() == typeMessage && slot.entry == nil && (cold.block != 0 || cold.off >= 0 && s.backing != "")
}

// entryHandlesLocked returns the entries of the given slots for a whole-history read: a resident entry as it is, a message that is not
// resident as a LazyEntry, and any other entry read from its record. The caller holds s.mu.
func (s *Session) entryHandlesLocked(indexes []int32) []SessionEntry {
	out := make([]SessionEntry, len(indexes))
	s.handleMu.Lock()
	if grow := len(s.slots) - len(s.handles); grow > 0 {
		s.handles = append(s.handles, make([]sessionentry.LazyEntry, grow)...)
	}
	var missing []int
	var need []int32
	for k, i := range indexes {
		slot := &s.slots[i]
		switch {
		case slot.entry != nil:
			out[k] = slot.entry
		case s.lazyAtLocked(int(i)):
			handle := &s.handles[i]
			if !handle.Indexed() {
				*handle = s.newHandleLocked(int(i))
			}
			out[k] = handle
		default:
			missing, need = append(missing, k), append(need, i)
		}
	}
	s.handleMu.Unlock()
	if len(need) > 0 {
		for j, entry := range s.readSlotsLocked(need) {
			out[missing[j]] = entry
		}
	}
	return out
}

// newHandleLocked is the LazyEntry of slot i, which lazyAtLocked accepts. The caller holds s.mu.
func (s *Session) newHandleLocked(i int) sessionentry.LazyEntry {
	slot := &s.slots[i]
	timestamp, odd := s.oddTimestamps[i]
	if !odd {
		timestamp, _ = dateISOString(slot.ts)
	}
	cold := s.coldPart(slot)
	at := sessionentry.RecordAt{Off: cold.off, Size: cold.size, Hash: uint64(cold.hash)}
	if cold.block != 0 {
		at.Off = cold.blockAt()
		return sessionentry.NewLazyEntry(slot.id, s.parentPtrLocked(i), timestamp, s.blockList[cold.block-1], at)
	}
	if s.records == nil || s.records.path != s.backing || s.records.snapshot != s.snapshot {
		// A handle keeps the source it was made with; a new backing file gets a new source.
		s.records = &fileRecords{path: s.backing, snapshot: s.snapshot}
	}
	return sessionentry.NewLazyEntry(slot.id, s.parentPtrLocked(i), timestamp, s.records, at)
}

// mustMessage is sessionentry.AsMessage for code that has no error to return. No LazyEntry reaches it: lazy entries leave the Session only
// by HandleReads and the handle reads, whose users (TestLazyEntriesLeaveOnlyByTheReviewedRoutes) pass them to the error-returning ReadRaws and
// MaterializeAll or read only their base members. A LazyEntry that did reach it with a changed record would panic with the
// *sessionentry.RecordChangedError.
func mustMessage(entry SessionEntry) (MessageEntry, bool) {
	message, ok, err := sessionentry.AsMessage(entry)
	if err != nil {
		panic(err)
	}
	return message, ok
}

// isUndescribed is sessionentry.IsUndescribed of an entry decoded from a record in hand, which is never a LazyEntry.
func isUndescribed(entry SessionEntry) bool {
	undescribed, err := sessionentry.IsUndescribed(entry)
	if err != nil {
		panic(fmt.Sprintf("session: a decoded entry is lazy: %v", err))
	}
	return undescribed
}

// materializeTree replaces each sessionentry.LazyEntry of the tree under nodes with its decoded entry, reading the records together, for
// code that reads every entry of a tree. A record that changed on disk panics with its *sessionentry.RecordChangedError.
func materializeTree(nodes []*SessionTreeNode) {
	var all []*SessionTreeNode
	var entries []SessionEntry
	stack := slices.Clone(nodes)
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = append(stack[:len(stack)-1], n.Children...)
		all, entries = append(all, n), append(entries, n.Entry)
	}
	materialized, err := sessionentry.MaterializeAll(entries)
	if err != nil {
		panic(err)
	}
	for k, n := range all {
		n.Entry = materialized[k]
	}
}

// HandleReads is a Session whose entry reads return each message entry that is not resident as a sessionentry.LazyEntry, for the readers in
// this module that read entries only through the error-returning sessionentry funnel (ReadRaws, MaterializeAll): RPC get_entries and
// get_tree, extension session reads and branch summary collection. It is never handed to code outside the module: the Session's own
// GetEntries, GetBranch, GetTree and GetEntry return decoded entries. TestLazyEntriesLeaveOnlyByTheReviewedRoutes keeps its uses, and those of
// the unexported handle reads, to the files that consume them so.
type HandleReads struct{ *Session }

// GetEntries is Session.GetEntries with lazy message entries.
func (r HandleReads) GetEntries() []SessionEntry { return r.entryHandles() }

// GetBranch is Session.GetBranch with lazy message entries.
func (r HandleReads) GetBranch(fromID ...string) []SessionEntry { return r.branchHandles(fromID...) }

// GetTree is Session.GetTree with lazy message entries.
func (r HandleReads) GetTree() []*SessionTreeNode { return r.treeHandles() }

// GetEntry is Session.GetEntry with a lazy message entry.
func (r HandleReads) GetEntry(id string) (SessionEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	idx, ok := r.slotOf(id)
	if !ok {
		return nil, false
	}
	return r.entryHandlesLocked([]int32{int32(idx)})[0], true
}
