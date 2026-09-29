package runtime

// Ports packages/agent/src/harness/runtime/drive/terminal.ts.

import (
	"sync"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
)

// OperationCleanupWrites builds the operation-owned suffix of a terminal transaction without changing lane-owned input.
func OperationCleanupWrites(ctx harness.Context, reader session.SessionReader, operationID string, state session.OperationState) ([]session.Write, error) {
	prefixes := []session.StoredAddressBase{
		session.OperationToolArgsPrefix(operationID, nil).Address(),
		session.OperationToolMemoPrefix(operationID, nil).Address(),
		session.OperationPreparationPrefix(operationID).Address(),
		session.PendingToolOutputPrefix(operationID).Address(),
	}
	values := make([][]session.StoredValue[any], len(prefixes))
	errs := make([]error, len(prefixes))
	var work sync.WaitGroup
	for index, prefix := range prefixes {
		work.Go(func() { values[index], errs[index] = reader.ScanValues(ctx, prefix) })
	}
	work.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	writes := []session.Write{
		session.DeleteValue(session.OperationMetaValue(operationID)),
		session.DeleteValue(session.OperationStateValue(operationID)),
	}
	for _, family := range values {
		for _, value := range family {
			writes = append(writes, session.DeleteValue(value.Address))
		}
	}
	if state.At == session.AtAssistantEffectPending || state.At == session.AtDeferredEffectPending {
		writes = append(writes, session.DeleteList(session.PendingAssistantFrames(operationID, state.ResponseEntryID)))
	}
	if state.At == session.AtTools {
		seen := make(map[string]bool)
		for _, call := range state.Batch.Calls {
			if call.Status == session.ToolCallOutcomeReady && !seen[call.ResultEntryID] {
				seen[call.ResultEntryID] = true
				writes = append(writes, session.DeleteValue(session.PendingEntryValue(call.ResultEntryID)))
			}
		}
	}
	return writes, nil
}

// OperationResultRecord constructs the immutable terminal observation; only a failed result carries an error.
func OperationResultRecord(meta session.OperationMeta, status string, tipID *string, operationError *session.OperationError) (session.OperationResultRecord, error) {
	if (status == session.TerminalFailed) != (operationError != nil) {
		return session.OperationResultRecord{}, &session.SessionInvariantError{Message: "Only a failed operation result may carry an error"}
	}
	return session.OperationResultRecord{
		OperationID: meta.OperationID, Kind: meta.Intent.Kind, Status: status, Error: operationError,
		FromTipID: meta.SourceTipID, TipID: tipID, StartedAt: meta.StartedAt, EndedAt: runtimeNow(),
	}, nil
}
