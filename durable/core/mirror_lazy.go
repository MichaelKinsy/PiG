// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"slices"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/store"
)

// lazyState is how the mirror reads the records it did not load at open. With the host's synchronous read (ABI section
// 4.2, pig.read) open loads the live tasks and submissions only, as Pi opens with the live state: a terminal task or a
// settled submission is read when a step needs it, as Pi's Storage reads it then. The read takes no time the Session can
// observe (the hop model charges each Storage read whether the mirror held the record or not), and the mirror holds every
// record a commit of this handle wrote until the host has applied it, so a read never needs a commit the host has not
// applied.
type lazyState struct {
	read Reader // nil: every record was loaded at open
	// below is the ID below which every entry, task and submission the mirror lacks is in storage: the next ID at open,
	// then nextID at each release (releaseApplied). The mirror holds every record at or above it, and the live tasks
	// and submissions.
	below session.ID
	// storedBounds are the newest stored entry and head marker of the conversations whose bounds a read asked for,
	// sorted by conversation, kept current as entries move to storage, so the latest head marker is read once.
	storedBounds []storedBound
	// subsAll reports that every submission is in the mirror.
	subsAll bool
	// taskMiss and subMiss are IDs below below the store answered absent; requestMiss are (conversation, requestId)
	// pairs it answered absent.
	taskMiss, subMiss []session.ID
	requestMiss       []requestKey
	// err is the first failed read: the handle cannot answer from storage any more (handler.go turns it fatal).
	err error
}

// lazyRead runs one storage read for a record the mirror may lack. It returns false once a read failed.
func (m *mirror) lazyRead(sql uint16, params ...abi.Value) ([][]abi.Value, bool) {
	if m.lazy.err != nil {
		return nil, false
	}
	rows, err := m.lazy.read(sql, params)
	if err != nil {
		m.lazy.err = err
		return nil, false
	}
	return rows, true
}

// mayLack reports whether record id may be in storage but not in the mirror.
func (m *mirror) mayLack(id session.ID, all bool, miss []session.ID) bool {
	if m.lazy.read == nil || all || id <= 0 || id >= m.lazy.below {
		return false
	}
	_, known := slices.BinarySearch(miss, id)
	return !known
}

// loadTask reads task id when the mirror may lack it and returns its position.
func (m *mirror) loadTask(id session.ID) (int, bool) {
	if !m.mayLack(id, false, m.lazy.taskMiss) {
		return 0, false
	}
	rows, ok := m.lazyRead(store.SQLTaskByID, abi.Int(id))
	if !ok {
		return 0, false
	}
	var rec *jv.Object
	if len(rows) != 0 {
		rec = parseRaw(rows[0][0].Bytes)
	}
	if rec == nil {
		m.lazy.taskMiss = insertSorted(m.lazy.taskMiss, id)
		return 0, false
	}
	return m.putTask(id, headOfTask(rec), rec, rows[0][0].Bytes), true
}

// loadSubmission reads submission id when the mirror may lack it and returns its position.
func (m *mirror) loadSubmission(id session.ID) (int, bool) {
	if !m.mayLack(id, m.lazy.subsAll, m.lazy.subMiss) {
		return 0, false
	}
	rows, ok := m.lazyRead(store.SQLSubmissionByID, abi.Int(id))
	if !ok {
		return 0, false
	}
	var rec *jv.Object
	if len(rows) != 0 {
		rec = parseRaw(rows[0][0].Bytes)
	}
	if rec == nil {
		m.lazy.subMiss = insertSorted(m.lazy.subMiss, id)
		return 0, false
	}
	return m.addSubmission(id, rec, rows[0][0].Bytes), true
}

// addSubmission records a submission read from storage, with its request ID.
func (m *mirror) addSubmission(id session.ID, rec *jv.Object, raw []byte) int {
	at := m.putSubmission(id, headOfSubmission(rec), rec, raw)
	if r, ok := rec.Get("requestId"); ok {
		if request, ok := r.(string); ok {
			m.subs.setRequest(num(rec, "conversationId"), request, id)
		}
	}
	return at
}

// loadSubmissionByRequest reads conversation conv's submission with request ID request (SqliteStorage.submissionByRequest)
// when the mirror may lack it.
func (m *mirror) loadSubmissionByRequest(conv session.ID, request string) (session.ID, bool) {
	if m.lazy.read == nil || m.lazy.subsAll {
		return 0, false
	}
	key := requestKey{conv: conv, request: request}
	cmpKey := func(k, want requestKey) int { return cmpRequest(k, want.conv, want.request) }
	at, absent := slices.BinarySearchFunc(m.lazy.requestMiss, key, cmpKey)
	if absent {
		return 0, false
	}
	rows, ok := m.lazyRead(store.SQLSubmissionByRequest, abi.Int(conv), abi.TextBytes(jv.Append(nil, request)))
	if !ok {
		return 0, false
	}
	var rec *jv.Object
	if len(rows) != 0 {
		rec = parseRaw(rows[0][0].Bytes)
	}
	if rec == nil {
		m.lazy.requestMiss = slices.Insert(m.lazy.requestMiss, at, key)
		return 0, false
	}
	id := num(rec, "id")
	if _, held := m.subs.at(id); !held {
		m.addSubmission(id, rec, rows[0][0].Bytes)
	}
	return id, true
}

// scanTaskRows offers the tasks after id after (before it, descending) in ID order to visit, until it returns false: the
// ones the mirror holds (held, no row) and, with stored, the ones storage has and the mirror lacks (a row of the
// SQLTasksPage columns). A held task shadows the stored row of its ID: it may be newer. Storage is read a page at a time,
// and only as far as visit asks for.
func (m *mirror) scanTaskRows(stored, desc bool, after session.ID, visit func(id session.ID, held bool, row []abi.Value) bool) {
	ids := m.tasks.ids
	// pos is the next held task to offer: ids[pos:] ascending, ids[:pos] descending.
	var pos int
	cursor := after
	if desc {
		if cursor == 0 {
			cursor = maxSafeID
		}
		pos, _ = slices.BinarySearch(ids, cursor)
	} else {
		pos, _ = slices.BinarySearch(ids, cursor+1)
	}
	// heldBefore offers the held tasks that come before id (all of them for id 0) and reports whether the scan goes on.
	heldBefore := func(id session.ID) bool {
		for {
			if desc {
				if pos == 0 || ids[pos-1] < id {
					return true
				}
				pos--
				if !visit(ids[pos], true, nil) {
					return false
				}
			} else {
				if pos == len(ids) || (id != 0 && ids[pos] > id) {
					return true
				}
				pos++
				if !visit(ids[pos-1], true, nil) {
					return false
				}
			}
		}
	}
	if !stored {
		heldBefore(0)
		return
	}
	for {
		sql := store.SQLTasksPage
		if desc {
			sql = store.SQLTasksDesc
		}
		rows, ok := m.lazyRead(sql, abi.Int(cursor))
		if !ok {
			return
		}
		for _, r := range rows {
			id := int64(r[0].Number())
			if !heldBefore(id) {
				return
			}
			// A held task of this ID was offered by heldBefore or shadows the row.
			if m.tasks.has(id) {
				continue
			}
			if !visit(id, false, r) {
				return
			}
		}
		if len(rows) < store.PageRows {
			heldBefore(0)
			return
		}
		cursor = int64(rows[len(rows)-1][0].Number())
	}
}

// loadAllSubmissions reads every submission the mirror lacks, for a scan that may return settled submissions.
func (m *mirror) loadAllSubmissions() {
	if m.lazy.read == nil || m.lazy.subsAll {
		return
	}
	for after := int64(0); ; {
		rows, ok := m.lazyRead(store.SQLSubmissionsPage, abi.Int(after))
		if !ok {
			return
		}
		for _, r := range rows {
			id := int64(r[0].Number())
			if _, held := m.subs.at(id); !held {
				at := m.putSubmission(id, subHead{conv: int64(r[1].Number()), status: string(r[3].Bytes)}, nil, r[4].Bytes)
				if r[2].Kind != abi.ValueNull {
					// The request_id column is JSON.stringify(requestId).
					m.subs.setRequest(m.subs.heads[at].conv, unquoteColumn(r[2].Bytes), id)
				}
			}
			after = id
		}
		if len(rows) < store.PageRows {
			break
		}
	}
	m.lazy.subsAll, m.lazy.subMiss, m.lazy.requestMiss = true, nil, nil
}

// storedType is the record_ids type of an id the mirror does not hold; "" when storage has none.
func (m *mirror) storedType(id session.ID) string {
	if m.lazy.read == nil || id <= 0 || id >= m.storedBelow() {
		return ""
	}
	rows, ok := m.lazyRead(store.SQLRecordType, abi.Int(id))
	if !ok || len(rows) == 0 {
		return ""
	}
	return string(rows[0][0].Bytes)
}

// unquoteColumn is the string a JSON.stringify(string) column holds.
func unquoteColumn(b []byte) string {
	if v, err := jv.Parse(b); err == nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return string(b)
}

// storedBelow is the bound below which a record may be in storage and not in the mirror: lazy.below with a host read,
// 0 when the mirror loaded every record.
func (m *mirror) storedBelow() session.ID {
	if m.lazy.read == nil {
		return 0
	}
	return m.lazy.below
}

// heldKeep and heldSettled bound what the mirror keeps of the records this handle wrote while a task is live: the
// record text of its entries and the number of its terminal tasks and settled submissions, so the steps of a turn read
// the records the turn wrote from memory. A handle that lives for many turns releases them at the latest once a turn
// is over (releaseApplied), so what it holds after a turn does not grow with the turns it ran.
const (
	heldKeep    = 256 << 10
	heldSettled = 1024
)

// releaseApplied releases the entries, terminal tasks and settled submissions the mirror holds once no task is live, or
// once they exceed heldKeep or heldSettled, and storage has all of them: the mirror then holds the live set (live
// tasks and submissions, documents, conversations), as Pi's Session keeps no per-record state, and the next read of a
// released record goes to storage, which the hop model charges anyway. The caller knows every commit this handle wrote
// has been applied (handler.StepStart). Every ID is below nextID, and a later commit writes higher IDs (the line runs
// one transaction at a time and IDs are minted in it; checkGlobalIDs refuses an entry below the bound).
func (m *mirror) releaseApplied() {
	if m.lazy.read == nil || m.lazy.below == m.nextID && len(m.entries.byConv) == 0 && m.settledHeld() == 0 {
		return
	}
	if m.liveHeld() && m.entries.bytes < heldKeep && m.settledHeld() < heldSettled {
		return
	}
	m.releaseHeld()
}

// releaseHeld drops the held records the guards of releaseApplied found worth dropping. It is apart so that the function every
// step calls stays small: V8 compiles a hot function whole, and this body is the cold part of it.
//
//go:noinline
func (m *mirror) releaseHeld() {
	for i := range m.entries.byConv {
		e := &m.entries.byConv[i]
		b := m.storedBound(e.conv)
		if b == nil || len(e.ids) == 0 {
			continue
		}
		b.hasMax, b.maxID = true, e.ids[len(e.ids)-1]
		if n := len(e.headIDs); n > 0 {
			b.hasMarker, b.markerID, b.markerHead = true, e.headIDs[n-1], int64(numOf(m.entry(e.headIDs[n-1]), "head"))
		}
	}
	m.entries = entryTable{}
	m.tasks.keepLive()
	m.subs.keepLive()
	m.lazy.below = max(m.lazy.below, m.nextID)
	// A released record is read again from storage, so neither a scan nor an absent answer from before stands.
	m.lazy.subsAll = false
	m.lazy.taskMiss, m.lazy.subMiss, m.lazy.requestMiss = nil, nil, nil
}

// liveHeld reports whether the mirror holds a live task: a turn is in progress.
func (m *mirror) liveHeld() bool { return len(m.tasks.heads) > m.tasks.terminal }

// heldBytes is the record text the mirror holds of written entries, terminal tasks and settled submissions.
func (m *mirror) heldBytes() uint64 {
	n := uint64(m.entries.bytes)
	for _, r := range m.tasks.raws {
		n += uint64(len(r))
	}
	for _, r := range m.subs.raws {
		n += uint64(len(r))
	}
	return n
}

// settledHeld is the number of terminal tasks and settled submissions the mirror holds.
func (m *mirror) settledHeld() int { return m.tasks.terminal + m.subs.settled }

// storedBound is conversation c's newest stored entry and head marker, below lazy.below.
type storedBound struct {
	conv session.ID
	entryBound
}

// storedBound is the cached stored bounds of conversation c; nil when no read asked for them yet.
func (m *mirror) storedBound(c session.ID) *storedBound {
	b := m.lazy.storedBounds
	if i, ok := slices.BinarySearchFunc(b, c, func(e storedBound, c session.ID) int { return cmpID(e.conv, c) }); ok {
		return &b[i]
	}
	return nil
}

// entriesDescRows gives fn conversation c's entry IDs in [from, upper] newest first (from 0: no lower bound, upper 0: none):
// the ones the mirror holds, then the stored ones, which all precede them, read a page at a time with the page growing
// so a scan that stops after a few entries reads a few rows. It gives fn the stored record of an entry it reads from storage too (nil for one the
// mirror holds), so a caller that needs the records reads a page of them instead of one row an entry. first is the size of
// the first page (0: the ids only, from a page of 8, or want when the caller needs fewer); the page grows from it.
func (m *mirror) entriesDescRows(c, from, upper session.ID, first, want int, fn func(id session.ID, record []byte) bool) bool {
	held, _ := m.entries.held(c)
	i := len(held) - 1
	if upper != 0 {
		i = firstAbove(held, upper) - 1
	}
	for ; i >= 0; i-- {
		if from != 0 && held[i] < from {
			return true
		}
		if !fn(held[i], nil) {
			return false
		}
	}
	hi := m.storedBelow() - 1
	if upper != 0 && upper < hi {
		hi = upper
	}
	lo := max(from, 1)
	page := 8
	if first > 0 {
		page = min(first, store.PageRows)
	} else if want > 0 {
		page = min(want, page) // a scan that needs want entries reads no more on its first page
	}
	for ; hi >= lo; page = min(4*page, store.PageRows) {
		var rows [][]abi.Value
		var ok bool
		if first > 0 {
			rows, ok = m.lazyRead(store.SQLEntriesDesc, abi.Int(c), abi.Int(lo), abi.Int(hi), abi.Int(int64(page)))
		} else {
			rows, ok = m.lazyRead(store.SQLEntryIDsDesc, abi.Int(c), abi.Int(lo), abi.Int(hi), abi.Int(int64(page)))
		}
		if !ok {
			return false
		}
		for _, r := range rows {
			id := session.ID(r[0].Number())
			hi = id - 1
			var record []byte
			if first > 0 {
				record = r[2].Bytes
			}
			if !fn(id, record) {
				return false
			}
		}
		if len(rows) < page {
			return true
		}
	}
	return true
}

// entriesAsc gives fn conversation c's entries with ids in [from, upper] (from 0: no lower bound, upper 0: none), oldest first,
// with the stored record of each one it reads from storage (nil for one the mirror holds): the stored ones, a page of records
// at a time, then the held ones, which all follow them. It returns false when fn stopped or a read failed.
func (m *mirror) entriesAsc(c, from, upper session.ID, fn func(id session.ID, record []byte) bool) bool {
	hi := m.storedBelow() - 1
	if upper != 0 && upper < hi {
		hi = upper
	}
	for after := max(from, 1) - 1; after < hi; {
		rows, ok := m.lazyRead(store.SQLEntriesPage, abi.Int(c), abi.Int(after), abi.Int(hi))
		if !ok {
			return false
		}
		for _, r := range rows {
			id := session.ID(r[0].Number())
			after = id
			if !fn(id, r[2].Bytes) {
				return false
			}
		}
		if len(rows) < store.PageRows {
			break
		}
	}
	held, _ := m.entries.held(c)
	for i := firstAbove(held, from-1); i < len(held); i++ {
		if upper != 0 && held[i] > upper {
			break
		}
		if !fn(held[i], nil) {
			return false
		}
	}
	return true
}

// visibleRowsAsc is visibleRows oldest first: the oldest ancestor's entries up to its fork point, then each descendant's
// (entriesAsc).
func (m *mirror) visibleRowsAsc(conv, min, max session.ID, fn func(id session.ID, record []byte) bool) {
	type segment struct{ conv, upper session.ID }
	var segs []segment
	cur, upper := conv, max // upper 0: unbounded
	for {
		segs = append(segs, segment{cur, upper})
		p := sub(m.convs.get(cur), "parent")
		if p == nil {
			break
		}
		if pat := num(p, "at"); upper == 0 || pat < upper {
			upper = pat
		}
		if min != 0 && upper < min {
			break
		}
		cur = num(p, "conversationId")
	}
	for _, seg := range slices.Backward(segs) {
		if !m.entriesAsc(seg.conv, min, seg.upper, fn) {
			return
		}
	}
}

// entryBound is history's NeedBounds answer for one conversation and bound.
type entryBound struct {
	hasMax, hasMarker bool
	maxID, markerID   session.ID
	markerHead        int64
}

// entryBounds is conversation c's newest entry and newest head marker (with its head) at or below upper (0: no bound):
// from the entries the mirror holds, else from storage. ok is false when a read failed.
func (m *mirror) entryBounds(c, upper session.ID) (entryBound, bool) {
	var b entryBound
	if upper == 0 {
		upper = session.ID(history.SQLUnbounded)
	}
	ids, heads := m.entries.held(c)
	if i := firstAbove(ids, upper) - 1; i >= 0 {
		b.hasMax, b.maxID = true, ids[i]
	}
	if i := firstAbove(heads, upper) - 1; i >= 0 {
		b.hasMarker, b.markerID = true, heads[i]
		b.markerHead = int64(numOf(m.entry(heads[i]), "head"))
	}
	top := m.storedBelow() - 1
	hi := min(upper, top)
	if (b.hasMax && b.hasMarker) || hi < 1 {
		return b, true
	}
	var s entryBound
	if cb := m.storedBound(c); cb != nil && (!cb.hasMax || hi >= cb.maxID) {
		// The bound is at or above the conversation's newest stored entry: the cached answer is the answer.
		s = cb.entryBound
	} else {
		rows, ok := m.lazyRead(store.SQLEntryBounds, abi.Int(c), abi.Int(hi), abi.Int(c), abi.Int(hi), abi.Int(c), abi.Int(hi))
		if !ok {
			return b, false
		}
		if len(rows) != 0 {
			r := rows[0]
			if r[0].Kind != abi.ValueNull {
				s.hasMax, s.maxID = true, session.ID(r[0].Number())
			}
			if r[1].Kind != abi.ValueNull {
				s.hasMarker, s.markerID, s.markerHead = true, session.ID(r[1].Number()), int64(r[2].Number())
			}
		}
		if hi == top {
			i, _ := slices.BinarySearchFunc(m.lazy.storedBounds, c, func(e storedBound, c session.ID) int { return cmpID(e.conv, c) })
			m.lazy.storedBounds = slices.Insert(m.lazy.storedBounds, i, storedBound{c, s})
		}
	}
	if !b.hasMax {
		b.hasMax, b.maxID = s.hasMax, s.maxID
	}
	if !b.hasMarker {
		b.hasMarker, b.markerID, b.markerHead = s.hasMarker, s.markerID, s.markerHead
	}
	return b, true
}

// entryRaw is the stored record text and commit sequence of entry id: from the mirror, or read from storage.
func (m *mirror) entryRaw(id session.ID) ([]byte, session.Seq, bool) {
	if i, ok := m.entries.at(id); ok {
		return m.entries.raws[i], m.entries.seqs[i], true
	}
	if id <= 0 || id >= m.storedBelow() {
		return nil, 0, false
	}
	rows, ok := m.lazyRead(store.SQLEntryByID, abi.Int(id))
	if !ok || len(rows) == 0 {
		return nil, 0, false
	}
	return rows[0][2].Bytes, session.Seq(rows[0][1].Number()), true
}

// storedEntries reads conversation conv's stored entries with ids in [lo, hi] a page at a time, ascending, and gives
// each page's rows (id, commit_seq, record) to fn. It returns false when a read failed or fn did.
func (m *mirror) storedEntries(conv, lo, hi session.ID, fn func(rows [][]abi.Value) bool) bool {
	after := lo - 1
	for {
		rows, ok := m.lazyRead(store.SQLEntriesPage, abi.Int(conv), abi.Int(after), abi.Int(hi))
		if !ok || !fn(rows) {
			return false
		}
		if len(rows) < store.PageRows {
			return true
		}
		after = session.ID(rows[len(rows)-1][0].Number())
	}
}
