package sessionentry

import (
	"encoding/json"
	"fmt"
	"testing"
)

// optionalMemberSessionLines is a session file's records, lines of them, in the mix a long session with an active extension holds: of every ten, six
// messages, a custom entry, a custom message, a label and a compaction, each optional member a value, null or left out in turn.
func optionalMemberSessionLines(lines int) [][]byte {
	const usage = `{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}`
	member := func(i int, name, value string) string {
		switch i / 10 % 3 {
		case 0:
			return `,"` + name + `":` + value
		case 1:
			return `,"` + name + `":null`
		}
		return ""
	}
	out := make([][]byte, 0, lines)
	for i := range lines {
		base := fmt.Sprintf(`"id":"%08x","parentId":"%08x","timestamp":"2026-01-01T00:00:00.000Z"`, i+1, i)
		var line string
		switch i % 10 {
		case 6:
			line = `{"type":"custom","customType":"bench"` + member(i, "data", `{"k":1,"text":"some extension state"}`) + `,` + base + `}`
		case 7:
			line = `{"type":"custom_message","customType":"bench","content":"a message an extension sent","display":true` + member(i, "details", `{"k":1}`) + `,` + base + `}`
		case 8:
			line = `{"type":"label",` + base + `,"targetId":"00000001"` + member(i, "label", `"mark"`) + `}`
		case 9:
			line = `{"type":"compaction",` + base + `,"summary":"a summary of the turns before","firstKeptEntryId":"00000001","tokensBefore":1000` + member(i, "details", `{"format":"x"}`) + member(i, "usage", usage) + `,"fromHook":true}`
		case 0, 2, 4:
			line = `{"type":"message",` + base + `,"message":{"role":"user","content":"a prompt of ordinary length for the benchmark","timestamp":1}}`
		default:
			line = `{"type":"message",` + base + `,"message":{"role":"assistant","content":[{"type":"text","text":"an answer of ordinary length for the benchmark"}],"api":"faux","provider":"faux","model":"faux-1","usage":` + usage + `,"stopReason":"stop","timestamp":2}}`
		}
		out = append(out, []byte(line))
	}
	return out
}

// BenchmarkDecodeOptionalMemberEntries decodes every record of a 5000-line session into its entry type, the work a reader does that needs each entry's
// members (a rewrite, an export, the transcript): the entries with optional members are the ones whose decode tells a null member from an absent one.
func BenchmarkDecodeOptionalMemberEntries(b *testing.B) {
	lines := optionalMemberSessionLines(5000)
	b.ReportAllocs()
	for b.Loop() {
		for i, line := range lines {
			var err error
			switch i % 10 {
			case 6:
				var entry CustomEntry
				err = json.Unmarshal(line, &entry)
			case 7:
				var entry CustomMessageEntry
				err = json.Unmarshal(line, &entry)
			case 8:
				var entry LabelEntry
				err = json.Unmarshal(line, &entry)
			case 9:
				var entry CompactionEntry
				err = json.Unmarshal(line, &entry)
			default:
				var entry MessageEntry
				err = json.Unmarshal(line, &entry)
			}
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkEncodeOptionalMemberEntries writes the entries an append builds: a custom entry, a custom message, a label and a compaction, without a record.
func BenchmarkEncodeOptionalMemberEntries(b *testing.B) {
	parent, label := "00000001", "mark"
	base := func(kind string) SessionEntryBase {
		return SessionEntryBase{Type: kind, ID: "00000002", ParentID: &parent, Timestamp: "2026-01-01T00:00:00.000Z"}
	}
	entries := []json.Marshaler{
		CustomEntry{SessionEntryBase: base("custom"), CustomType: "bench", Data: json.RawMessage(`{"k":1}`)},
		CustomMessageEntry{SessionEntryBase: base("custom_message"), CustomType: "bench", Content: "x", Display: true},
		LabelEntry{SessionEntryBase: base("label"), TargetID: parent, Label: &label},
		CompactionEntry{SessionEntryBase: base("compaction"), Summary: "s", FirstKeptEntryID: parent, TokensBefore: 10, FromHook: true},
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, entry := range entries {
			if _, err := entry.MarshalJSON(); err != nil {
				b.Fatal(err)
			}
		}
	}
}
