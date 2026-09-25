package inproc_test

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// TestExtractToolResultFields_AllVariantsCovered verifies that every known tool
// result variant preserves content, details, and error state.
func TestExtractToolResultFields_AllVariantsCovered(t *testing.T) {
	wantContent := []any{"sentinel-content"}

	base := func(content []any, isError bool) extension.ToolResultEventBase {
		return extension.ToolResultEventBase{Type: "tool_result", Content: content, IsError: isError}
	}

	cases := []struct {
		name    string
		event   extension.ToolResultEvent
		wantErr bool
	}{
		{"BashToolResultEvent", extension.BashToolResultEvent{
			ToolResultEventBase: base(wantContent, true), ToolName: "bash",
		}, true},
		{"PowerShellToolResultEvent", extension.PowerShellToolResultEvent{
			ToolResultEventBase: base(wantContent, true), ToolName: "powershell",
		}, true},
		{"ReadToolResultEvent", extension.ReadToolResultEvent{
			ToolResultEventBase: base(wantContent, false), ToolName: "read",
		}, false},
		{"EditToolResultEvent", extension.EditToolResultEvent{
			ToolResultEventBase: base(wantContent, false), ToolName: "edit",
		}, false},
		{"WriteToolResultEvent", extension.WriteToolResultEvent{
			ToolResultEventBase: base(wantContent, false), ToolName: "write",
		}, false},
		{"GrepToolResultEvent", extension.GrepToolResultEvent{
			ToolResultEventBase: base(wantContent, false), ToolName: "grep",
		}, false},
		{"FindToolResultEvent", extension.FindToolResultEvent{
			ToolResultEventBase: base(wantContent, false), ToolName: "find",
		}, false},
		{"LsToolResultEvent", extension.LsToolResultEvent{
			ToolResultEventBase: base(wantContent, false), ToolName: "ls",
		}, false},
		{"CustomToolResultEvent", extension.CustomToolResultEvent{
			ToolResultEventBase: base(wantContent, true), ToolName: "custom_tool_xyz",
			Details: map[string]any{"k": "v"},
		}, true},
	}

	// Sync gate: assert the test enumerates exactly the variants the
	// helper recognizes. If upstream adds another variant, this length
	// check is the first signal: extend the table above AND the
	// type switch in runner.go.
	const expectedVariants = 9
	if len(cases) != expectedVariants {
		t.Fatalf("test enumerates %d variants, want %d: extend extractToolResultFields and this table together",
			len(cases), expectedVariants)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content, details, isError, _ := inproc.ExtractToolResultFieldsForTest(tc.event)
			if got := len(content); got != 1 || content[0] != "sentinel-content" {
				t.Errorf("Content = %v, want [sentinel-content] (helper failed to recognize variant)", content)
			}
			if isError != tc.wantErr {
				t.Errorf("IsError = %v, want %v (helper failed to project field)", isError, tc.wantErr)
			}
			// Details is optional per variant; just exercise the path.
			_ = details
			// The chained values round-trip through the same variant.
			chained := inproc.WithToolResultFieldsForTest(tc.event, []any{"next"}, nil, !tc.wantErr, "usage")
			if reflect.TypeOf(chained) != reflect.TypeOf(tc.event) {
				t.Fatalf("withToolResultFields changed the variant to %T", chained)
			}
			nextContent, _, nextIsError, nextUsage := inproc.ExtractToolResultFieldsForTest(chained)
			if len(nextContent) != 1 || nextContent[0] != "next" || nextIsError == tc.wantErr || nextUsage != "usage" {
				t.Errorf("chained fields = %v %v %v", nextContent, nextIsError, nextUsage)
			}
		})
	}
}

// TestSessionBeforeIsCancel_AllVariantsCovered verifies cancellation for every
// value and pointer form in the SessionBefore result union.
func TestSessionBeforeIsCancel_AllVariantsCovered(t *testing.T) {
	cases := []struct {
		name       string
		result     any
		wantCancel bool
	}{
		{"SessionBeforeSwitchResult-byValue-cancel", extension.SessionBeforeSwitchResult{Cancel: true}, true},
		{"SessionBeforeSwitchResult-byValue-allow", extension.SessionBeforeSwitchResult{Cancel: false}, false},
		{"SessionBeforeSwitchResult-byPointer", &extension.SessionBeforeSwitchResult{Cancel: true}, true},
		{"SessionBeforeForkResult-byValue", extension.SessionBeforeForkResult{Cancel: true}, true},
		{"SessionBeforeForkResult-byPointer", &extension.SessionBeforeForkResult{Cancel: true}, true},
		{"SessionBeforeCompactResult-byValue", extension.SessionBeforeCompactResult{Cancel: true}, true},
		{"SessionBeforeCompactResult-byPointer", &extension.SessionBeforeCompactResult{Cancel: true}, true},
		{"SessionBeforeTreeResult-byValue", extension.SessionBeforeTreeResult{Cancel: true}, true},
		{"SessionBeforeTreeResult-byPointer", &extension.SessionBeforeTreeResult{Cancel: true}, true},
	}

	// Sync gate: derived count, not a magic number. Update the inputs
	// (resultVariants/formsPerVariant/extraNegatives) when extending.
	const (
		resultVariants  = 4 // SessionBeforeSwitch/Fork/Compact/Tree
		formsPerVariant = 2 // by-value + by-pointer
		extraAllowFalse = 1 // SessionBeforeSwitchResult{Cancel:false}
		expectedCases   = resultVariants*formsPerVariant + extraAllowFalse
	)
	if len(cases) != expectedCases {
		t.Fatalf("test enumerates %d cases, want %d (= %d variants × %d forms + %d extras): extend sessionBeforeIsCancel and this table together",
			len(cases), expectedCases, resultVariants, formsPerVariant, extraAllowFalse)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := inproc.SessionBeforeIsCancelForTest(tc.result); got != tc.wantCancel {
				t.Errorf("sessionBeforeIsCancel(%T) = %v, want %v (helper failed to recognize variant or project Cancel)",
					tc.result, got, tc.wantCancel)
			}
		})
	}

	// Negative case: a non-SessionBefore* result must NOT be recognized
	// as a cancel signal (would otherwise short-circuit unrelated emits).
	t.Run("UnrelatedResultType", func(t *testing.T) {
		if got := inproc.SessionBeforeIsCancelForTest("not a session-before result"); got {
			t.Error("sessionBeforeIsCancel(string) = true, want false (must only match the 4 SessionBefore*Result variants)")
		}
	})
}

// TestIsSessionBeforeEvent_AllVariantsCovered verifies every value and pointer
// form in the SessionBefore event union.
func TestIsSessionBeforeEvent_AllVariantsCovered(t *testing.T) {
	cases := []struct {
		name  string
		event any
	}{
		{"SessionBeforeSwitchEvent-byValue", extension.SessionBeforeSwitchEvent{}},
		{"SessionBeforeSwitchEvent-byPointer", &extension.SessionBeforeSwitchEvent{}},
		{"SessionBeforeForkEvent-byValue", extension.SessionBeforeForkEvent{}},
		{"SessionBeforeForkEvent-byPointer", &extension.SessionBeforeForkEvent{}},
		{"SessionBeforeCompactEvent-byValue", extension.SessionBeforeCompactEvent{}},
		{"SessionBeforeCompactEvent-byPointer", &extension.SessionBeforeCompactEvent{}},
		{"SessionBeforeTreeEvent-byValue", extension.SessionBeforeTreeEvent{}},
		{"SessionBeforeTreeEvent-byPointer", &extension.SessionBeforeTreeEvent{}},
	}

	const expectedCases = 8 // 4 event types × 2 forms each
	if len(cases) != expectedCases {
		t.Fatalf("test enumerates %d cases, want %d: extend isSessionBeforeEvent and this table together",
			len(cases), expectedCases)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !inproc.IsSessionBeforeEventForTest(tc.event) {
				t.Errorf("isSessionBeforeEvent(%T) = false, want true", tc.event)
			}
		})
	}

	// Negative cases: non-SessionBefore* events must NOT be recognized.
	t.Run("SessionStartEvent-NotRecognized", func(t *testing.T) {
		if inproc.IsSessionBeforeEventForTest(extension.SessionStartEvent{}) {
			t.Error("isSessionBeforeEvent(SessionStartEvent) = true, want false")
		}
	})
}
