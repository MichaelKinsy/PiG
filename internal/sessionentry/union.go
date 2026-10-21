package sessionentry

import (
	"bytes"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"

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
func (b *SessionEntryBase) setBase(base SessionEntryBase) { *b = base }

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
	return encodeFields(e.ParentID, fields(e))
}

// AppendStringify appends the JSON.stringify text of MarshalJSON's output to dst (json.StringifyAppender): the message is encoded into dst
// itself rather than apart.
func (e MessageEntry) AppendStringify(dst []byte) ([]byte, error) {
	if e.raw != nil {
		return json.AppendStringify(dst, json.RawMessage(e.raw))
	}
	type fields MessageEntry
	return appendFields(dst, e.ParentID, fields(e))
}

func (e ThinkingLevelEntry) Base() SessionEntryBase { return e.withoutRecord() }

func (e ThinkingLevelEntry) fileEntry() {}

func (e ThinkingLevelEntry) Raw() json.RawMessage { return rawOf(e.MarshalJSON) }

func (e ThinkingLevelEntry) MarshalJSON() ([]byte, error) {
	if e.raw != nil {
		return e.raw, nil
	}
	type fields ThinkingLevelEntry
	return encodeFields(e.ParentID, fields(e))
}

func (e ModelChangeEntry) Base() SessionEntryBase { return e.withoutRecord() }

func (e ModelChangeEntry) fileEntry() {}

func (e ModelChangeEntry) Raw() json.RawMessage { return rawOf(e.MarshalJSON) }

func (e ModelChangeEntry) MarshalJSON() ([]byte, error) {
	if e.raw != nil {
		return e.raw, nil
	}
	type fields ModelChangeEntry
	return encodeFields(e.ParentID, fields(e))
}

func (e UsageEntry) Base() SessionEntryBase { return e.withoutRecord() }

func (e UsageEntry) fileEntry() {}

func (e UsageEntry) Raw() json.RawMessage { return rawOf(e.MarshalJSON) }

func (e UsageEntry) MarshalJSON() ([]byte, error) {
	if e.raw != nil {
		return e.raw, nil
	}
	type fields UsageEntry
	return encodeFields(e.ParentID, fields(e))
}

func (e CompactionEntry) Base() SessionEntryBase { return e.withoutRecord() }

func (e CompactionEntry) fileEntry() {}

func (e CompactionEntry) Raw() json.RawMessage { return rawOf(e.MarshalJSON) }

// WithoutRecord is e without the record it was decoded from: it writes its fields.
func (e CompactionEntry) WithoutRecord() CompactionEntry {
	e.raw = nil
	return e
}

func (e CompactionEntry) MarshalJSON() ([]byte, error) {
	if e.raw != nil {
		return e.raw, nil
	}
	type fields CompactionEntry
	out, err := encodeFields(e.ParentID, fields(e))
	if err != nil || !e.FromHookAbsent {
		return out, err
	}
	object, err := orderedjson.Parse(out)
	if err != nil {
		return nil, err
	}
	object.Delete("fromHook")
	return object.MarshalJSON() //perf:ok once per compaction entry written, and only when the entry carries no fromHook
}

func (e BranchSummaryEntry) Base() SessionEntryBase { return e.withoutRecord() }

func (e BranchSummaryEntry) fileEntry() {}

func (e BranchSummaryEntry) Raw() json.RawMessage { return rawOf(e.MarshalJSON) }

func (e BranchSummaryEntry) MarshalJSON() ([]byte, error) {
	if e.raw != nil {
		return e.raw, nil
	}
	type fields BranchSummaryEntry
	out, err := encodeFields(e.ParentID, fields(e))
	// fromHook is the last member of the record.
	if trimmed, found := bytes.CutSuffix(out, []byte(`,"fromHook":false}`)); err == nil && e.fromHookAbsent && found {
		out = trimmed[:len(trimmed)+1]
		out[len(out)-1] = '}'
	}
	return out, err
}

func (e CustomEntry) Base() SessionEntryBase { return e.withoutRecord() }

func (e CustomEntry) fileEntry() {}

func (e CustomEntry) Raw() json.RawMessage { return rawOf(e.MarshalJSON) }

func (e CustomEntry) MarshalJSON() ([]byte, error) {
	if e.raw != nil {
		return e.raw, nil
	}
	// session-manager.ts appendCustomEntry builds { type, customType, data, id, parentId, timestamp }.
	return encodeFields(e.ParentID, struct {
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
	return encodeFields(e.ParentID, struct {
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
	return encodeFields(e.ParentID, fields(e))
}

func (e LabelEntry) Base() SessionEntryBase { return e.withoutRecord() }

func (e LabelEntry) fileEntry() {}

func (e LabelEntry) Raw() json.RawMessage { return rawOf(e.MarshalJSON) }

func (e LabelEntry) MarshalJSON() ([]byte, error) {
	if e.raw != nil {
		return e.raw, nil
	}
	type fields LabelEntry
	return encodeFields(e.ParentID, fields(e))
}

func (e SessionInfoEntry) Base() SessionEntryBase { return e.withoutRecord() }

func (e SessionInfoEntry) fileEntry() {}

func (e SessionInfoEntry) Raw() json.RawMessage { return rawOf(e.MarshalJSON) }

func (e SessionInfoEntry) MarshalJSON() ([]byte, error) {
	if e.raw != nil {
		return e.raw, nil
	}
	type fields SessionInfoEntry
	return encodeFields(e.ParentID, fields(e))
}

func (e SessionHeader) Raw() json.RawMessage { return rawOf(e.MarshalJSON) }

// SameRecord reports whether e and other have equal members and decoded records, so they encode to the same bytes. It does not encode either.
func (e SessionHeader) SameRecord(other SessionHeader) bool {
	return e.Type == other.Type && e.Version == other.Version && e.ID == other.ID && e.Timestamp == other.Timestamp && e.CWD == other.CWD && e.ParentSession == other.ParentSession && bytes.Equal(e.raw, other.raw)
}

func (e SessionHeader) MarshalJSON() ([]byte, error) {
	if e.raw != nil {
		return e.raw, nil
	}
	type fields SessionHeader
	return encodeFields(nil, fields(e))
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
func encodeFields(parent *string, fields any) ([]byte, error) {
	out, err := jsstring.MarshalJSON(fields)
	if err != nil || parent == nil || !IsJSValue(*parent) {
		return out, err
	}
	// A child of an entry whose id is no string has that value as its parentId; JSON.stringify omits an undefined one.
	object, err := orderedjson.Parse(out)
	if err != nil {
		return nil, err
	}
	if value, ok := ValueJSON(*parent); ok {
		object.Set("parentId", value)
	} else {
		object.Delete("parentId")
	}
	return object.MarshalJSON()
}

// appendFields appends the JSON.stringify text of encodeFields(parent, fields) to dst. Only a parentId that is no string needs the separate
// encoding.
func appendFields(dst []byte, parent *string, fields any) ([]byte, error) {
	if parent != nil && IsJSValue(*parent) {
		out, err := encodeFields(parent, fields)
		if err != nil {
			return dst, err
		}
		return json.AppendStringify(dst, json.RawMessage(out))
	}
	return json.AppendStringify(dst, fields)
}

// DecodeSessionEntry reads one entry record into the member of SessionEntry its type names, keeping the record so the entry writes back as it
// was read. Pi casts whatever JSON.parse returns and checks no member (session-manager.ts parseSessionEntries), so an id or parentId that is no
// string keeps its JavaScript value's key (see jsValueMark). A record that is not an object, or whose other members do not decode into the
// member its type names, is a RawEntry.
func DecodeSessionEntry(raw json.RawMessage) SessionEntry {
	base, data := strictBase(raw)
	if data == nil {
		base, data = lenientBase(raw)
		if data == nil {
			return RawEntry{base}
		}
	}
	switch base.Type {
	case "message":
		return decodeMember[MessageEntry](data, base)
	case "thinking_level_change":
		return decodeMember[ThinkingLevelEntry](data, base)
	case "model_change":
		return decodeMember[ModelChangeEntry](data, base)
	case "usage":
		return decodeMember[UsageEntry](data, base)
	case "compaction":
		return decodeMember[CompactionEntry](data, base)
	case "branch_summary":
		return decodeMember[BranchSummaryEntry](data, base)
	case "custom":
		return decodeMember[CustomEntry](data, base)
	case "custom_message":
		return decodeMember[CustomMessageEntry](data, base)
	case "context_edit":
		return decodeMember[ContextEditEntry](data, base)
	case "label":
		return decodeMember[LabelEntry](data, base)
	case "session_info":
		return decodeMember[SessionInfoEntry](data, base)
	}
	return RawEntry{base}
}

// strictBase decodes the base members of a record whose type, id and timestamp are strings or null and whose parentId is null or a string;
// data is nil when they do not decode. An absent id or parentId is undefined and a null id is null, which the string decode cannot tell from "".
func strictBase(raw json.RawMessage) (base SessionEntryBase, data json.RawMessage) {
	if json.Unmarshal(raw, &base) != nil {
		return SessionEntryBase{}, nil
	}
	base.raw = raw
	return WithJSValues(raw, base), raw
}

// WithJSValues is base, decoded from raw as a string-typed SessionEntryBase, with an absent parentId undefined and an id that is absent or null
// its JavaScript value's key: the string decode reads both as nil or "".
func WithJSValues(raw json.RawMessage, base SessionEntryBase) SessionEntryBase {
	if base.ParentID != nil && base.ID != "" {
		return base
	}
	var probe struct {
		ID       json.RawMessage `json:"id"`
		ParentID json.RawMessage `json:"parentId"`
	}
	if json.Unmarshal(raw, &probe) != nil {
		return base
	}
	if base.ParentID == nil && probe.ParentID == nil {
		undefined := jsUndefined
		base.ParentID = &undefined
	}
	if base.ID == "" && !bytes.HasPrefix(bytes.TrimSpace(probe.ID), []byte(`"`)) {
		base.ID = jsValueKey(probe.ID)
	}
	return base
}

// decodeMember decodes data into the entry type T and sets its base members to base, which carries the record; a RawEntry when the members do
// not decode.
func decodeMember[T SessionEntry, PT interface {
	*T
	setBase(SessionEntryBase)
}](data json.RawMessage, base SessionEntryBase) SessionEntry {
	var entry T
	if json.Unmarshal(data, &entry) != nil {
		return RawEntry{base}
	}
	PT(&entry).setBase(base)
	return *PT(&entry)
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
		//portlint:allow mapkeyorder stripped is only decoded into SessionHeader, and the header keeps the record's own bytes (header.raw = raw below)
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

// NewDecodedMessageEntry is the entry DecodeSessionEntry(raw) returns for the record raw, which encodes base and message, when decoded is the message that record decodes to. The record is not parsed again. It reports false for a base whose id or parent id stands for a JavaScript value, which decoding reads from the record.
// pig additive (D112): see agent.AgentMessage.DecodedForm.
func NewDecodedMessageEntry(base SessionEntryBase, decoded agent.AgentMessage, raw json.RawMessage) (MessageEntry, bool) {
	if base.ID == "" || IsJSValue(base.ID) || (base.ParentID != nil && IsJSValue(*base.ParentID)) {
		return MessageEntry{}, false
	}
	base.raw = raw
	return MessageEntry{SessionEntryBase: base, Message: decoded}, true
}
