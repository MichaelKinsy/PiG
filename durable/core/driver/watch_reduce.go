// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver

import (
	"encoding/json"
	"maps"
	"slices"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// A view or graph frame carries only the ops of one commit. A reducer turns them into the next typed value by changing what they
// name, instead of encoding the whole value to JSON, applying the ops to the generic form and decoding it all again: a commit
// costs what it changed. An op a reducer does not recognize makes it report false, and the mount takes the full conversion.

// reducer returns the value after ops, or false for ops it cannot apply to the typed value.
type reducer[T any] func(prev T, ops []durable.Op) (T, bool)

// pathOf is the path of an op as its segments, which are object keys here.
func pathOf(op durable.Op) ([]string, bool) {
	raw := delta.PathOf(op)
	if len(raw) == 0 {
		return nil, false
	}
	path := make([]string, len(raw))
	for i, segment := range raw {
		key, ok := segment.(string)
		if !ok {
			return nil, false
		}
		path[i] = key
	}
	return path, true
}

func intOf(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		i := int(n)
		return i, float64(i) == n
	}
	return 0, false
}

// reduceTaskGraph applies the graph ops: ["s", ["tasks", id], node], ["d", ["tasks", id]], ["s", ["tasks", id, "conversations"], ids] and
// ["s"|"d", ["tasks", id, "blocked"], reason]. It copies the task map once per frame, as Pi copies the graph's record.
func reduceTaskGraph(prev harness.TaskGraph, ops []durable.Op) (harness.TaskGraph, bool) {
	tasks := maps.Clone(prev.Tasks)
	if tasks == nil {
		tasks = map[string]harness.TaskGraphNode{}
	}
	for _, op := range ops {
		path, ok := pathOf(op)
		if !ok || path[0] != "tasks" || len(path) < 2 || len(path) > 3 {
			return prev, false
		}
		key := path[1]
		verb := delta.Verb(op)
		switch {
		case len(path) == 2 && verb == "d":
			delete(tasks, key)
		case len(path) == 2 && verb == "s" && len(op) == 3:
			node, err := typed[harness.TaskGraphNode](op[2])
			if err != nil {
				return prev, false
			}
			tasks[key] = node
		case len(path) == 3 && path[2] == "conversations" && verb == "s" && len(op) == 3:
			node, present := tasks[key]
			if !present {
				return prev, false
			}
			conversations, err := typed[[]durable.ConversationId](op[2])
			if err != nil {
				return prev, false
			}
			node.Conversations = conversations
			tasks[key] = node
		case len(path) == 3 && path[2] == "blocked" && (verb == "s" || verb == "d"):
			node, present := tasks[key]
			if !present {
				return prev, false
			}
			node.Blocked = ""
			if verb == "s" {
				reason, isString := op[2].(string)
				if !isString || len(op) != 3 {
					return prev, false
				}
				node.Blocked = durable.BlockedReason(reason)
			}
			tasks[key] = node
		default:
			return prev, false
		}
	}
	return harness.TaskGraph{Tasks: tasks}, true
}

// viewReducer returns the reducer of one view mount. It applies ["p", ["entries"], index, remove, items], ["s"|"d", ["docs", kind]]
// and the ops inside a document (["docs", kind, ...]). The entries of the value it last returned are a prefix of a slice it owns,
// so appending an entry (a commit's usual change) is amortized O(1); the slice a value carries has no spare capacity, so a holder
// that appends to it copies.
func viewReducer() reducer[harness.ConversationView] {
	var owned []durable.EntryRecord
	return func(prev harness.ConversationView, ops []durable.Op) (harness.ConversationView, bool) {
		next := prev
		entries := prev.Entries
		for _, op := range ops {
			path, ok := pathOf(op)
			if !ok {
				return prev, false
			}
			switch {
			case path[0] == "entries" && len(path) == 1 && delta.Verb(op) == "p" && len(op) == 5:
				index, okIndex := intOf(op[2])
				remove, okRemove := intOf(op[3])
				items, okItems := op[4].([]any)
				if !okIndex || !okRemove || !okItems || index < 0 || remove < 0 {
					return prev, false
				}
				index = min(index, len(entries)) // the applier clamps both
				remove = min(remove, len(entries)-index)
				records, err := typed[[]durable.EntryRecord](items)
				if err != nil {
					return prev, false
				}
				if index == len(entries) && remove == 0 {
					if len(owned) != len(entries) || (len(entries) > 0 && &owned[0] != &entries[0]) {
						owned = slices.Clone(entries)
					}
					owned = append(owned, records...)
					entries = owned[:len(owned):len(owned)]
				} else {
					owned = slices.Concat(entries[:index], records, entries[index+remove:])
					entries = owned[:len(owned):len(owned)]
				}
			case path[0] == "docs" && len(path) == 2 && delta.Verb(op) == "s" && len(op) == 3:
				document, ok := op[2].(durable.JsonObject)
				if !ok {
					return prev, false
				}
				next.Docs = harness.ViewDocsOf(next.Docs, path[1], document)
			case path[0] == "docs" && len(path) == 2 && delta.Verb(op) == "d":
				next.Docs = harness.ViewDocsWithout(next.Docs, path[1])
			case path[0] == "docs" && len(path) > 2 && delta.Verb(op) != "r":
				document, present := next.Docs.Get(path[1])
				if !present {
					return prev, false
				}
				inner := slices.Clone(op)
				inner[1] = delta.PathOf(op)[2:]
				applied, err := delta.ApplyImmutable(document, []delta.Op{inner})
				if err != nil {
					return prev, false
				}
				updated, ok := applied.(durable.JsonObject)
				if !ok {
					return prev, false
				}
				next.Docs = harness.ViewDocsOf(next.Docs, path[1], updated)
			default:
				return prev, false
			}
		}
		next.Entries = entries
		return next, true
	}
}

// fullConvert is the general path: encode the typed value, apply the ops to its generic form and convert the result.
func fullConvert[T any](prev T, ops []durable.Op, convert func(any) (T, error)) (T, bool) {
	raw, err := json.Marshal(prev)
	if err != nil {
		return prev, false
	}
	value, err := delta.DecodeJson(raw) //perf:ok cold fallback: graph and view watches take the ops-only paths above
	if err != nil {
		return prev, false
	}
	applied, err := delta.ApplyImmutable(value, ops)
	if err != nil {
		return prev, false
	}
	next, err := convert(applied)
	return next, err == nil
}
