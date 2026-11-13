package extension

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// Pi appends a boundary draft's members as the handler wrote them (agent-session.ts:1007-1045 _applyBoundaryDrafts) and hands the same drafts to the next handler,
// so a draft decodes with a null `data`, `details` or `usage` apart from an absent one and encodes back as written.
func TestBoundaryDraftKeepsNullMembersApartFromAbsent(t *testing.T) {
	const usage = `{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}`
	for _, tc := range []struct{ name, text, view string }{
		{"custom data null", `{"type":"custom","customType":"t","data":null}`, "data=null"},
		{"custom data absent", `{"type":"custom","customType":"t"}`, "data=absent"},
		{"custom data value", `{"type":"custom","customType":"t","data":{"b":1,"a":2}}`, "data=value"},
		{"custom_message details null", `{"type":"custom_message","customType":"t","content":"x","display":false,"details":null}`, "details=null"},
		{"custom_message details absent", `{"type":"custom_message","customType":"t","content":"x","display":false}`, "details=absent"},
		{"custom_message display absent", `{"type":"custom_message","customType":"t","content":"x"}`, "details=absent"},
		{"custom_message content absent", `{"type":"custom_message","customType":"t","display":true}`, "details=absent"},
		{"custom_message content null", `{"type":"custom_message","customType":"t","content":null,"display":false}`, "details=absent"},
		{"compaction members null", `{"type":"compaction","summary":"s","firstKeptEntryId":null,"details":null,"usage":null}`, "details=null,usage=null"},
		{"compaction members absent", `{"type":"compaction","summary":"s","firstKeptEntryId":null}`, "details=absent,usage=absent"},
		{"compaction members value", `{"type":"compaction","summary":"s","firstKeptEntryId":"aaaaaaaa","details":{"k":1},"usage":` + usage + `}`, "details=value,usage=value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var draft SessionBoundaryDraft
			if err := json.Unmarshal([]byte(tc.text), &draft); err != nil {
				t.Fatal(err)
			}
			var view string
			switch draft.Type {
			case SessionBoundaryDraftCustom:
				view = "data=" + nullMemberState(draft.Data)
			case SessionBoundaryDraftCustomMessage:
				view = "details=" + nullMemberState(draft.Details)
			case SessionBoundaryDraftCompaction:
				usageState := map[bool]string{true: "null", false: "absent"}[draft.UsageNull]
				if draft.Usage != nil {
					usageState = "value"
				}
				view = "details=" + nullMemberState(draft.Details) + ",usage=" + usageState
			}
			if view != tc.view {
				t.Errorf("the draft's fields say %s, want %s", view, tc.view)
			}
			encoded, err := json.Marshal(draft)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != tc.text {
				t.Errorf("the draft encodes as %s, want %s", encoded, tc.text)
			}
		})
	}
}

func nullMemberState(value any) string {
	switch {
	case value == nil:
		return "absent"
	case orderedjson.IsNull(value):
		return "null"
	}
	return "value"
}

// Every result or call argument an extension writes that reaches a session entry decodes with a null member apart from an absent one: Pi hands the value on as
// written (agent-session.ts compact `details = extensionCompaction.details`, navigateTree `summaryDetails = extensionSummary.details`, sendCustomMessage
// `details: message.details`; runner.ts emitToolResult `handlerResult.details !== undefined`).
func TestExtensionWrittenMembersKeepNullApartFromAbsent(t *testing.T) {
	decode := func(t *testing.T, text string, target any) {
		t.Helper()
		if err := json.Unmarshal([]byte(text), target); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, null, absent string
		member             func(text string) any
	}{
		{"session_before_compact result details", `{"summary":"s","firstKeptEntryId":"a","tokensBefore":1,"details":null}`, `{"summary":"s","firstKeptEntryId":"a","tokensBefore":1}`, func(text string) any {
			var result CompactionResult
			decode(t, text, &result)
			return result.Details
		}},
		{"session_before_tree summary details", `{"summary":"s","details":null}`, `{"summary":"s"}`, func(text string) any {
			var summary SessionBeforeTreeResultSummary
			decode(t, text, &summary)
			return summary.Details
		}},
		{"sendMessage message details", `{"customType":"t","content":"x","display":true,"details":null}`, `{"customType":"t","content":"x","display":true}`, func(text string) any {
			var message CustomMessageRef
			decode(t, text, &message)
			return message.Details
		}},
		{"sendMessage payload details", `{"customType":"t","content":"x","display":true,"details":null}`, `{"customType":"t","content":"x","display":true}`, func(text string) any {
			var payload SendMessagePayload
			decode(t, text, &payload)
			return payload.Details
		}},
		{"tool_result handler result details", `{"details":null}`, `{}`, func(text string) any {
			var result ToolResultEventResult
			decode(t, text, &result)
			return result.Details
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := nullMemberState(tc.member(tc.null)); got != "null" {
				t.Errorf("a member written as null decodes as %s", got)
			}
			if got := nullMemberState(tc.member(tc.absent)); got != "absent" {
				t.Errorf("a member left out decodes as %s", got)
			}
		})
	}
	var summary SessionBeforeTreeResultSummary
	decode(t, `{"summary":"s","usage":null}`, &summary)
	if summary.Usage != nil || !summary.UsageNull {
		t.Errorf("a summary usage written as null: Usage %v, UsageNull %t", summary.Usage, summary.UsageNull)
	}
	decode(t, `{"summary":"s"}`, &summary)
	if summary.Usage != nil || summary.UsageNull {
		t.Errorf("a summary without usage: Usage %v, UsageNull %t", summary.Usage, summary.UsageNull)
	}
}

// The three decoders that see an extension's usage keep the object as written beside the decoded usage: a compaction draft, which also hands it to the next
// handler as written, and the summary of a session_before_tree result. Numbers are written as JSON.stringify writes them.
func TestExtensionUsageKeepsTheAuthorsObject(t *testing.T) {
	const authored = `{"cost":{"total":0,"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"totalTokens":3,"output":2,"input":1,"cacheRead":0,"cacheWrite":0,"extra":1}`
	const spaced = `{ "cost": {"total": 0.0, "input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "totalTokens": 3, "output": 2, "input": 1e0, "cacheRead": 0, "cacheWrite": 0, "extra": 1 }`
	for _, written := range []string{authored, spaced} {
		var draft SessionBoundaryDraft
		if err := json.Unmarshal([]byte(`{"type":"compaction","summary":"s","firstKeptEntryId":null,"usage":`+written+`}`), &draft); err != nil {
			t.Fatal(err)
		}
		if draft.Usage == nil || draft.Usage.Input != 1 || draft.Usage.Output != 2 || draft.Usage.TotalTokens != 3 || string(draft.UsageRaw) != authored {
			t.Errorf("draft usage = %+v, raw %s; want the decoded usage and %s", draft.Usage, draft.UsageRaw, authored)
		}
		encoded, err := json.Marshal(draft)
		if err != nil {
			t.Fatal(err)
		}
		if want := `{"type":"compaction","summary":"s","firstKeptEntryId":null,"usage":` + authored + `}`; string(encoded) != want {
			t.Errorf("the next handler is handed %s, want %s", encoded, want)
		}
		var summary SessionBeforeTreeResultSummary
		if err := json.Unmarshal([]byte(`{"summary":"s","usage":`+written+`}`), &summary); err != nil {
			t.Fatal(err)
		}
		if summary.Usage == nil || summary.Usage.Input != 1 || string(summary.UsageRaw) != authored {
			t.Errorf("summary usage = %+v, raw %s; want the decoded usage and %s", summary.Usage, summary.UsageRaw, authored)
		}
	}
	var draft SessionBoundaryDraft
	if err := json.Unmarshal([]byte(`{"type":"compaction","summary":"s","firstKeptEntryId":null,"usage":null}`), &draft); err != nil || draft.UsageRaw != nil || !draft.UsageNull {
		t.Errorf("a null usage decodes as raw %s, null %v, err %v", draft.UsageRaw, draft.UsageNull, err)
	}
}
