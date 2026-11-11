package viewstest

import (
	"encoding/json"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/views"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

// EditsValue is edits as a call to a room's `edit` carries them (the contract's TextEdit schema).
func EditsValue(edits []views.TextEdit) []any {
	id := func(id views.TextID) *delta.JsonObject {
		return delta.JsonObjectOf("bunchId", id.BunchID, "counter", id.Counter)
	}
	list := make([]any, len(edits))
	for i, edit := range edits {
		if edit.Insert {
			var before any
			if edit.Before != nil {
				before = id(*edit.Before)
			}
			list[i] = delta.JsonObjectOf("kind", "insert", "before", before, "id", id(edit.ID), "content", edit.Content, "inWord", edit.InWord)
			continue
		}
		list[i] = delta.JsonObjectOf("kind", "delete", "start", id(edit.Start), "end", id(edit.End), "length", edit.Length)
	}
	return list
}

// EditsOfLog is the edits of a message of a text room's log (`JSON.parse(message.data)`).
func EditsOfLog(data string) []views.TextEdit {
	var raw []map[string]json.RawMessage
	if json.Unmarshal([]byte(data), &raw) != nil {
		return nil
	}
	read := func(entry map[string]json.RawMessage, key string) views.TextID {
		var id struct {
			BunchID string  `json:"bunchId"`
			Counter float64 `json:"counter"`
		}
		_ = json.Unmarshal(entry[key], &id)
		return views.TextID{BunchID: id.BunchID, Counter: id.Counter}
	}
	edits := make([]views.TextEdit, len(raw))
	for i, entry := range raw {
		var kind string
		_ = json.Unmarshal(entry["kind"], &kind)
		if kind == "insert" {
			edit := views.TextEdit{Insert: true, ID: read(entry, "id")}
			if string(entry["before"]) != "null" {
				before := read(entry, "before")
				edit.Before = &before
			}
			_ = json.Unmarshal(entry["content"], &edit.Content)
			_ = json.Unmarshal(entry["inWord"], &edit.InWord)
			edits[i] = edit
			continue
		}
		edit := views.TextEdit{Start: read(entry, "start"), End: read(entry, "end")}
		_ = json.Unmarshal(entry["length"], &edit.Length)
		edits[i] = edit
	}
	return edits
}
