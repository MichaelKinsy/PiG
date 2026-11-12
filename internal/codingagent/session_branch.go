// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"slices"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// branchedRecord is one retained entry of a branched session: the slot it comes from, its id, and the entry the new file chains it to.
type branchedRecord struct {
	slot int32
	id   string
	// parent is the id of the retained entry before it, nil for the first.
	parent *string
	// firstKept is the compaction's rewritten firstKeptEntryId, valid when rekeyed.
	firstKept string
	rekeyed   bool
}

// branchedPlan is what the root-to-leaf path of a branched session keeps, read from the index alone.
type branchedPlan struct {
	records []branchedRecord
	// hasConversation is Pi's _hasConversation of the retained entries: one is a user or assistant message.
	hasConversation bool
}

// planBranchedSession is the entries createBranchedSession keeps of the path to leafID, and false when that path is empty.
// Ports packages/coding-agent/src/core/session-manager.ts (createBranchedSession).
// Removing label entries rechains the retained path and moves compaction boundaries to the next retained entry.
func (s *Session) planBranchedSession(leafID string) (branchedPlan, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	path := s.pathIndexesLocked(leafID)
	if len(path) == 0 {
		return branchedPlan{}, false
	}
	plan := branchedPlan{records: make([]branchedRecord, 0, len(path))}
	replacements := make(map[string]string)
	var pending []string
	for _, idx := range path {
		slot := &s.slots[idx]
		slotID := s.idOf(slot)
		if slot.typ() == typeLabel {
			pending = append(pending, slotID)
			continue
		}
		for _, id := range pending {
			replacements[id] = slotID
		}
		pending = pending[:0]
		record := branchedRecord{slot: idx, id: slotID}
		if slot.typ() == typeCompaction {
			if first := s.firstKept[int(idx)]; first != slotID {
				record.firstKept, record.rekeyed = replacements[first]
			}
		}
		if slot.typ() == typeMessage {
			role := slot.role()
			plan.hasConversation = plan.hasConversation || role == "user" || role == "assistant"
		}
		plan.records = append(plan.records, record)
	}
	// The ids are final, so a record's parent can point at the one before it.
	for i := 1; i < len(plan.records); i++ {
		plan.records[i].parent = &plan.records[i-1].id
	}
	return plan, true
}

// writeBranchedSession passes emit the records of the branched session after its header, one at a time, in file order, each as the parts whose concatenation is the record: each retained entry's
// record with its parentId chained to the entry before it (and a compaction's firstKeptEntryId moved past the labels removed), then one
// fresh label entry for each labeled target on the path. A record that is not resident is read from its line alone, so no more than one record
// is held. The members the rewrite leaves alone keep the bytes the source has for them.
//
// emit also gets the entry the record is rewritten from when the source holds it decoded (nil for a record read from the file and for a fresh label) and the parent the record was
// given, so a caller that keeps the records in memory builds the clone's entries from the source's with rebasedMessage and decodes the others.
func (s *Session) writeBranchedSession(plan branchedPlan, emit func(entry SessionEntry, parent *string, parts ...[]byte) error) error {
	records := recordSource{s: s}
	defer records.close()
	var reader recordReader
	retained := make(map[string]struct{}, len(plan.records))
	var parent *string
	for _, record := range plan.records {
		raw, resident := s.branchedRecord(record.slot, &records, &reader)
		line, err := replaceJSONFieldParts(raw, "parentId", record.parent)
		if err != nil {
			return err
		}
		if record.rekeyed {
			if line, err = replaceJSONFieldParts(slices.Concat(line...), "firstKeptEntryId", record.firstKept); err != nil {
				return err
			}
		}
		if err := emit(resident, record.parent, line...); err != nil {
			return err
		}
		retained[record.id] = struct{}{}
		parent = &record.id
	}
	labels := make(map[string]LabelEntry)
	var order []string
	for _, entry := range s.getEntriesOfType(typeLabel) {
		var label LabelEntry
		if err := json.Unmarshal(entry.Raw(), &label); err != nil {
			return err
		}
		if label.Label == nil || *label.Label == "" {
			delete(labels, label.TargetID)
			order = slices.DeleteFunc(order, func(id string) bool { return id == label.TargetID })
			continue
		}
		if _, found := labels[label.TargetID]; !found {
			order = append(order, label.TargetID)
		}
		labels[label.TargetID] = label
	}
	for _, target := range order {
		if _, found := retained[target]; !found {
			continue
		}
		label := labels[target]
		id, err := generateID(retained)
		if err != nil {
			return err
		}
		label.ID = id
		label.ParentID = parent
		raw, err := marshalSessionLine(label)
		if err != nil {
			return err
		}
		if err := emit(nil, label.ParentID, raw); err != nil {
			return err
		}
		retained[id] = struct{}{}
		parent = &id
	}
	return nil
}

// branchedRecord is the record of slot idx and the entry the source holds decoded for it, nil when it is not resident: the resident entry's record, or the line read back when its hash is the one the index holds, else what a
// read of the entry decodes to (a line that no longer holds the entry is counted in staleReads). The bytes are valid until the next call.
func (s *Session) branchedRecord(idx int32, records *recordSource, reader *recordReader) ([]byte, SessionEntry) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	slot := &s.slots[idx]
	if resident := s.resident.get(int(idx)); resident != nil {
		return resident.Raw(), resident
	}
	cold := s.coldPart(slot)
	reader.blocks, reader.file = s.blockList, records.get()
	if record, ok := reader.record(cold); ok && recordHash(record) == uint64(cold.hash) {
		return record, nil
	}
	return s.readSlotsLocked([]int32{idx})[0].Raw(), nil
}

// rebasedMessage is the decode of raw, entry's record with its parentId set to parent, when entry is a decoded message entry whose parent
// raw names as a string: the same entry with that parent and raw as its record, since the rewrite changes no other member. It is nil for
// any other entry, which the caller decodes from raw. An in-memory clone decodes only the records this does not cover (an entry read back from the file, labels, model changes and so on).
func rebasedMessage(entry SessionEntry, parent *string, raw json.RawMessage) FileEntry {
	if entry == nil {
		return nil
	}
	message, ok := asMessage(entry)
	if !ok || (parent != nil && sessionentry.IsJSValue(*parent)) {
		return nil
	}
	if parent != nil {
		id := *parent
		parent = &id
	}
	message.ParentID = parent
	return sessionentry.WithRecord(message, raw)
}
