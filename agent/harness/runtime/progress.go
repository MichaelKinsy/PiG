package runtime

// Ports packages/agent/src/harness/runtime/progress.ts.

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

// ReadAssistantFrames reads the committed frame prefix in ascending sequence order without interpreting frames or changing storage.
func ReadAssistantFrames(ctx context.Context, reader session.SessionReader, operationID, responseEntryID string) ([]ai.AssistantMessageFrame, error) {
	address, err := session.NewList[ai.AssistantMessageFrame](session.PendingAssistantFrames("", "").Namespace, operationID+":"+responseEntryID)
	if err != nil {
		return nil, err
	}
	frames := []ai.AssistantMessageFrame{}
	var cursor *session.ListCursor
	// upstream: packages/agent/src/harness/runtime/progress.ts:readAssistantFrames
	const pageSize = 1_000
	for {
		page, err := session.ReadList(ctx, reader, address, &session.ListReadOptions{Order: session.OrderAsc, Limit: new(pageSize), Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, element := range page {
			frames = append(frames, element.Value)
		}
		if len(page) < pageSize {
			return frames, nil
		}
		cursor = &session.ListCursor{Seq: page[len(page)-1].Seq}
	}
}

// ProgressChannel serializes durable progress admission. Seal refuses subsequent writes; Drain returns the latest write's completion or admission error.
type ProgressChannel[T any] struct {
	mu     sync.Mutex
	sealed bool
	latest *progressWrite
	commit func(T) (*CommandJob, error)
}

type progressWrite struct {
	job *CommandJob
	err error
}

// Write admits an ordered progress update without waiting for storage. Drain owns completion of admitted updates.
func (progress *ProgressChannel[T]) Write(item T) {
	progress.mu.Lock()
	defer progress.mu.Unlock()
	if progress.sealed {
		return
	}
	job, err := progress.commit(item)
	progress.latest = &progressWrite{job: job, err: err}
}

// Seal closes admission without cancelling already queued updates.
func (progress *ProgressChannel[T]) Seal() {
	progress.mu.Lock()
	defer progress.mu.Unlock()
	progress.sealed = true
}

// Drain waits for the latest write and preserves its rejection. Session lifetime owns any earlier commit still in flight after a later admission failure.
func (progress *ProgressChannel[T]) Drain() error {
	progress.mu.Lock()
	latest := progress.latest
	progress.mu.Unlock()
	if latest == nil {
		return nil
	}
	if latest.err != nil {
		return latest.err
	}
	_, err := latest.job.Wait()
	return err
}

func openProgress[T any](lane *Lane, drive *Drive, commitWrite func(T) session.Write, stillOwns func(LaneState) bool) *ProgressChannel[T] {
	return &ProgressChannel[T]{commit: func(item T) (*CommandJob, error) {
		return lane.CommandAsync(drive.Context, func(state LaneState, _ session.SessionReader) (LaneCommand[any], error) {
			if !stillOwns(state) {
				return LaneCommand[any]{Kind: CommandReturn}, nil
			}
			return LaneCommand[any]{Kind: CommandCommit, Writes: []session.Write{commitWrite(item)}, Next: state, Materialize: func(session.CommitResult) any { return nil }}, nil
		})
	}}
}

// OpenFrameProgress appends frames only while the lane's authoritative phase owns this response.
func OpenFrameProgress(lane *Lane, drive *Drive, responseEntryID string) *ProgressChannel[ai.AssistantMessageFrame] {
	address := session.PendingAssistantFrames(drive.OperationID, responseEntryID)
	return openProgress(lane, drive, func(frame ai.AssistantMessageFrame) session.Write { return session.AppendList(address, frame) }, func(state LaneState) bool {
		if state.Operation == nil {
			return false
		}
		operation := state.Operation.State
		return (operation.At == session.AtAssistantEffectPending || operation.At == session.AtDeferredEffectPending) && operation.ResponseEntryID == responseEntryID
	})
}

// OpenToolProgress replaces the checkpoint only while the authoritative tool batch owns the invocation.
func OpenToolProgress(lane *Lane, drive *Drive, turnID string, sourceIndex int, invocationID string) *ProgressChannel[harness.AgentToolResult] {
	address := session.PendingToolOutput(drive.OperationID, invocationID)
	return openProgress(lane, drive, func(result harness.AgentToolResult) session.Write { return session.SetValue(address, result) }, func(state LaneState) bool {
		if state.Operation == nil || state.Operation.State.At != session.AtTools {
			return false
		}
		batch := state.Operation.State.Batch
		if batch.TurnID != turnID {
			return false
		}
		for _, call := range batch.Calls {
			if call.SourceIndex == sourceIndex && call.ResultEntryID == invocationID && call.Status == session.ToolCallEffectPending {
				return true
			}
		}
		return false
	})
}
