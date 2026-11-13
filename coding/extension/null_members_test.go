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
