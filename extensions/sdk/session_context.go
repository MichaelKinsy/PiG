package sdk

import (
	"errors"
	"maps"
)

// SessionLeaf is the leafId argument of [BuildSessionContext], which Pi reads three ways: omitted or undefined (the last entry), null (no path: an empty context) and an id
// (the entry to walk back from; an id no entry has, or the empty string, falls back to the last entry as omitted does).
type SessionLeaf struct {
	id   string
	none bool
}

// SessionLeafLast is an omitted leafId: the context ends at the last entry.
var SessionLeafLast = SessionLeaf{}

// SessionLeafNone is a null leafId: no entry is on the path, so the context is empty.
var SessionLeafNone = SessionLeaf{none: true}

// SessionLeafAt is a leafId that names an entry.
func SessionLeafAt(id string) SessionLeaf { return SessionLeaf{id: id} }

// idKey is a Map key for an entry id as JavaScript compares it: a string, number or boolean. Any other value is no key (an id that never matches).
func idKey(value any) (any, bool) {
	switch value.(type) {
	case string, float64, bool:
		return value, true
	}
	return nil, false
}

// BuildSessionContext is Pi's buildSessionContext (core/session-manager.ts, exported from index.ts): the model context of a session as the entries on the path from the
// leaf to the root, with compaction, branch summaries and context edits applied (Pi's buildSessionProjection without its provenance). The result has "messages" (the
// context's messages in order), "thinkingLevel" (the last thinking_level_change on the path, "off" without one) and "model" (the last model_change or assistant message's
// {provider, modelId}, nil without one). byID is an index of the entries by id that the walk reads instead of building one from entries; the leaf falls back to the last
// of entries. An entry whose parent chain loops ends the walk where it would revisit an entry, where Pi would never return. A message entry with a null or missing message
// is Pi's TypeError.
func BuildSessionContext(entries []map[string]any, leaf SessionLeaf, byID map[string]map[string]any) (map[string]any, error) {
	path := buildSessionPath(entries, leaf, byID)
	thinking, thinkingDefined, model, err := sessionContextSettings(path)
	if err != nil {
		return nil, err
	}
	contextEntries := buildContextEntries(path)
	edits := map[any]map[string]any{}
	for _, entry := range contextEntries {
		if entryType(entry) != "context_edit" {
			continue
		}
		if key, ok := idKey(entry["targetId"]); ok {
			edits[key] = entry
		}
	}
	messages := []any{}
	for i, entry := range contextEntries {
		if entryType(entry) == "compaction" && i > 0 {
			continue
		}
		projected, err := SessionEntryToContextMessages(entry)
		if err != nil {
			return nil, err
		}
		if key, ok := idKey(entry["id"]); ok {
			if edit, edited := edits[key]; edited {
				if projected, err = applyContextEdit(projected, edit); err != nil {
					return nil, err
				}
			}
		}
		messages = append(messages, projected...)
	}
	result := map[string]any{"messages": messages, "model": nil}
	if thinkingDefined {
		result["thinkingLevel"] = thinking
	}
	if model != nil {
		result["model"] = model
	}
	return result, nil
}

func buildSessionPath(entries []map[string]any, leaf SessionLeaf, byID map[string]map[string]any) []map[string]any {
	if leaf.none {
		return []map[string]any{}
	}
	// Pi indexes the entries once (later duplicates win) unless it is handed an index.
	var index map[any]map[string]any
	if byID == nil {
		index = make(map[any]map[string]any, len(entries))
		for _, entry := range entries {
			if id, ok := idKey(entry["id"]); ok {
				index[id] = entry
			}
		}
	}
	lookup := func(key any) (map[string]any, bool) {
		key, ok := idKey(key)
		if !ok {
			return nil, false
		}
		if byID != nil {
			id, isString := key.(string)
			if !isString {
				return nil, false
			}
			entry, found := byID[id]
			return entry, found
		}
		entry, found := index[key]
		return entry, found
	}
	var current map[string]any
	if leaf.id != "" {
		current, _ = lookup(leaf.id)
	}
	if current == nil {
		if len(entries) == 0 {
			return []map[string]any{}
		}
		current = entries[len(entries)-1]
	}
	var reversed []map[string]any
	// A parent chain longer than every entry there is has looped.
	limit := len(entries) + len(byID)
	for current != nil && len(reversed) <= limit {
		reversed = append(reversed, current)
		if !jsTruthy(current["parentId"]) {
			break
		}
		next, _ := lookup(current["parentId"])
		current = next
	}
	path := make([]map[string]any, len(reversed))
	for i, entry := range reversed {
		path[len(reversed)-1-i] = entry
	}
	return path
}

// messageRole is `entry.message.role` of a message entry: Pi's TypeError when the message is null or missing, and no role for a message that is not an object.
func messageRole(entry map[string]any) (string, error) {
	message, present := entry["message"]
	switch {
	case !present:
		return "", errors.New("TypeError: Cannot read properties of undefined (reading 'role')")
	case message == nil:
		return "", errors.New("TypeError: Cannot read properties of null (reading 'role')")
	}
	object, _ := message.(map[string]any)
	role, _ := object["role"].(string)
	return role, nil
}

func sessionContextSettings(path []map[string]any) (thinking any, thinkingDefined bool, model map[string]any, err error) {
	thinking, thinkingDefined = "off", true
	for _, entry := range path {
		switch entryType(entry) {
		case "thinking_level_change":
			thinking, thinkingDefined = entry["thinkingLevel"]
		case "model_change":
			model = definedMembers(entry, map[string]string{"provider": "provider", "modelId": "modelId"})
		case "message":
			role, roleErr := messageRole(entry)
			if roleErr != nil {
				return nil, false, nil, roleErr
			}
			if role == "assistant" {
				message := entry["message"].(map[string]any)
				model = definedMembers(message, map[string]string{"provider": "provider", "modelId": "model"})
			}
		}
	}
	return thinking, thinkingDefined, model, nil
}

// definedMembers builds {out: source[in]} for the members source has: JSON.stringify drops the ones Pi reads as undefined.
func definedMembers(source map[string]any, names map[string]string) map[string]any {
	out := map[string]any{}
	for outName, inName := range names {
		if value, present := source[inName]; present {
			out[outName] = value
		}
	}
	return out
}

// buildContextEntries is Pi's buildContextEntries: the newest compaction on the path, the entries it kept (from its firstKeptEntryId, minus system messages) and the
// entries after it.
func buildContextEntries(path []map[string]any) []map[string]any {
	var compaction map[string]any
	for _, entry := range path {
		if entryType(entry) == "compaction" {
			compaction = entry
		}
	}
	if compaction == nil {
		return path
	}
	compactionIndex := -1
	for i, entry := range path {
		if sameID(entry, compaction, "id", "id") {
			compactionIndex = i
			break
		}
	}
	if compactionIndex < 0 {
		return path
	}
	context := []map[string]any{compaction}
	foundFirstKept := false
	for _, entry := range path[:compactionIndex] {
		if sameID(entry, compaction, "id", "firstKeptEntryId") {
			foundFirstKept = true
		}
		if !foundFirstKept {
			continue
		}
		if role, _ := entryMessageRole(entry); entryType(entry) == "message" && role == "system" {
			continue
		}
		context = append(context, entry)
	}
	return append(context, path[compactionIndex+1:]...)
}

func entryMessageRole(entry map[string]any) (string, bool) {
	message, _ := entry["message"].(map[string]any)
	role, ok := message["role"].(string)
	return role, ok
}

// sameID is `a[aKey] === b[bKey]` for the values a decoded entry holds: undefined (a missing member) equals only undefined, and an object or array never equals another.
func sameID(a, b map[string]any, aKey, bKey string) bool {
	left, leftPresent := a[aKey]
	right, rightPresent := b[bKey]
	if !leftPresent || !rightPresent {
		return leftPresent == rightPresent
	}
	leftKey, leftOK := idKey(left)
	rightKey, rightOK := idKey(right)
	if left == nil && right == nil {
		return true
	}
	return leftOK && rightOK && leftKey == rightKey
}

// applyContextEdit is Pi's projectContextEntry edit: a null replacement removes the entry's messages, and any other replaces the content of the user, assistant, toolResult
// and custom ones (a string replacement of an assistant or toolResult message is one text block).
func applyContextEdit(messages []any, edit map[string]any) ([]any, error) {
	replacement, present := edit["replacement"]
	if present && replacement == nil {
		return []any{}, nil
	}
	out := make([]any, len(messages))
	for i, raw := range messages {
		message, isObject := raw.(map[string]any)
		role, _ := message["role"].(string)
		if !isObject || (role != "user" && role != "assistant" && role != "toolResult" && role != "custom") {
			out[i] = raw
			continue
		}
		if !present {
			return nil, errors.New("TypeError: Cannot read properties of undefined (reading 'content')")
		}
		object, _ := replacement.(map[string]any)
		content, hasContent := object["content"]
		copied := make(map[string]any, len(message))
		maps.Copy(copied, message)
		if text, isString := content.(string); isString && (role == "assistant" || role == "toolResult") {
			copied["content"] = []any{map[string]any{"type": "text", "text": text}}
		} else if hasContent {
			copied["content"] = content
		} else {
			delete(copied, "content")
		}
		out[i] = copied
	}
	return out, nil
}
