// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"slices"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Ports packages/coding-agent/src/core/session-manager.ts (createBranchedSession).
// Removing label entries rechains the retained path and moves compaction boundaries to the next retained entry. Resolved labels retain their original timestamps.
func branchedSessionEntries(source *Session, path []SessionEntry) ([]json.RawMessage, error) {
	records := make([]json.RawMessage, 0, len(path))
	retained := make(map[string]struct{})
	replacements := make(map[string]string)
	var pending []string
	var parent *string
	for _, entry := range path {
		if entry.Base().Type == "label" {
			pending = append(pending, entry.Base().ID)
			continue
		}
		for _, id := range pending {
			replacements[id] = entry.Base().ID
		}
		pending = pending[:0]
		raw, err := replaceJSONField(entry.Raw(), "parentId", parent)
		if err != nil {
			return nil, err
		}
		if entry.Base().Type == "compaction" {
			var comp CompactionEntry
			if err := json.Unmarshal(raw, &comp); err != nil {
				return nil, err
			}
			if replacement, found := replacements[comp.FirstKeptEntryID]; found && comp.FirstKeptEntryID != entry.Base().ID {
				raw, err = replaceJSONField(raw, "firstKeptEntryId", replacement)
				if err != nil {
					return nil, err
				}
			}
		}
		records = append(records, raw)
		retained[entry.Base().ID] = struct{}{}
		id := entry.Base().ID
		parent = &id
	}
	labels := make(map[string]LabelEntry)
	var order []string
	for _, entry := range source.GetEntries() {
		if entry.Base().Type != "label" {
			continue
		}
		var label LabelEntry
		if err := json.Unmarshal(entry.Raw(), &label); err != nil {
			return nil, err
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
			return nil, err
		}
		label.ID = id
		label.ParentID = parent
		raw, err := marshalSessionLine(label)
		if err != nil {
			return nil, err
		}
		records = append(records, raw)
		retained[id] = struct{}{}
		parent = &id
	}
	return records, nil
}
