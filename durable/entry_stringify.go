package durable

import (
	"math"
	"slices"
	"strconv"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/jsparity"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// maxStringifyDepth bounds the nesting appendEntryData follows; a deeper value goes to MarshalJSON, which reports it.
const maxStringifyDepth = 1000

// entryMarshaler hides EntryRecord's AppendStringify, so the general encoder runs MarshalJSON and compacts its output.
type entryMarshaler EntryRecord

func (entry entryMarshaler) MarshalJSON() ([]byte, error) { return EntryRecord(entry).marshalGeneral() } //perf:ok the cold fallback of AppendStringify: it runs MarshalJSON, the encoder AppendStringify stands in for, and compacts its output

// AppendStringify appends the JSON.stringify text of the record to dst (json.StringifyAppender): the members are written in
// place, in the order of MarshalJSON, instead of through the wire struct and a second compacting pass. MarshalJSON writes the
// same members with the same writer, so the text is MarshalJSON's, byte for byte. A record with anything the in-place writer does
// not write (a Data that is not a JSON kind, a Go map, number or slice type it does not know, a non-finite number, a nesting
// deeper than maxStringifyDepth, a message or edit that fails to encode) goes through encoding/json, so its text and its error
// are the general encoder's.
func (entry EntryRecord) AppendStringify(dst []byte) ([]byte, error) {
	start := len(dst)
	if dst, ok := entry.appendMembers(dst); ok {
		return dst, nil
	}
	return jsstring.AppendMarshalJSON(dst[:start], entryMarshaler(entry))
}

func (entry EntryRecord) appendMembers(dst []byte) ([]byte, bool) {
	var err error
	var ok bool
	dst = append(dst, `{"id":`...)
	dst = strconv.AppendInt(dst, int64(entry.Id), 10)
	dst = append(dst, `,"conversationId":`...)
	dst = strconv.AppendInt(dst, int64(entry.ConversationId), 10)
	dst = append(dst, `,"kind":`...)
	dst = appendString(dst, entry.Kind)
	if entry.Model != nil {
		dst = append(dst, `,"model":`...)
		if dst, err = appendMessages(dst, entry.Model); err != nil {
			return dst, false
		}
	}
	if entry.Data != nil {
		dst = append(dst, `,"data":`...)
		if dst, ok = appendEntryData(dst, entry.Data, 0); !ok {
			return dst, false
		}
	}
	if entry.Head != nil {
		dst = append(dst, `,"head":`...)
		dst = strconv.AppendInt(dst, int64(*entry.Head), 10)
	}
	if entry.Edits != nil {
		dst = append(dst, `,"edits":`...)
		if dst, err = jsstring.AppendMarshalJSON(dst, entry.Edits); err != nil {
			return dst, false
		}
	}
	if entry.ByTaskId != nil {
		dst = append(dst, `,"byTaskId":`...)
		dst = strconv.AppendInt(dst, int64(*entry.ByTaskId), 10)
	}
	return append(dst, '}'), true
}

// appendMessages appends the JSON.stringify text of a message list, each message encoded on its own: the list itself would
// be boxed to be passed on, and a message is already an interface value.
func appendMessages(dst []byte, messages []ai.Message) ([]byte, error) {
	start := len(dst)
	dst = append(dst, '[')
	for index, message := range messages {
		if index > 0 {
			dst = append(dst, ',')
		}
		var err error
		if dst, err = jsstring.AppendMarshalJSON(dst, message); err != nil {
			return dst[:start], err
		}
	}
	return append(dst, ']'), nil
}

// appendString appends s as the JSON.stringify text of a string: a lone UTF-16 surrogate held as WTF-8 is its escape, a high
// unit followed by a low one is the character, an ill-formed byte is U+FFFD.
func appendString(dst []byte, s string) []byte { return jsparity.AppendString(dst, s) }

// appendEntryData appends the JSON.stringify text of a Data value as MarshalJSON writes it. It writes the JSON kinds a
// durable decode or copy produces (nil, bool, float64, string, []any, *delta.JsonObject), a Go map[string]any, whose keys
// MarshalJSON sorts as strings, and int, int32 and int64. ok is false for any other value, and for one MarshalJSON writes
// differently or fails on.
func appendEntryData(dst []byte, value any, depth int) ([]byte, bool) {
	if depth > maxStringifyDepth {
		return dst, false
	}
	var ok bool
	switch typed := value.(type) {
	case nil:
		return append(dst, "null"...), true
	case bool:
		return jsparity.AppendBool(dst, typed), true
	case string:
		return appendString(dst, typed), true
	case int:
		return strconv.AppendInt(dst, int64(typed), 10), true
	case int32:
		return strconv.AppendInt(dst, int64(typed), 10), true
	case int64:
		return strconv.AppendInt(dst, typed, 10), true
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return dst, false
		}
		return jsparity.AppendNumber(dst, typed), true
	case []any:
		if typed == nil {
			return append(dst, "null"...), true
		}
		dst = append(dst, '[')
		for index, item := range typed {
			if index > 0 {
				dst = append(dst, ',')
			}
			if dst, ok = appendEntryData(dst, item, depth+1); !ok {
				return dst, false
			}
		}
		return append(dst, ']'), true
	case *delta.JsonObject:
		if typed == nil {
			return append(dst, "null"...), true
		}
		dst = append(dst, '{')
		first := true
		for key, item := range typed.All() {
			if !first {
				dst = append(dst, ',')
			}
			first = false
			dst = appendString(dst, key)
			dst = append(dst, ':')
			if dst, ok = appendEntryData(dst, item, depth+1); !ok {
				return dst, false
			}
		}
		return append(dst, '}'), true
	case map[string]any:
		if typed == nil {
			return append(dst, "null"...), true
		}
		var room [16]string
		keys := room[:0]
		for key := range typed {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		dst = append(dst, '{')
		for index, key := range keys {
			if index > 0 {
				dst = append(dst, ',')
			}
			dst = appendString(dst, key)
			dst = append(dst, ':')
			if dst, ok = appendEntryData(dst, typed[key], depth+1); !ok {
				return dst, false
			}
		}
		return append(dst, '}'), true
	}
	return dst, false
}
