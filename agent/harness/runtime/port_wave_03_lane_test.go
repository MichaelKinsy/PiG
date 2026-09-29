package runtime

import (
	"errors"
	"reflect"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/require"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

func TestPortWave03Lane(t *testing.T) {
	// upstream: packages/agent/test/harness/runtime/lane.test.ts:114
	t.Run("reads and replaces configuration from owned state", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			lane, model, opened, _ := newCommandLane(t, nil)
			ctx := t.Context()
			names := []string{"read"}
			require.NoError(t, lane.SetModel(ctx, agentharness.ModelIdentity{Provider: model.ProviderMeta.ProviderID, ModelID: model.ID}))
			require.NoError(t, lane.SetThinkingLevel(ctx, ai.ThinkingHigh))
			require.NoError(t, lane.SetActiveTools(ctx, names))
			gotModel, err := lane.GetModel(ctx)
			require.NoError(t, err)
			require.Same(t, model, gotModel)
			thinking, err := lane.GetThinkingLevel(ctx)
			require.NoError(t, err)
			require.Equal(t, ai.ThinkingHigh, thinking)
			gotNames, err := lane.GetActiveTools(ctx)
			require.NoError(t, err)
			require.Equal(t, names, gotNames)
			require.Same(t, &names[0], &gotNames[0])
			config, err := session.GetValue(ctx, opened, session.LaneConfig("main"))
			require.NoError(t, err)
			require.NotNil(t, config)
			require.Equal(t, session.LaneConfiguration{Model: session.ModelRef{Provider: model.ProviderMeta.ProviderID, ModelID: model.ID}, ThinkingLevel: ai.ThinkingHigh, ActiveToolNames: names}, config.Value)
		})
	})
	// upstream: packages/agent/test/harness/runtime/lane.test.ts:132
	t.Run("derives queued configuration updates from the latest committed state", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			lane, model, _, storage := newCommandLane(t, nil)
			ctx := t.Context()
			started, release := make(chan struct{}), laneTestReleaseChannel(t)
			storage.beforeCommit(func() error { close(started); <-release; return nil })
			modelUpdate := make(chan error, 1)
			go func() {
				modelUpdate <- lane.SetModel(ctx, agentharness.ModelIdentity{Provider: model.ProviderMeta.ProviderID, ModelID: model.ID})
			}()
			<-started
			thinkingUpdate := make(chan error, 1)
			go func() { thinkingUpdate <- lane.SetThinkingLevel(ctx, ai.ThinkingHigh) }()
			synctest.Wait()
			close(release)
			require.NoError(t, <-modelUpdate)
			require.NoError(t, <-thinkingUpdate)
			require.Equal(t, session.LaneConfiguration{Model: session.ModelRef{Provider: model.ProviderMeta.ProviderID, ModelID: model.ID}, ThinkingLevel: ai.ThinkingHigh, ActiveToolNames: []string{}}, lane.SnapshotState().Configuration)
		})
	})
	// upstream: packages/agent/test/harness/runtime/lane.test.ts:154
	t.Run("returns a promise value without holding the lane line", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			lane, _, _, _ := newCommandLane(t, nil)
			ctx := t.Context()
			release := laneTestReleaseChannel(t)
			line := &session.MutationLine{}
			completion := line.Enqueue(func() (any, error) { <-release; return nil, nil })
			joined := make(chan error, 1)
			go func() {
				_, err := lane.Command(ctx, func(LaneState, session.SessionReader) (LaneCommand[any], error) {
					return LaneCommand[any]{Kind: CommandReturn, Result: completion}, nil
				})
				joined <- err
			}()
			synctest.Wait()
			require.NoError(t, lane.SetThinkingLevel(ctx, ai.ThinkingHigh))
			select {
			case err := <-joined:
				t.Fatalf("promise completed before release: %v", err)
			default:
			}
			close(release)
			require.NoError(t, <-joined)
		})
	})
	// upstream: packages/agent/test/harness/runtime/lane.test.ts:170
	t.Run("returns an expected rejection without faulting the lane", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			lane, _, _, _ := newCommandLane(t, nil)
			ctx := t.Context()
			rejection := errors.New("declined")
			_, err := lane.Command(ctx, func(LaneState, session.SessionReader) (LaneCommand[any], error) {
				return LaneCommand[any]{Kind: CommandReject, Error: rejection}, nil
			})
			require.Same(t, rejection, err)
			tip, err := lane.GetTipID(ctx)
			require.NoError(t, err)
			require.Nil(t, tip)
			require.NoError(t, lane.SetThinkingLevel(ctx, ai.ThinkingHigh))
		})
	})
	// upstream: packages/agent/test/harness/runtime/lane.test.ts:181
	t.Run("passes bounded reads and commit metadata through the serialized command", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			lane, _, _, _ := newCommandLane(t, nil)
			ctx := t.Context()
			var stored *session.StoredValue[session.LaneConfiguration]
			published := false
			commit, err := Command(ctx, lane, func(state LaneState, reader session.SessionReader) (LaneCommand[session.CommitResult], error) {
				var err error
				stored, err = session.GetValue(ctx, reader, session.LaneConfig("main"))
				if err != nil {
					return LaneCommand[session.CommitResult]{}, err
				}
				next := state
				next.Configuration.ThinkingLevel = ai.ThinkingHigh
				return LaneCommand[session.CommitResult]{Kind: CommandCommit, Writes: []session.Write{session.SetValue(session.LaneConfig("main"), next.Configuration)}, Next: next, Materialize: func(commit session.CommitResult) session.CommitResult {
					published = reflect.DeepEqual(lane.SnapshotState(), next)
					return commit
				}}, nil
			})
			require.NoError(t, err)
			require.NotNil(t, stored)
			require.Equal(t, laneTestConfiguration(), stored.Value)
			require.True(t, published)
			require.Len(t, commit.Seqs, 1)
			require.IsType(t, int64(0), commit.Timestamp)
		})
	})
	// upstream: packages/agent/test/harness/runtime/lane.test.ts:207
	t.Run("rejects thenable materialization after publishing committed memory", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			lane, _, opened, _ := newCommandLane(t, nil)
			ctx := t.Context()
			line := &session.MutationLine{}
			completion := line.Enqueue(func() (any, error) { return nil, nil })
			_, err := lane.Command(ctx, func(state LaneState, _ session.SessionReader) (LaneCommand[any], error) {
				next := state
				next.Configuration.ThinkingLevel = ai.ThinkingHigh
				return LaneCommand[any]{Kind: CommandCommit, Writes: []session.Write{session.SetValue(session.LaneConfig("main"), next.Configuration)}, Next: next, Materialize: func(session.CommitResult) any { return completion }}, nil
			})
			require.EqualError(t, err, "Lane command materialize() must be synchronous")
			_, err = completion.Wait()
			require.NoError(t, err)
			require.Equal(t, ai.ThinkingHigh, lane.SnapshotState().Configuration.ThinkingLevel)
			stored, err := session.GetValue(ctx, opened, session.LaneConfig("main"))
			require.NoError(t, err)
			require.NotNil(t, stored)
			require.Equal(t, ai.ThinkingHigh, stored.Value.ThinkingLevel)
		})
	})
	// upstream: packages/agent/test/harness/runtime/lane.test.ts:229
	t.Run("preserves committed memory when synchronous event publication fails", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			bus := agentharness.NewHarnessEventBus()
			lane, _, opened, _ := newCommandLane(t, func(ctx harness.Context, events []agentharness.HarnessEvent) (func() error, error) {
				deliver := bus.PrepareBatch(ctx, events)
				return func() error { deliver(); return nil }, nil
			})
			ctx := t.Context()
			uncloneable := agentharness.HarnessEvent{Lane: "main", Payload: uncloneableRunStart{RunStartPayload: agentharness.RunStartPayload{RunID: "run"}, Invalid: func() {}}}
			_, err := lane.Command(ctx, func(state LaneState, _ session.SessionReader) (LaneCommand[any], error) {
				next := state
				next.Configuration.ThinkingLevel = ai.ThinkingHigh
				return LaneCommand[any]{Kind: CommandCommit, Writes: []session.Write{session.SetValue(session.LaneConfig("main"), next.Configuration)}, Next: next, Materialize: func(session.CommitResult) any { return nil }, Events: func(session.CommitResult) []agentharness.HarnessEvent {
					return []agentharness.HarnessEvent{uncloneable}
				}}, nil
			})
			require.Error(t, err)
			// The upstream assertion is the exception's name, not its implementation-specific message.
			require.Equal(t, "DataCloneError", reflect.Indirect(reflect.ValueOf(err)).Type().Name())
			require.Equal(t, ai.ThinkingHigh, lane.SnapshotState().Configuration.ThinkingLevel)
			stored, err := session.GetValue(ctx, opened, session.LaneConfig("main"))
			require.NoError(t, err)
			require.NotNil(t, stored)
			require.Equal(t, ai.ThinkingHigh, stored.Value.ThinkingLevel)
		})
	})
	// upstream: packages/agent/test/harness/runtime/lane.test.ts:258
	t.Run("rejects work after sealing while an admitted commit finishes", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			lane, _, _, storage := newCommandLane(t, nil)
			ctx := t.Context()
			started, release := make(chan struct{}), laneTestReleaseChannel(t)
			storage.beforeCommit(func() error { close(started); <-release; return nil })
			admitted := make(chan error, 1)
			go func() { _, err := setLaneTestThinking(ctx, lane, ai.ThinkingHigh, nil); admitted <- err }()
			<-started
			closed := &harness.HarnessClosed{}
			lane.Seal(closed)
			_, err := lane.GetTipID(ctx)
			require.Same(t, closed, err)
			require.Same(t, closed, lane.SetThinkingLevel(ctx, ai.ThinkingLow))
			close(release)
			require.NoError(t, <-admitted)
			require.Equal(t, ai.ThinkingHigh, lane.SnapshotState().Configuration.ThinkingLevel)
		})
	})
	// upstream: packages/agent/test/harness/runtime/lane.test.ts:279
	t.Run("publishes memory only after the durable commit succeeds", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			lane, _, opened, storage := newCommandLane(t, nil)
			ctx := t.Context()
			started, release := make(chan struct{}), laneTestReleaseChannel(t)
			storage.beforeCommit(func() error { close(started); <-release; return nil })
			result := make(chan ai.ThinkingLevel, 1)
			failure := make(chan error, 1)
			go func() {
				value, err := setLaneTestThinking(ctx, lane, ai.ThinkingHigh, nil)
				result <- value
				failure <- err
			}()
			<-started
			require.Equal(t, ai.ThinkingOff, lane.SnapshotState().Configuration.ThinkingLevel)
			close(release)
			require.Equal(t, ai.ThinkingHigh, <-result)
			require.NoError(t, <-failure)
			require.Equal(t, ai.ThinkingHigh, lane.SnapshotState().Configuration.ThinkingLevel)
			stored, err := session.GetValue(ctx, opened, session.LaneConfig("main"))
			require.NoError(t, err)
			require.NotNil(t, stored)
			require.Equal(t, ai.ThinkingHigh, stored.Value.ThinkingLevel)
		})
	})
	// upstream: packages/agent/test/harness/runtime/lane.test.ts:300
	t.Run("preserves memory when the durable commit fails", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			lane, _, opened, storage := newCommandLane(t, nil)
			ctx := t.Context()
			failure := errors.New("commit failed")
			storage.beforeCommit(func() error { return failure })
			_, err := setLaneTestThinking(ctx, lane, ai.ThinkingHigh, nil)
			require.Same(t, failure, err)
			require.Equal(t, ai.ThinkingOff, lane.SnapshotState().Configuration.ThinkingLevel)
			stored, err := session.GetValue(ctx, opened, session.LaneConfig("main"))
			require.NoError(t, err)
			require.NotNil(t, stored)
			require.Equal(t, ai.ThinkingOff, stored.Value.ThinkingLevel)
		})
	})
	// upstream: packages/agent/test/harness/runtime/lane.test.ts:315
	t.Run("diverts ordinary work but settles against latest cancelled control", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			lane, _, opened, _ := newCommandLane(t, nil)
			ctx := t.Context()
			prompt := "hello"
			_, err := lane.Accept(ctx, agentharness.OperationRequest{Kind: agentharness.RequestPrompt, PromptText: &prompt})
			require.NoError(t, err)
			operation := lane.SnapshotState().Operation
			require.NotNil(t, operation)
			require.Equal(t, session.AtStarting, operation.State.At)
			cancelled := operation.State
			cancelled.Control = session.Control{Status: session.ControlCancelRequested, RequestedAt: 1}
			_, err = lane.Command(ctx, func(state LaneState, _ session.SessionReader) (LaneCommand[any], error) {
				next := state
				next.Operation = &session.Operation{Meta: operation.Meta, State: cancelled}
				return LaneCommand[any]{Kind: CommandCommit, Writes: []session.Write{session.SetValue(session.OperationStateValue(operation.Meta.OperationID), cancelled)}, Next: next, Materialize: func(session.CommitResult) any { return nil }}, nil
			})
			require.NoError(t, err)
			continued := false
			var settledControl string
			result, err := lane.ContinueOperation(ctx, cancelled, func(LaneState, session.OperationState, session.OperationMeta, session.SessionReader) (OperationCommand[any], error) {
				continued = true
				return OperationCommand[any]{Kind: CommandReturn, Result: "continued"}, nil
			})
			require.NoError(t, err)
			require.Equal(t, ContinueOperationResult[any]{CancelRequested: true}, result)
			require.False(t, continued)
			settled, err := SettleOperation(ctx, lane, cancelled, func(state LaneState, current session.OperationState, _ session.OperationMeta, _ session.SessionReader) (OperationCommand[[]session.InboxItem], error) {
				settledControl = current.Control.Status
				inbox := append(append([]session.InboxItem{}, state.Inbox...), session.InboxItem{EntryID: "accepted-during-cancellation", Kind: "write"})
				return OperationCommand[[]session.InboxItem]{Kind: CommandCommit, Writes: []session.Write{}, OperationState: current, Lane: &LanePatch{SetInbox: true, Inbox: inbox}, Materialize: func(session.CommitResult) []session.InboxItem { return inbox }}, nil
			})
			require.NoError(t, err)
			require.Equal(t, session.ControlCancelRequested, settledControl)
			require.Equal(t, []session.InboxItem{{EntryID: "accepted-during-cancellation", Kind: "write"}}, settled)
			require.Equal(t, settled, lane.SnapshotState().Inbox)
			stored, err := session.GetValue(ctx, opened, session.LaneStateValue("main"))
			require.NoError(t, err)
			require.NotNil(t, stored)
			require.Equal(t, settled, stored.Value.Inbox)
		})
	})
	// upstream: packages/agent/test/harness/runtime/lane.test.ts:370
	t.Run("plans queued commands from the latest committed memory", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			lane, _, _, storage := newCommandLane(t, nil)
			ctx := t.Context()
			started, release := make(chan struct{}), laneTestReleaseChannel(t)
			observed := []ai.ThinkingLevel{}
			storage.beforeCommit(func() error { close(started); <-release; return nil })
			first, second := make(chan ai.ThinkingLevel, 1), make(chan ai.ThinkingLevel, 1)
			failures := make(chan error, 2)
			go func() {
				value, err := setLaneTestThinking(ctx, lane, ai.ThinkingHigh, &observed)
				first <- value
				failures <- err
			}()
			<-started
			go func() {
				value, err := setLaneTestThinking(ctx, lane, ai.ThinkingMedium, &observed)
				second <- value
				failures <- err
			}()
			synctest.Wait()
			require.Equal(t, []ai.ThinkingLevel{ai.ThinkingOff}, observed)
			close(release)
			require.Equal(t, []ai.ThinkingLevel{ai.ThinkingHigh, ai.ThinkingMedium}, []ai.ThinkingLevel{<-first, <-second})
			require.NoError(t, <-failures)
			require.NoError(t, <-failures)
			require.Equal(t, []ai.ThinkingLevel{ai.ThinkingOff, ai.ThinkingHigh}, observed)
			require.Equal(t, ai.ThinkingMedium, lane.SnapshotState().Configuration.ThinkingLevel)
		})
	})
}

type uncloneableRunStart struct {
	agentharness.RunStartPayload
	Invalid func()
}
