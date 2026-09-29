package runtime

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/compaction"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
)

func requireHarnessFault(t *testing.T, err error) {
	t.Helper()
	if _, ok := err.(*harness.HarnessFault); !ok { //nolint:errorlint // Upstream toBeInstanceOf checks the outer error, not a wrapped cause.
		t.Fatalf("error = %v (%T), want HarnessFault", err, err)
	}
}

func restoreScope() session.OperationScope {
	return session.OperationScope{Control: session.Control{Status: session.ControlRunning}, Settings: session.RunSettings{Compaction: compaction.DefaultCompactionSettings, SteeringMode: agent.QueueModeAll, FollowUpMode: agent.QueueModeAll, ToolExecution: session.ToolExecutionParallel}}
}

func restoreCheckpoint(trigger string) session.OperationState {
	return session.OperationState{OperationScope: restoreScope(), At: session.AtCheckpoint, Continuation: session.Continuation{Kind: session.ContinuationNeedAssistant}, TriggerEntryID: trigger}
}

func restoreLaneWrites(name string, configuration session.LaneConfiguration) []session.Write {
	return []session.Write{
		session.SetValue(session.BranchTip(name), (*string)(nil)),
		session.SetValue(session.LaneConfig(name), configuration),
		session.SetValue(session.LaneStateValue(name), session.LaneState{Inbox: []session.InboxItem{}}),
	}
}

func restoreFixture(t *testing.T) session.Session {
	t.Helper()
	isolateHarnessTest(t)
	repo := session.NewMemorySessionRepo(nil)
	t.Cleanup(func() { requireHarnessOK(t, repo.Close(context.Background())) })
	opened, err := repo.Create(context.Background(), session.SessionCreateOptions{})
	requireHarnessOK(t, err)
	harnessCommit(t, opened, restoreLaneWrites("main", laneTestConfiguration())...)
	return opened
}

func storeCurrentOperation(t *testing.T, opened session.Session, meta session.OperationMeta, state session.OperationState) {
	t.Helper()
	harnessCommit(t, opened,
		session.SetValue(session.OperationMetaValue(meta.OperationID), meta),
		session.SetValue(session.OperationStateValue(meta.OperationID), state),
		session.SetValue(session.LaneStateValue(meta.Lane), session.LaneState{CurrentOperationID: &meta.OperationID, Inbox: []session.InboxItem{}}),
	)
}
