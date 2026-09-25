package session_test

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

// Compile-time counterparts of the upstream types.test.ts signature checks.
var (
	_ session.Storage                                    = (*session.MemoryStorage)(nil)
	_ session.Session                                    = (*session.StorageBackedSession)(nil)
	_ session.SessionReader                              = session.Session(nil)
	_ session.SessionMutator                             = session.SessionMutation(nil)
	_ session.IdGenerator                                = session.IdGeneratorFunc(nil)
	_ func() session.Value[session.LaneConfiguration]    = func() session.Value[session.LaneConfiguration] { return session.LaneConfig("main") }
	_ func() session.ValueList[ai.AssistantMessageFrame] = func() session.ValueList[ai.AssistantMessageFrame] {
		return session.PendingAssistantFrames("operation", "response")
	}
	_ func(session.Value[session.LaneConfiguration], session.LaneConfiguration) session.ValueSetWrite = session.SetValue[session.LaneConfiguration]
)

func TestDurableDiscriminantsMatchUpstreamUnions(t *testing.T) {
	cases := []struct {
		name string
		got  []string
		want []string
	}{
		{"intent kinds", []string{session.OperationKindRun, session.OperationKindCompaction, session.OperationKindNavigation}, []string{"run", "compaction", "navigation"}},
		{"control", []string{session.ControlRunning, session.ControlCancelRequested}, []string{"running", "cancel_requested"}},
		{"tool call status", []string{session.ToolCallPlanned, session.ToolCallEffectPending, session.ToolCallOutcomeReady, session.ToolCallCompleted}, []string{"planned", "effect_pending", "outcome_ready", "completed"}},
		{"inbox kinds", []string{session.InboxSteer, session.InboxFollowUp, session.InboxNextRun, session.InboxWrite}, []string{"steer", "followUp", "nextRun", "write"}},
		{"terminal status", []string{session.TerminalCompleted, session.TerminalDeclined, session.TerminalAborted, session.TerminalFailed}, []string{"completed", "declined", "aborted", "failed"}},
		{"entry types", []string{string(session.EntryTypeMessage), string(session.EntryTypeCompaction), string(session.EntryTypeBranchSummary), string(session.EntryTypeCustom)}, []string{"message", "compaction", "branch_summary", "custom"}},
	}
	for _, testCase := range cases {
		if !reflect.DeepEqual(testCase.got, testCase.want) {
			t.Errorf("%s = %v, want %v", testCase.name, testCase.got, testCase.want)
		}
	}
	var leaves []string
	for _, at := range session.OperationAts() {
		leaves = append(leaves, string(at))
	}
	want := []string{"starting", "checkpoint", "assistant.ready", "assistant.effect_pending", "assistant.retry_wait", "tools", "deferred.suspended", "deferred.effect_pending", "summary.deciding", "summary.ready", "summary.effect_pending", "summary.retry_wait", "navigation.ready_to_commit"}
	if !reflect.DeepEqual(leaves, want) {
		t.Fatalf("leaves = %v", leaves)
	}
}
