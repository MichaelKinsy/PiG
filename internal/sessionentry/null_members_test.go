package sessionentry

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// Pi stores each optional member as its caller gave it (session-manager.ts appendCustomEntry, appendCustomMessageEntry, appendCompaction, branchWithSummary,
// appendLabelChange) and JSON.stringify writes a null and leaves out an undefined one, so a reader gets null or undefined back. Each record is a line the
// pinned Pi package wrote (coding/testdata/entry-null-members-pi.jsonl, with an identity and a time): it decodes into an entry that tells null from absent, the
// entry writes the record back, and an entry built from the decoded fields alone writes the same bytes.
func TestOptionalMembersKeepNullApartFromAbsent(t *testing.T) {
	const base = `"id":"aaaaaaaa","parentId":"bbbbbbbb","timestamp":"2026-01-01T00:00:00.000Z"`
	const usage = `{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}`
	custom := func(data string) string { return `{"type":"custom","customType":"t",` + data + base + `}` }
	message := func(details string) string {
		return `{"type":"custom_message","customType":"t","content":"x","display":true,` + details + base + `}`
	}
	compaction := func(members string) string {
		return `{"type":"compaction",` + base + `,"summary":"s","firstKeptEntryId":"cccccccc","tokensBefore":10,` + members + `"fromHook":true}`
	}
	branch := func(members string) string {
		return `{"type":"branch_summary",` + base + `,"fromId":"cccccccc","summary":"s",` + members + `"fromHook":false}`
	}
	label := func(member string) string { return `{"type":"label",` + base + `,"targetId":"cccccccc"` + member + `}` }
	cases := []struct{ name, record, view string }{
		{"custom data absent", custom(""), "data=absent"},
		{"custom data null", custom(`"data":null,`), "data=null"},
		{"custom data object", custom(`"data":{"b":1,"a":2},`), "data=value"},
		{"custom data zero", custom(`"data":0,`), "data=value"},
		{"custom data empty string", custom(`"data":"",`), "data=value"},
		{"custom data false", custom(`"data":false,`), "data=value"},
		{"custom data empty object", custom(`"data":{},`), "data=value"},
		{"custom data empty array", custom(`"data":[],`), "data=value"},
		{"custom_message details absent", message(""), "details=absent"},
		{"custom_message details null", message(`"details":null,`), "details=null"},
		{"custom_message details object", message(`"details":{"k":1},`), "details=value"},
		{"custom_message display absent", `{"type":"custom_message","customType":"t","content":"x",` + base + `}`, "details=absent"},
		{"custom_message display false", `{"type":"custom_message","customType":"t","content":"x","display":false,` + base + `}`, "details=absent"},
		{"custom_message content absent", `{"type":"custom_message","customType":"t","display":true,` + base + `}`, "details=absent"},
		{"custom_message content null", `{"type":"custom_message","customType":"t","content":null,"display":true,` + base + `}`, "details=absent"},
		{"compaction members absent", compaction(""), "details=absent,usage=absent"},
		{"compaction members null", compaction(`"details":null,"usage":null,`), "details=null,usage=null"},
		{"compaction members value", compaction(`"details":{"k":1},"usage":` + usage + `,`), "details=value,usage=value"},
		{"compaction details null, usage value", compaction(`"details":null,"usage":` + usage + `,`), "details=null,usage=value"},
		{"compaction details value, usage null", compaction(`"details":{"k":1},"usage":null,`), "details=value,usage=null"},
		{"branch_summary members absent", branch(""), "details=absent,usage=absent"},
		{"branch_summary members null", branch(`"details":null,"usage":null,`), "details=null,usage=null"},
		{"branch_summary members value", branch(`"details":{"k":1},"usage":` + usage + `,`), "details=value,usage=value"},
		{"branch_summary fromHook absent", `{"type":"branch_summary",` + base + `,"fromId":"cccccccc","summary":"s"}`, "details=absent,usage=absent"},
		{"label absent", label(""), "label=absent"},
		{"label null", label(`,"label":null`), "label=null"},
		{"label empty", label(`,"label":""`), "label=value"},
		{"label value", label(`,"label":"x"`), "label=value"},
	}
	state := func(value any) string {
		switch {
		case value == nil:
			return "absent"
		case orderedjson.IsNull(value):
			return "null"
		}
		return "value"
	}
	pointer := func(set, null bool) string {
		switch {
		case set && null:
			return "value marked null"
		case set:
			return "value"
		case null:
			return "null"
		}
		return "absent"
	}
	// view is what a Go reader of the entry's fields is told, and rebuilt is the entry with those fields and no record.
	view := func(entry SessionEntry) (string, SessionEntry) {
		switch e := entry.(type) {
		case CustomEntry:
			e.SessionEntryBase = e.Base()
			return "data=" + state(e.Data), e
		case CustomMessageEntry:
			e.SessionEntryBase = e.Base()
			return "details=" + state(e.Details), e
		case CompactionEntry:
			e.SessionEntryBase = e.Base()
			return "details=" + state(e.Details) + ",usage=" + pointer(e.Usage != nil, e.UsageNull), e
		case BranchSummaryEntry:
			e.SessionEntryBase = e.Base()
			return "details=" + state(e.Details) + ",usage=" + pointer(e.Usage != nil, e.UsageNull), e
		case LabelEntry:
			e.SessionEntryBase = e.Base()
			return "label=" + pointer(e.Label != nil, e.LabelNull), e
		}
		return fmt.Sprintf("%T", entry), entry
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decoded := DecodeSessionEntry([]byte(tc.record))
			if got := string(decoded.Raw()); got != tc.record {
				t.Errorf("the decoded entry writes %s, want the record %s", got, tc.record)
			}
			got, rebuilt := view(decoded)
			if got != tc.view {
				t.Errorf("a reader of the fields gets %s, want %s", got, tc.view)
			}
			encoded, err := rebuilt.MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != tc.record {
				t.Errorf("an entry built from the fields writes %s, want %s", encoded, tc.record)
			}
		})
	}
}

// Pi stores the usage object an extension supplies (agent-session.ts compact, navigateTree, _applyBoundaryDrafts) and JSON.stringify writes it member for
// member; a usage PiG built is written from the typed Usage, and a null one as null.
func TestUsageMemberIsWrittenAsItsAuthorWroteIt(t *testing.T) {
	const authored = `{"cost":{"total":0,"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"totalTokens":3,"output":2,"input":1,"cacheRead":0,"cacheWrite":0,"extra":1}`
	const typed = `{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}`
	var usage ai.Usage
	if err := json.Unmarshal([]byte(authored), &usage); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		usage *ai.Usage
		raw   json.RawMessage
		null  bool
		want  string
	}{
		{"authored object", &usage, json.RawMessage(authored), false, `"usage":` + authored},
		{"built usage", &usage, nil, false, `"usage":` + typed},
		{"null", nil, nil, true, `"usage":null`},
		{"absent", nil, nil, false, ""},
		{"an authored object without a usage is not written", nil, json.RawMessage(authored), false, ""},
	} {
		for kind, entry := range map[string]json.Marshaler{
			"compaction":     CompactionEntry{SessionEntryBase: SessionEntryBase{Type: "compaction", ID: "a"}, Usage: tc.usage, UsageRaw: tc.raw, UsageNull: tc.null},
			"branch_summary": BranchSummaryEntry{SessionEntryBase: SessionEntryBase{Type: "branch_summary", ID: "a"}, Usage: tc.usage, UsageRaw: tc.raw, UsageNull: tc.null},
		} {
			encoded, err := entry.MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if strings.Contains(string(encoded), `"usage"`) {
					t.Errorf("%s, %s: %s has a usage member", tc.name, kind, encoded)
				}
			} else if !strings.Contains(string(encoded), tc.want+`,"fromHook"`) {
				t.Errorf("%s, %s: %s does not hold %s", tc.name, kind, encoded, tc.want)
			}
		}
	}
}
