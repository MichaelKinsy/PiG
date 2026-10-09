package sessionentry

import (
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// FileEntry is session-manager.ts FileEntry, `SessionHeader | SessionEntry`: one record of a session file. The members are SessionHeader and
// the members of SessionEntry.
type FileEntry interface {
	json.Marshaler
	// Raw is the record's JSON.
	Raw() json.RawMessage
	fileEntry()
}

// SessionEntry is session-manager.ts SessionEntry, the union of MessageEntry (SessionMessageEntry), ThinkingLevelEntry (ThinkingLevelChangeEntry),
// ModelChangeEntry, UsageEntry, CompactionEntry, BranchSummaryEntry, CustomEntry, CustomMessageEntry, ContextEditEntry, LabelEntry and
// SessionInfoEntry. RawEntry is the one more implementation: the record a file holds that none of them describes.
type SessionEntry interface {
	FileEntry
	// Base is the type, id, parentId and timestamp every entry has.
	Base() SessionEntryBase
}

// RawEntry is a record of a session file that no member of the union describes: a type this build does not know, a known type whose members do
// not decode into its entry (Pi casts the parsed JSON and checks no member), or JSON that is not an object. Pi keeps the parsed value whatever
// it is (session-manager.ts parseSessionEntries, loadEntriesFromFile), and so does the record: Raw is the JSON as read, and the base holds the
// members of it that decode.
type RawEntry struct {
	SessionEntryBase
}

func (e RawEntry) Base() SessionEntryBase { return e.withoutRecord() }

func (e RawEntry) fileEntry() {}

// Raw is the record's JSON as it was read.
func (e RawEntry) Raw() json.RawMessage { return e.raw }

// MarshalJSON writes the record as it was read.
func (e RawEntry) MarshalJSON() ([]byte, error) { return e.raw, nil }

// ContextEditEntry is an append-only change to one earlier entry's contribution to model context (session-manager.ts ContextEditEntry). A nil
// Replacement serializes as null and omits the target from model context; a replacement changes only the target's content. The target entry
// itself is never rewritten.
type ContextEditEntry struct {
	SessionEntryBase
	TargetID    string                  `json:"targetId"`
	Replacement *ContextEditReplacement `json:"replacement"`
}

// The unexported marker method fileEntry belongs to each member itself, so no type outside this package, and no type that merely embeds
// SessionEntryBase, is a member of the union.
//
// Each member writes the record it was decoded from, so a member the struct does not name and the order of the members survive a rewrite of the
// file, as Pi's parsed object does. A decoded member is a value to read: changing a field of a copy does not change the record it writes, so
// build a new member to write different fields. Base carries no record, so a member built from another entry's Base writes its own fields. A
// member built in memory writes its fields.

// withoutRecord is the base members without the record they were decoded from.
func (b SessionEntryBase) withoutRecord() SessionEntryBase {
	b.raw = nil
	return b
}

func (e MessageEntry) Base() SessionEntryBase { return e.withoutRecord() }

func (e MessageEntry) fileEntry() {}

func (e MessageEntry) Raw() json.RawMessage { return rawOf(e.MarshalJSON) }

func (e MessageEntry) MarshalJSON() ([]byte, error) {
	if e.raw != nil {
		return e.raw, nil
	}
	type fields MessageEntry
	return encodeFields(fields(e))
}

func (e ThinkingLevelEntry) Base() SessionEntryBase { return e.withoutRecord() }

func (e ThinkingLevelEntry) fileEntry() {}

func (e ThinkingLevelEntry) Raw() json.RawMessage { return rawOf(e.MarshalJSON) }

func (e ThinkingLevelEntry) MarshalJSON() ([]byte, error) {
	if e.raw != nil {
		return e.raw, nil
	}
	type fields ThinkingLevelEntry
	return encodeFields(fields(e))
}

func (e ModelChangeEntry) Base() SessionEntryBase { return e.withoutRecord() }

func (e ModelChangeEntry) fileEntry() {}

func (e ModelChangeEntry) Raw() json.RawMessage { return rawOf(e.MarshalJSON) }

func (e ModelChangeEntry) MarshalJSON() ([]byte, error) {
	if e.raw != nil {
		return e.raw, nil
	}
	type fields ModelChangeEntry
	return encodeFields(fields(e))
}

func (e UsageEntry) Base() SessionEntryBase { return e.withoutRecord() }

func (e UsageEntry) fileEntry() {}

func (e UsageEntry) Raw() json.RawMessage { return rawOf(e.MarshalJSON) }

func (e UsageEntry) MarshalJSON() ([]byte, error) {
	if e.raw != nil {
		return e.raw, nil
	}
	type fields UsageEntry
	return encodeFields(fields(e))
}

func (e CompactionEntry) Base() SessionEntryBase { return e.withoutRecord() }

func (e CompactionEntry) fileEntry() {}

func (e CompactionEntry) Raw() json.RawMessage { return rawOf(e.MarshalJSON) }

func (e CompactionEntry) MarshalJSON() ([]byte, error) {
	if e.raw != nil {
		return e.raw, nil
	}
	type fields CompactionEntry
	return encodeFields(fields(e))
}

func (e BranchSummaryEntry) Base() SessionEntryBase { return e.withoutRecord() }

func (e BranchSummaryEntry) fileEntry() {}

func (e BranchSummaryEntry) Raw() json.RawMessage { return rawOf(e.MarshalJSON) }

func (e BranchSummaryEntry) MarshalJSON() ([]byte, error) {
	if e.raw != nil {
		return e.raw, nil
	}
	type fields BranchSummaryEntry
	return encodeFields(fields(e))
}

func (e CustomEntry) Base() SessionEntryBase { return e.withoutRecord() }

func (e CustomEntry) fileEntry() {}

func (e CustomEntry) Raw() json.RawMessage { return rawOf(e.MarshalJSON) }

func (e CustomEntry) MarshalJSON() ([]byte, error) {
	if e.raw != nil {
		return e.raw, nil
	}
	// session-manager.ts appendCustomEntry builds { type, customType, data, id, parentId, timestamp }.
	return encodeFields(struct {
		Type       string  `json:"type"`
		CustomType string  `json:"customType"`
		Data       any     `json:"data,omitempty"`
		ID         string  `json:"id"`
		ParentID   *string `json:"parentId"`
		Timestamp  string  `json:"timestamp"`
	}{e.Type, e.CustomType, e.Data, e.ID, e.ParentID, e.Timestamp})
}

func (e CustomMessageEntry) Base() SessionEntryBase { return e.withoutRecord() }

func (e CustomMessageEntry) fileEntry() {}

func (e CustomMessageEntry) Raw() json.RawMessage { return rawOf(e.MarshalJSON) }

func (e CustomMessageEntry) MarshalJSON() ([]byte, error) {
	if e.raw != nil {
		return e.raw, nil
	}
	// session-manager.ts appendCustomMessageEntry builds { type, customType, content, display, details, id, parentId, timestamp }.
	return encodeFields(struct {
		Type       string  `json:"type"`
		CustomType string  `json:"customType"`
		Content    any     `json:"content"`
		Display    bool    `json:"display"`
		Details    any     `json:"details,omitempty"`
		ID         string  `json:"id"`
		ParentID   *string `json:"parentId"`
		Timestamp  string  `json:"timestamp"`
	}{e.Type, e.CustomType, e.Content, e.Display, e.Details, e.ID, e.ParentID, e.Timestamp})
}

func (e ContextEditEntry) Base() SessionEntryBase { return e.withoutRecord() }

func (e ContextEditEntry) fileEntry() {}

func (e ContextEditEntry) Raw() json.RawMessage { return rawOf(e.MarshalJSON) }

func (e ContextEditEntry) MarshalJSON() ([]byte, error) {
	if e.raw != nil {
		return e.raw, nil
	}
	type fields ContextEditEntry
	return encodeFields(fields(e))
}

func (e LabelEntry) Base() SessionEntryBase { return e.withoutRecord() }

func (e LabelEntry) fileEntry() {}

func (e LabelEntry) Raw() json.RawMessage { return rawOf(e.MarshalJSON) }

func (e LabelEntry) MarshalJSON() ([]byte, error) {
	if e.raw != nil {
		return e.raw, nil
	}
	type fields LabelEntry
	return encodeFields(fields(e))
}

func (e SessionInfoEntry) Base() SessionEntryBase { return e.withoutRecord() }

func (e SessionInfoEntry) fileEntry() {}

func (e SessionInfoEntry) Raw() json.RawMessage { return rawOf(e.MarshalJSON) }

func (e SessionInfoEntry) MarshalJSON() ([]byte, error) {
	if e.raw != nil {
		return e.raw, nil
	}
	type fields SessionInfoEntry
	return encodeFields(fields(e))
}

func (e SessionHeader) Raw() json.RawMessage { return rawOf(e.MarshalJSON) }

func (e SessionHeader) MarshalJSON() ([]byte, error) {
	if e.raw != nil {
		return e.raw, nil
	}
	type fields SessionHeader
	return encodeFields(fields(e))
}

// rawOf is the record a member writes. An encoding error leaves no record.
func rawOf(marshal func() ([]byte, error)) json.RawMessage {
	encoded, err := marshal()
	if err != nil {
		return nil
	}
	return encoded
}

// encodeFields encodes the fields of a member in declaration order as JSON.stringify writes them: '<', '>' and '&' and the line and paragraph
// separators literal, a negative zero as 0.
func encodeFields(fields any) ([]byte, error) {
	return jsstring.MarshalJSON(fields)
}

// DecodeSessionEntry reads one entry record into the member of SessionEntry its type names, keeping the record so the entry writes back as it
// was read. A record that is not an object with string type, id and timestamp and a null or string parentId, or whose members do not decode into
// the named member, is a RawEntry: Pi casts whatever JSON.parse returns and checks no member (session-manager.ts parseSessionEntries).
func DecodeSessionEntry(raw json.RawMessage) SessionEntry {
	var base SessionEntryBase
	if json.Unmarshal(raw, &base) != nil {
		return RawEntry{SessionEntryBase{raw: raw}}
	}
	base.raw = raw
	switch base.Type {
	case "message":
		var entry MessageEntry
		if json.Unmarshal(raw, &entry) != nil {
			return RawEntry{base}
		}
		entry.raw = raw
		return entry
	case "thinking_level_change":
		var entry ThinkingLevelEntry
		if json.Unmarshal(raw, &entry) != nil {
			return RawEntry{base}
		}
		entry.raw = raw
		return entry
	case "model_change":
		var entry ModelChangeEntry
		if json.Unmarshal(raw, &entry) != nil {
			return RawEntry{base}
		}
		entry.raw = raw
		return entry
	case "usage":
		var entry UsageEntry
		if json.Unmarshal(raw, &entry) != nil {
			return RawEntry{base}
		}
		entry.raw = raw
		return entry
	case "compaction":
		var entry CompactionEntry
		if json.Unmarshal(raw, &entry) != nil {
			return RawEntry{base}
		}
		entry.raw = raw
		return entry
	case "branch_summary":
		var entry BranchSummaryEntry
		if json.Unmarshal(raw, &entry) != nil {
			return RawEntry{base}
		}
		entry.raw = raw
		return entry
	case "custom":
		var entry CustomEntry
		if json.Unmarshal(raw, &entry) != nil {
			return RawEntry{base}
		}
		entry.raw = raw
		return entry
	case "custom_message":
		var entry CustomMessageEntry
		if json.Unmarshal(raw, &entry) != nil {
			return RawEntry{base}
		}
		entry.raw = raw
		return entry
	case "context_edit":
		var entry ContextEditEntry
		if json.Unmarshal(raw, &entry) != nil {
			return RawEntry{base}
		}
		entry.raw = raw
		return entry
	case "label":
		var entry LabelEntry
		if json.Unmarshal(raw, &entry) != nil {
			return RawEntry{base}
		}
		entry.raw = raw
		return entry
	case "session_info":
		var entry SessionInfoEntry
		if json.Unmarshal(raw, &entry) != nil {
			return RawEntry{base}
		}
		entry.raw = raw
		return entry
	}
	return RawEntry{base}
}

// CurrentSessionVersion is session-manager.ts CURRENT_SESSION_VERSION.
const CurrentSessionVersion = 3

// DecodeFileEntry reads one record of a session file: the header when it has type "session" and a string id (loadEntriesFromFile checks no more),
// else the entry DecodeSessionEntry reads.
func DecodeFileEntry(raw json.RawMessage) FileEntry {
	var probe struct {
		Type string  `json:"type"`
		ID   *string `json:"id"`
	}
	if json.Unmarshal(raw, &probe) != nil || probe.Type != "session" || probe.ID == nil {
		return DecodeSessionEntry(raw)
	}
	header, err := DecodeSessionHeader(raw)
	if err != nil {
		return DecodeSessionEntry(raw)
	}
	return header
}

// DecodeSessionHeader reads a header record, keeping the record. A version that is not a number is accepted: migrateToCurrentVersion's
// comparisons are then false, no migration step runs, and the header counts as current.
func DecodeSessionHeader(raw json.RawMessage) (SessionHeader, error) {
	var header SessionHeader
	err := json.Unmarshal(raw, &header)
	if err != nil {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			return SessionHeader{}, err
		}
		delete(fields, "version")
		stripped, marshalErr := json.Marshal(fields)
		header = SessionHeader{}
		if marshalErr != nil || json.Unmarshal(stripped, &header) != nil {
			return SessionHeader{}, err
		}
		header.Version = CurrentSessionVersion
	}
	header.raw = raw
	return header, nil
}
