package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/compaction"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

func TestPortWave02AtomicRunAcceptance(t *testing.T) {
	isolateHarnessTest(t)
	// upstream: packages/agent/test/harness/runtime/accept.test.ts:90-131.
	image := ai.ImageContent{Data: "aW1hZ2U=", MimeType: "image/png"}
	for _, row := range []struct {
		name, text string
		images     []ai.ImageContent
		content    []ai.UserContentBlock
	}{
		{"text", "hello", nil, []ai.UserContentBlock{ai.TextContent{Text: "hello"}}},
		{"images", "", []ai.ImageContent{image}, []ai.UserContentBlock{image}},
		{"text and images", "hello", []ai.ImageContent{image}, []ai.UserContentBlock{ai.TextContent{Text: "hello"}, image}},
	} {
		t.Run(fmt.Sprintf("accepts normalized %s prompts into starting", row.name), func(t *testing.T) {
			fixture := newAdmissionFixture(t, nil, nil)
			request := promptRequest(row.text)
			request.Images = row.images
			admission := acceptRequest(t, fixture.lane, request)
			operation := requireRunOperation(t, fixture.lane)
			if len(operation.Meta.Intent.PromptEntryIDs) == 0 {
				t.Fatal("expected prompt entry")
			}
			entry := readHarnessEntry(t, fixture.session, operation.Meta.Intent.PromptEntryIDs[0])
			requireHarnessEqual(t, admission.OperationID, operation.Meta.OperationID)
			requireHarnessEqual(t, admission.Kind, agentharness.OperationRun)
			requireHarnessEqual(t, entry.Type, session.EntryTypeMessage)
			if entry.Message.User == nil {
				t.Fatal("expected user message")
			}
			requireHarnessEqual(t, entry.Message.User.Role, "user")
			requireHarnessEqual(t, entry.Message.User.Content.(ai.UserContentBlocks), ai.UserContentBlocks(row.content))
			requireHarnessEqual(t, operation.State.Settings, session.RunSettings{Compaction: compaction.DefaultCompactionSettings, SteeringMode: "all", FollowUpMode: "all", ToolExecution: "parallel"})
		})
	}

	// upstream: packages/agent/test/harness/runtime/accept.test.ts:133-181.
	t.Run("preserves supplied message arrays and commits the exact acceptance write families once", func(t *testing.T) {
		fixture := newAdmissionFixture(t, nil, nil)
		messages := []agent.AgentMessage{laneUser("one", 10), laneUser("two", 11)}
		key := harness.CreateContextKey[string]("accept.context")
		ctx := harness.WithContextValue(context.Background(), key, "source")
		seen := []agentharness.HarnessEventType{}
		sameContext := true
		for _, kind := range []agentharness.HarnessEventType{agentharness.EventRunStart, agentharness.EventMessageStart, agentharness.EventMessageEnd, agentharness.EventEntryAdded} {
			listenHarness(t, fixture.owner, kind, func(eventContext harness.Context, event agentharness.HarnessEvent) error {
				seen = append(seen, event.Type())
				sameContext = sameContext && eventContext == ctx
				return nil
			})
		}
		admission, err := fixture.lane.Accept(ctx, agentharness.OperationRequest{Kind: agentharness.RequestPrompt, OperationID: new("operation"), PromptMessages: messages})
		requireHarnessOK(t, err)
		requireHarnessEqual(t, admission, agentharness.OperationAdmission{OperationID: "operation", Kind: agentharness.OperationRun, StartedAt: admission.StartedAt})
		attempts := fixture.storage.GetCommitAttempts()
		requireHarnessEqual(t, len(attempts), 1)
		requireHarnessEqual(t, acceptanceWriteKinds(attempts[0]), []string{"entry", "entry", "value:set", "value:set", "value:set", "value:set"})
		operation := requireRunOperation(t, fixture.lane)
		requireHarnessEqual(t, operation.Meta.OperationID, "operation")
		requireHarnessEqual(t, operation.Meta.Lane, "main")
		requireHarnessEqual(t, operation.Meta.SourceTipID, (*string)(nil))
		requireHarnessEqual(t, len(operation.Meta.Intent.PromptEntryIDs), 2)
		tip, err := fixture.lane.GetTipID(context.Background())
		requireHarnessOK(t, err)
		requireHarnessEqual(t, tip, new(operation.Meta.Intent.PromptEntryIDs[1]))
		requireHarnessEqual(t, seen, []agentharness.HarnessEventType{agentharness.EventRunStart, agentharness.EventMessageStart, agentharness.EventMessageEnd, agentharness.EventEntryAdded, agentharness.EventMessageStart, agentharness.EventMessageEnd, agentharness.EventEntryAdded})
		requireHarnessEqual(t, sameContext, true)
		requireHarnessEqual(t, fixture.models.lookups.Load(), int64(0))
	})

	// upstream: packages/agent/test/harness/runtime/accept.test.ts:183-240.
	t.Run("captures next-run messages before request messages and accepts an otherwise empty request", func(t *testing.T) {
		const first, second = "pending-first", "pending-second"
		fixture := newAdmissionFixture(t, func(opened session.Session) {
			harnessCommit(t, opened,
				session.SetValue(session.PendingEntryValue(first), session.PendingEntry{Type: "message", Message: laneUser("first", 1)}),
				session.SetValue(session.PendingEntryValue(second), session.PendingEntry{Type: "message", Message: laneUser("second", 2)}),
				session.SetValue(session.LaneStateValue("main"), session.LaneState{Inbox: []session.InboxItem{{EntryID: first, Kind: "nextRun"}, {EntryID: second, Kind: "nextRun"}}}))
		}, nil)
		events := []agentharness.HarnessEventType{}
		listenHarness(t, fixture.owner, agentharness.EventQueueUpdate, func(_ harness.Context, event agentharness.HarnessEvent) error {
			events = append(events, event.Type())
			return nil
		})
		acceptRequest(t, fixture.lane, promptRequest(""))
		operation := requireRunOperation(t, fixture.lane)
		requireHarnessEqual(t, operation.Meta.Intent.PromptEntryIDs, []string{})
		requireHarnessEqual(t, operation.Meta.SourceTipID, (*string)(nil))
		requireHarnessEqual(t, fixture.lane.SnapshotState().Inbox, []session.InboxItem{})
		requireHarnessEqual(t, harnessEntryIDs(scanHarnessBranch(t, fixture.session, second)), []string{first, second})
		for _, id := range []string{first, second} {
			if readHarnessValue(t, fixture.session, session.PendingEntryValue(id)) != nil {
				t.Fatalf("pending entry %q retained", id)
			}
		}
		attempts := fixture.storage.GetCommitAttempts()
		if len(attempts) == 0 {
			t.Fatal("acceptance did not commit")
		}
		requireHarnessEqual(t, acceptanceWriteKinds(attempts[0]), []string{"entry", "entry", "value:delete", "value:delete", "value:set", "value:set", "value:set", "value:set"})
		requireHarnessEqual(t, events, []agentharness.HarnessEventType{agentharness.EventQueueUpdate})
	})

	// upstream: packages/agent/test/harness/runtime/accept.test.ts:242-308.
	t.Run("captures every eligible tag by mode and preserves admission order before the request", func(t *testing.T) {
		const write, next, steer1, steer2, follow1, follow2 = "queued-write", "queued-next", "queued-steer-1", "queued-steer-2", "queued-follow-1", "queued-follow-2"
		fixture := newAdmissionFixture(t, func(opened session.Session) {
			writes := []session.Write{session.SetValue(session.PendingEntryValue(write), session.PendingEntry{Type: "custom", CustomType: "queued-write"})}
			for _, id := range []string{next, steer1, steer2, follow1, follow2} {
				writes = append(writes, session.SetValue(session.PendingEntryValue(id), session.PendingEntry{Type: "message", Message: laneUser(id, 1)}))
			}
			writes = append(writes, session.SetValue(session.LaneStateValue("main"), session.LaneState{Inbox: []session.InboxItem{{EntryID: steer1, Kind: "steer"}, {EntryID: write, Kind: "write"}, {EntryID: next, Kind: "nextRun"}, {EntryID: follow1, Kind: "followUp"}, {EntryID: steer2, Kind: "steer"}, {EntryID: follow2, Kind: "followUp"}}}))
			harnessCommit(t, opened, writes...)
		}, func(options *AgentHarnessOptions) {
			options.SteeringMode, options.FollowUpMode = "one-at-a-time", "one-at-a-time"
		})
		acceptRequest(t, fixture.lane, promptRequest("request"))
		state := fixture.lane.SnapshotState()
		if state.TipID == nil {
			t.Fatal("expected accepted tip")
		}
		requireHarnessEqual(t, harnessEntryIDs(scanHarnessBranch(t, fixture.session, *state.TipID)), []string{steer1, write, next, follow1, *state.TipID})
		requireHarnessEqual(t, state.Inbox, []session.InboxItem{{EntryID: steer2, Kind: "steer"}, {EntryID: follow2, Kind: "followUp"}})
		stored := readHarnessValue(t, fixture.session, session.LaneStateValue("main"))
		if stored == nil {
			t.Fatal("missing lane state")
		}
		requireHarnessEqual(t, stored.Value.Inbox, state.Inbox)
		for _, id := range []string{write, next, steer1, follow1} {
			if readHarnessValue(t, fixture.session, session.PendingEntryValue(id)) != nil {
				t.Fatalf("pending entry %q retained", id)
			}
		}
		for _, id := range []string{steer2, follow2} {
			if readHarnessValue(t, fixture.session, session.PendingEntryValue(id)) == nil {
				t.Fatalf("pending entry %q removed", id)
			}
		}
	})

	// upstream: packages/agent/test/harness/runtime/accept.test.ts:310-338.
	t.Run("does not let a lone queued write validate empty acceptance", func(t *testing.T) {
		inbox := []session.InboxItem{{EntryID: "queued-write", Kind: "write"}}
		fixture := newAdmissionFixture(t, func(opened session.Session) {
			harnessCommit(t, opened, session.SetValue(session.PendingEntryValue("queued-write"), session.PendingEntry{Type: "custom", CustomType: "queued-write"}), session.SetValue(session.LaneStateValue("main"), session.LaneState{Inbox: inbox}))
		}, nil)
		_, err := fixture.lane.Accept(context.Background(), promptRequest(""))
		requireAdmissionError(t, err, "InvalidMessage", "empty")
		requireHarnessEqual(t, fixture.lane.SnapshotState().Inbox, inbox)
		if readHarnessValue(t, fixture.session, session.PendingEntryValue("queued-write")) == nil {
			t.Fatal("pending write removed")
		}
	})

	// upstream: packages/agent/test/harness/runtime/accept.test.ts:340-369.
	t.Run("formats skills and templates before acceptance", func(t *testing.T) {
		resources := agentharness.Resources{Skills: []harness.Skill{{Name: "review", Description: "Review", Content: "Inspect it", FilePath: "/skills/review/SKILL.md"}}, PromptTemplates: []harness.PromptTemplate{{Name: "fix", Content: "Fix $1 then $@"}}}
		configure := func(options *AgentHarnessOptions) { options.Resources = resources }
		skill := newAdmissionFixture(t, nil, configure)
		acceptRequest(t, skill.lane, agentharness.OperationRequest{Kind: agentharness.RequestSkill, Name: "review", AdditionalInstructions: new("Be strict")})
		entry := readHarnessEntry(t, skill.session, *skill.lane.SnapshotState().TipID)
		requireHarnessEqual(t, entry.Type, session.EntryTypeMessage)
		if entry.Message.User == nil || len(entry.Message.User.Content.(ai.UserContentBlocks)) != 1 {
			t.Fatalf("skill message = %#v", entry.Message)
		}
		text, ok := entry.Message.User.Content.(ai.UserContentBlocks)[0].(ai.TextContent)
		if !ok || !strings.Contains(text.Text, `<skill name="review"`) {
			t.Fatalf("skill text = %#v", entry.Message.User.Content)
		}
		template := newAdmissionFixture(t, nil, configure, "accept-1")
		acceptRequest(t, template.lane, agentharness.OperationRequest{Kind: agentharness.RequestPromptTemplate, Name: "fix", Args: []string{"A", "B"}})
		entry = readHarnessEntry(t, template.session, *template.lane.SnapshotState().TipID)
		requireHarnessEqual(t, entry.Type, session.EntryTypeMessage)
		if entry.Message.User == nil {
			t.Fatal("template is not a user message")
		}
		requireHarnessEqual(t, entry.Message.User.Content.(ai.UserContentBlocks), ai.UserContentBlocks{ai.TextContent{Text: "Fix A then A B"}})
	})

	// upstream: packages/agent/test/harness/runtime/accept.test.ts:371-402.
	t.Run("accepts standalone compaction with durable preparation and no execution", func(t *testing.T) {
		fixture := newAdmissionFixture(t, nil, nil)
		_, err := fixture.lane.AppendMessage(context.Background(), laneUser("history", 1))
		requireHarnessOK(t, err)
		fixture.storage.ClearCommitAttempts()
		starts := 0
		listenHarness(t, fixture.owner, agentharness.EventCompactionStart, func(harness.Context, agentharness.HarnessEvent) error { starts++; return nil })
		admission := acceptRequest(t, fixture.lane, agentharness.OperationRequest{Kind: agentharness.RequestCompaction, OperationID: new("compaction"), CustomInstructions: new("focus")})
		requireHarnessEqual(t, admission, agentharness.OperationAdmission{OperationID: "compaction", Kind: agentharness.OperationCompaction, StartedAt: admission.StartedAt})
		operation := fixture.lane.SnapshotState().Operation
		if operation == nil || operation.State.At != session.AtSummaryDeciding {
			t.Fatal("expected accepted compaction")
		}
		task := operation.State.Task
		requireHarnessEqual(t, task.Reason, "manual")
		requireHarnessEqual(t, task.CustomInstructions, new("focus"))
		requireHarnessEqual(t, task.Boundary.Kind, "finish")
		preparation := readHarnessValue(t, fixture.session, session.OperationPreparation("compaction", task.TaskID))
		if preparation == nil {
			t.Fatal("missing durable preparation")
		}
		requireHarnessEqual(t, preparation.Value.Kind, "compaction")
		requireHarnessEqual(t, len(fixture.storage.GetCommitAttempts()), 1)
		requireHarnessEqual(t, starts, 1)
		requireHarnessEqual(t, fixture.lane.CurrentDrive(), (*Drive)(nil))
	})

	// upstream: packages/agent/test/harness/runtime/accept.test.ts:404-412.
	t.Run("rejects empty standalone compaction without writing", func(t *testing.T) {
		fixture := newAdmissionFixture(t, nil, nil)
		_, err := fixture.lane.Accept(context.Background(), agentharness.OperationRequest{Kind: agentharness.RequestCompaction})
		requireAdmissionError(t, err, "NothingToCompact", "")
		requireHarnessEqual(t, len(fixture.storage.GetCommitAttempts()), 0)
	})

	// upstream: packages/agent/test/harness/runtime/accept.test.ts:414-486.
	for _, summarize := range []bool{false, true} {
		t.Run(fmt.Sprintf("accepts %t summarized navigation atomically", summarize), func(t *testing.T) {
			fixture := newAdmissionFixture(t, func(opened session.Session) {
				harnessCommit(t, opened,
					session.InsertEntry(session.Entry{ID: "root", Type: session.EntryTypeMessage, Message: laneUser("root", 1)}),
					session.InsertEntry(session.Entry{ID: "source", ParentID: new("root"), Type: session.EntryTypeMessage, Message: laneUser("source", 2)}),
					session.InsertEntry(session.Entry{ID: "target", ParentID: new("root"), Type: session.EntryTypeMessage, Message: laneUser("target", 3)}), session.SetValue(session.BranchTip("main"), new("source")))
			}, nil)
			fixture.storage.ClearCommitAttempts()
			starts := 0
			listenHarness(t, fixture.owner, agentharness.EventNavigationStart, func(harness.Context, agentharness.HarnessEvent) error { starts++; return nil })
			id, at := "direct", session.AtNavigationReadyToCommit
			if summarize {
				id, at = "summarized", session.AtSummaryDeciding
			}
			acceptRequest(t, fixture.lane, agentharness.OperationRequest{Kind: agentharness.RequestNavigation, OperationID: &id, TargetID: new("target"), NavigateOptions: &agentharness.NavigateOptions{Summarize: summarize, Label: new("chosen"), CustomInstructions: new("focus")}})
			state := fixture.lane.SnapshotState()
			if state.Operation == nil {
				t.Fatal("expected accepted navigation")
			}
			requireHarnessEqual(t, state.Operation.State.At, at)
			requireHarnessEqual(t, state.TipID, new("source"))
			if summarize {
				preparation := readHarnessValue(t, fixture.session, session.OperationPreparation(id, state.Operation.State.Task.TaskID))
				if preparation == nil {
					t.Fatal("missing branch preparation")
				}
				requireHarnessEqual(t, preparation.Value.Kind, "branch_summary")
				requireHarnessEqual(t, len(preparation.Value.Messages), 1)
				if preparation.Value.Messages[0].User == nil {
					t.Fatal("expected source user message")
				}
				requireHarnessEqual(t, preparation.Value.Messages[0].User.Content, laneUser("source", 2).User.Content)
			}
			requireHarnessEqual(t, len(fixture.storage.GetCommitAttempts()), 1)
			requireHarnessEqual(t, starts, 1)
			requireHarnessEqual(t, fixture.lane.CurrentDrive(), (*Drive)(nil))
		})
	}

	// upstream: packages/agent/test/harness/runtime/accept.test.ts:488-526.
	t.Run("validates navigation before acceptance", func(t *testing.T) {
		fixture := newAdmissionFixture(t, func(opened session.Session) {
			harnessCommit(t, opened, session.InsertEntry(session.Entry{ID: "source", Type: session.EntryTypeMessage, Message: laneUser("source", 1)}), session.SetValue(session.BranchTip("main"), new("source")))
		}, nil)
		_, err := fixture.lane.Accept(context.Background(), agentharness.OperationRequest{Kind: agentharness.RequestNavigation, TargetID: new("source")})
		requireAdmissionError(t, err, "InvalidNavigation", "current_tip")
		_, err = fixture.lane.Accept(context.Background(), agentharness.OperationRequest{Kind: agentharness.RequestNavigation, NavigateOptions: &agentharness.NavigateOptions{Label: new("bad")}})
		requireAdmissionError(t, err, "InvalidNavigation", "root_label")
		_, err = fixture.lane.Accept(context.Background(), agentharness.OperationRequest{Kind: agentharness.RequestNavigation, TargetID: new("missing")})
		requireAdmissionError(t, err, "UnknownTarget", "")
		requireHarnessEqual(t, len(fixture.storage.GetCommitAttempts()), 0)
	})

	// upstream: packages/agent/test/harness/runtime/accept.test.ts:528-549.
	t.Run("returns expected pre-acceptance errors without writing", func(t *testing.T) {
		fixture := newAdmissionFixture(t, nil, nil)
		_, err := fixture.lane.Accept(context.Background(), promptRequest(""))
		requireAdmissionError(t, err, "InvalidMessage", "empty")
		pending := agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{}, StopReason: ai.StopReasonPending}}
		_, err = fixture.lane.Accept(context.Background(), agentharness.OperationRequest{Kind: agentharness.RequestPrompt, PromptMessages: []agent.AgentMessage{pending}})
		requireAdmissionError(t, err, "InvalidMessage", "pending_assistant")
		_, err = fixture.lane.Accept(context.Background(), agentharness.OperationRequest{Kind: agentharness.RequestSkill, Name: "missing"})
		requireAdmissionError(t, err, "UnknownSkill", "")
		_, err = fixture.lane.Accept(context.Background(), agentharness.OperationRequest{Kind: agentharness.RequestPromptTemplate, Name: "missing"})
		requireAdmissionError(t, err, "UnknownTemplate", "")
		requireHarnessEqual(t, len(fixture.storage.GetCommitAttempts()), 0)
	})

	// upstream: packages/agent/test/harness/runtime/accept.test.ts:551-562.
	t.Run("serializes concurrent accepts so exactly one wins", func(t *testing.T) {
		fixture := newAdmissionFixture(t, nil, nil)
		errors := make(chan error, 2)
		for _, text := range []string{"first", "second"} {
			go func() { _, err := fixture.lane.Accept(context.Background(), promptRequest(text)); errors <- err }()
		}
		results := []error{<-errors, <-errors}
		wins := 0
		for _, err := range results {
			if err == nil {
				wins++
			} else {
				requireAdmissionError(t, err, "LaneBusy", "")
			}
		}
		requireHarnessEqual(t, wins, 1)
		requireHarnessEqual(t, len(fixture.storage.GetCommitAttempts()), 1)
	})

	// upstream: packages/agent/test/harness/runtime/accept.test.ts:564-594.
	t.Run("faults on commit failure without publishing acceptance", func(t *testing.T) {
		storage := &controlledLaneStorage{MemoryStorage: session.NewMemoryStorage(nil)}
		opened := harnessSession(t, "failing", storage)
		seedAcceptance(t, opened)
		owner := attachedHarness(t, opened)
		lane := acquireHarnessLane(t, owner, "main")
		storage.beforeCommit(func() error { return errors.New("accept failed") })
		_, err := lane.Accept(context.Background(), promptRequest("hello"))
		if _, ok := errors.AsType[*harness.HarnessFault](err); !ok {
			t.Fatalf("got %v; want HarnessFault", err)
		}
		requireHarnessEqual(t, lane.SnapshotState().Operation, (*session.Operation)(nil))
	})

	// upstream: packages/agent/test/harness/runtime/accept.test.ts:596-606.
	t.Run("delivers acceptance listeners after publishing state and permits serialized reads", func(t *testing.T) {
		fixture := newAdmissionFixture(t, nil, nil)
		inspected := false
		listenHarness(t, fixture.owner, agentharness.EventRunStart, func(harness.Context, agentharness.HarnessEvent) error {
			execution, err := fixture.lane.InspectExecution(context.Background())
			inspected = err == nil && execution.Current != nil && execution.Current.Status == "open"
			return err
		})
		acceptRequest(t, fixture.lane, promptRequest("hello"))
		requireHarnessEqual(t, inspected, true)
	})

	// upstream: packages/agent/test/harness/runtime/accept.test.ts:608-628.
	t.Run("does not resolve acceptance before its direct listeners settle", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newAdmissionFixture(t, nil, nil)
			started, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			releaseNow := func() { once.Do(func() { close(release) }) }
			t.Cleanup(releaseNow)
			listenHarness(t, fixture.owner, agentharness.EventRunStart, func(harness.Context, agentharness.HarnessEvent) error { close(started); <-release; return nil })
			done := make(chan error, 1)
			go func() { _, err := fixture.lane.Accept(context.Background(), promptRequest("hello")); done <- err }()
			<-started
			synctest.Wait()
			select {
			case err := <-done:
				t.Fatalf("acceptance resolved before listener release: %v", err)
			default:
			}
			releaseNow()
			requireHarnessOK(t, <-done)
		})
	})

	// upstream: packages/agent/test/harness/runtime/accept.test.ts:630-639.
	t.Run("returns Closed when acceptance starts after close", func(t *testing.T) {
		fixture := newAdmissionFixture(t, nil, nil)
		requireHarnessOK(t, fixture.owner.Close(context.Background()))
		_, err := fixture.lane.Accept(context.Background(), promptRequest("late"))
		requireAdmissionError(t, err, "Closed", "")
		requireHarnessEqual(t, len(fixture.storage.GetCommitAttempts()), 0)
	})

	// upstream: packages/agent/test/harness/runtime/accept.test.ts:641-675.
	t.Run("publishes an acceptance admitted before close", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			storage := &controlledLaneStorage{MemoryStorage: session.NewMemoryStorage(nil)}
			opened := harnessSession(t, "closing", storage)
			seedAcceptance(t, opened)
			owner := attachedHarness(t, opened)
			lane := acquireHarnessLane(t, owner, "main")
			started, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			releaseNow := func() { once.Do(func() { close(release) }) }
			t.Cleanup(releaseNow)
			storage.beforeCommit(func() error { close(started); <-release; return nil })
			accepted, closed := make(chan error, 1), make(chan error, 1)
			go func() { _, err := lane.Accept(context.Background(), promptRequest("hello")); accepted <- err }()
			<-started
			go func() { closed <- owner.Close(context.Background()) }()
			synctest.Wait()
			releaseNow()
			requireHarnessOK(t, <-accepted)
			requireHarnessOK(t, <-closed)
		})
	})
}
