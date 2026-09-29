package runtime

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

func TestPortWave02HarnessLaneManagement(t *testing.T) {
	isolateHarnessTest(t)
	// upstream: packages/agent/test/harness/runtime/harness.test.ts:52-61.
	t.Run("attaches to a fresh Session without creating an implicit main lane", func(t *testing.T) {
		opened := harnessSession(t, "session-0", nil)
		owner := attachedHarness(t, opened)
		lanes, err := owner.Lanes(context.Background())
		requireHarnessOK(t, err)
		requireHarnessEqual(t, lanes, []agentharness.LaneInfo{})
		branch, err := opened.Branch(context.Background(), "main")
		requireHarnessOK(t, err)
		if branch != nil {
			t.Fatal("implicit main Branch")
		}
		for _, name := range []string{"Accept", "GetTipID", "AppendMessage"} {
			if _, exists := reflect.TypeOf(owner).MethodByName(name); exists {
				t.Fatalf("Harness exposes lane method %s", name)
			}
		}
	})

	// upstream: packages/agent/test/harness/runtime/harness.test.ts:63-86.
	t.Run("atomically gets or creates a complete AgentLane", func(t *testing.T) {
		opened := harnessSession(t, "session-0", nil)
		owner := attachedHarness(t, opened)
		events := []string{}
		listenHarness(t, owner, agentharness.EventLaneCreated, func(_ harness.Context, event agentharness.HarnessEvent) error {
			events = append(events, event.Lane)
			return nil
		})
		lane := acquireHarnessLane(t, owner, "main")
		same, err := owner.Lane(context.Background(), "main", &agentharness.AcquireLaneOptions{SetCreateAt: true, CreateAt: new("ignored")})
		requireHarnessOK(t, err)
		if same != lane {
			t.Fatal("lane identity changed")
		}
		tip, err := lane.GetTipID(context.Background())
		requireHarnessOK(t, err)
		requireHarnessEqual(t, tip, (*string)(nil))
		thinking, err := lane.GetThinkingLevel(context.Background())
		requireHarnessOK(t, err)
		requireHarnessEqual(t, thinking, ai.ThinkingLevel("medium"))
		tools, err := lane.GetActiveTools(context.Background())
		requireHarnessOK(t, err)
		requireHarnessEqual(t, tools, []string{"read", "bash"})
		stored := readHarnessValue(t, opened, session.LaneStateValue("main"))
		if stored == nil {
			t.Fatal("missing durable lane state")
		}
		requireHarnessEqual(t, stored.Value, session.LaneState{Inbox: []session.InboxItem{}})
		requireHarnessEqual(t, events, []string{"main"})
		lanes, err := owner.Lanes(context.Background())
		requireHarnessOK(t, err)
		requireHarnessEqual(t, len(lanes), 1)
		requireHarnessEqual(t, lanes[0].Name, "main")
		requireHarnessEqual(t, lanes[0].TipID, (*string)(nil))
	})

	// upstream: packages/agent/test/harness/runtime/harness.test.ts:88-103.
	t.Run("returns one published AgentLane under concurrent acquisition", func(t *testing.T) {
		owner := attachedHarness(t, nil)
		calls := 0
		listenHarness(t, owner, agentharness.EventLaneCreated, func(harness.Context, agentharness.HarnessEvent) error { calls++; return nil })
		type acquired struct {
			lane *Lane
			err  error
		}
		results := make(chan acquired, 3)
		for range 3 {
			go func() { lane, err := owner.Lane(context.Background(), "main", nil); results <- acquired{lane, err} }()
		}
		all := []acquired{<-results, <-results, <-results}
		for _, result := range all {
			requireHarnessOK(t, result.err)
			if result.lane != all[0].lane {
				t.Fatal("concurrent lane identities differ")
			}
		}
		requireHarnessEqual(t, calls, 1)
	})

	// upstream: packages/agent/test/harness/runtime/harness.test.ts:105-140.
	t.Run("uses one stable provider session id per lane", func(t *testing.T) {
		opened := harnessSession(t, "shared-session", nil)
		options := fauxHarnessOptions(opened, "main one", "main two", "review")
		options.ThinkingLevel = ""
		options.ActiveToolNames = []string{}
		models := options.Models.(*ai.Models)
		base := models.GetProvider("faux")
		provider := *base
		sessionIDs := []string{}
		provider.StreamSimple = func(ctx context.Context, model *ai.Model, request ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			sessionIDs = append(sessionIDs, options.SessionID)
			return base.StreamSimple(ctx, model, request, options)
		}
		models.SetProvider(&provider)
		created, err := CreateAgentHarness(context.Background(), options)
		requireHarnessOK(t, err)
		owner := created.Harness
		t.Cleanup(func() { requireHarnessOK(t, owner.Close(context.Background())) })
		main, review := acquireHarnessLane(t, owner, "main"), acquireHarnessLane(t, owner, "review")
		_, err = main.Prompt(context.Background(), "one", nil)
		requireHarnessOK(t, err)
		_, err = main.Prompt(context.Background(), "two", nil)
		requireHarnessOK(t, err)
		_, err = review.Prompt(context.Background(), "review", nil)
		requireHarnessOK(t, err)
		requireHarnessEqual(t, sessionIDs, []string{"shared-session:main", "shared-session:main", "shared-session:review"})
	})

	// upstream: packages/agent/test/harness/runtime/harness.test.ts:142-167.
	t.Run("serializes commands from different AgentLanes on the one Session line", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			owner := attachedHarness(t, nil)
			main, review := acquireHarnessLane(t, owner, "main"), acquireHarnessLane(t, owner, "review")
			started, gate := make(chan struct{}), make(chan struct{})
			var release sync.Once
			unlock := func() { release.Do(func() { close(gate) }) }
			t.Cleanup(unlock)
			order := []string{}
			first, err := main.CommandAsync(context.Background(), func(LaneState, session.SessionReader) (LaneCommand[any], error) {
				order = append(order, "main:start")
				close(started)
				<-gate
				order = append(order, "main:end")
				return LaneCommand[any]{Kind: CommandReturn}, nil
			})
			requireHarnessOK(t, err)
			<-started
			second, err := review.CommandAsync(context.Background(), func(LaneState, session.SessionReader) (LaneCommand[any], error) {
				order = append(order, "review")
				return LaneCommand[any]{Kind: CommandReturn}, nil
			})
			requireHarnessOK(t, err)
			synctest.Wait()
			requireHarnessEqual(t, order, []string{"main:start"})
			unlock()
			_, err = first.Wait()
			requireHarnessOK(t, err)
			_, err = second.Wait()
			requireHarnessOK(t, err)
			requireHarnessEqual(t, order, []string{"main:start", "main:end", "review"})
		})
	})

	// upstream: packages/agent/test/harness/runtime/harness.test.ts:169-199.
	t.Run("uses createAt only for a missing lane and validates the target", func(t *testing.T) {
		opened := harnessSession(t, "session-0", nil)
		harnessCommit(t, opened, session.InsertEntry(session.Entry{ID: "target", Type: session.EntryTypeCustom, CustomType: "target"}))
		owner := attachedHarness(t, opened)
		lane, err := owner.Lane(context.Background(), "review", &agentharness.AcquireLaneOptions{SetCreateAt: true, CreateAt: new("target")})
		requireHarnessOK(t, err)
		tip, err := lane.GetTipID(context.Background())
		requireHarnessOK(t, err)
		requireHarnessEqual(t, tip, new("target"))
		same, err := owner.Lane(context.Background(), "review", &agentharness.AcquireLaneOptions{SetCreateAt: true, CreateAt: new("missing")})
		requireHarnessOK(t, err)
		if same != lane {
			t.Fatal("existing lane identity changed")
		}
		_, err = owner.Lane(context.Background(), "missing", &agentharness.AcquireLaneOptions{SetCreateAt: true, CreateAt: new("unknown")})
		requireAdmissionError(t, err, "UnknownTarget", "")
		_, err = owner.Lane(context.Background(), "", nil)
		requireAdmissionError(t, err, "InvalidLane", "")
		_, err = owner.Lane(context.Background(), "bad\x00name", nil)
		requireAdmissionError(t, err, "InvalidLane", "")
	})

	// upstream: packages/agent/test/harness/runtime/harness.test.ts:201-232.
	t.Run("attaches agent state to a data-only Branch without moving its tip", func(t *testing.T) {
		opened := harnessSession(t, "session-0", nil)
		harnessCommit(t, opened, session.InsertEntry(session.Entry{ID: "target", Type: session.EntryTypeCustom, CustomType: "target"}), session.SetValue(session.BranchTip("main"), new("target")))
		owner := attachedHarness(t, opened)
		lanes, err := owner.Lanes(context.Background())
		requireHarnessOK(t, err)
		requireHarnessEqual(t, lanes, []agentharness.LaneInfo{})
		lane := acquireHarnessLane(t, owner, "main")
		tip, err := lane.GetTipID(context.Background())
		requireHarnessOK(t, err)
		requireHarnessEqual(t, tip, new("target"))
		stored := readHarnessValue(t, opened, session.LaneConfig("main"))
		if stored == nil {
			t.Fatal("missing configuration")
		}
		requireHarnessEqual(t, stored.Value, session.LaneConfiguration{Model: session.ModelRef{Provider: "faux", ModelID: "faux-1"}, ThinkingLevel: "medium", ActiveToolNames: []string{"read", "bash"}})
	})

	// upstream: packages/agent/test/harness/runtime/harness.test.ts:234-256.
	t.Run("keeps AgentLane appends operation-aware while exposing the Branch surface directly", func(t *testing.T) {
		opened := harnessSession(t, "session-0", nil)
		lane := acquireHarnessLane(t, attachedHarness(t, opened), "main")
		idleID, err := lane.AppendCustomEntry(context.Background(), "idle", nil)
		requireHarnessOK(t, err)
		tip, err := lane.GetTipID(context.Background())
		requireHarnessOK(t, err)
		requireHarnessEqual(t, tip, &idleID)
		acceptRequest(t, lane, promptRequest("run"))
		acceptedTip, err := lane.GetTipID(context.Background())
		requireHarnessOK(t, err)
		var data session.JsonValue = map[string]any{"queued": true}
		pendingID, err := lane.AppendCustomEntry(context.Background(), "pending", &data)
		requireHarnessOK(t, err)
		tip, err = lane.GetTipID(context.Background())
		requireHarnessOK(t, err)
		requireHarnessEqual(t, tip, acceptedTip)
		storedTip := readHarnessValue(t, opened, session.BranchTip("main"))
		if storedTip == nil {
			t.Fatal("missing tip")
		}
		requireHarnessEqual(t, storedTip.Value, acceptedTip)
		pending := readHarnessValue(t, opened, session.PendingEntryValue(pendingID))
		if pending == nil {
			t.Fatal("missing pending entry")
		}
		requireHarnessEqual(t, pending.Value, session.PendingEntry{Type: "custom", CustomType: "pending", CustomPayload: &data})
		storedState := readHarnessValue(t, opened, session.LaneStateValue("main"))
		if storedState == nil {
			t.Fatal("missing lane state")
		}
		requireHarnessEqual(t, storedState.Value.Inbox, []session.InboxItem{{EntryID: pendingID, Kind: "write"}})
	})

	// upstream: packages/agent/test/harness/runtime/harness.test.ts:258-293.
	t.Run("flushes queued writes before a new idle append in one commit", func(t *testing.T) {
		opened := harnessSession(t, "session-0", nil)
		lane := acquireHarnessLane(t, attachedHarness(t, opened), "main")
		first, second := opened.IdGenerator().Next(nil), opened.IdGenerator().Next(nil)
		_, err := lane.Command(context.Background(), func(state LaneState, _ session.SessionReader) (LaneCommand[any], error) {
			inbox := []session.InboxItem{{EntryID: first, Kind: "write"}, {EntryID: second, Kind: "write"}}
			next := state
			next.Inbox = inbox
			return LaneCommand[any]{Kind: CommandCommit, Writes: []session.Write{session.SetValue(session.PendingEntryValue(first), session.PendingEntry{Type: "custom", CustomType: "queued-first"}), session.SetValue(session.PendingEntryValue(second), session.PendingEntry{Type: "custom", CustomType: "queued-second"}), session.SetValue(session.LaneStateValue("main"), session.LaneState{Inbox: inbox})}, Next: next, Materialize: func(session.CommitResult) any { return nil }}, nil
		})
		requireHarnessOK(t, err)
		appended, err := lane.AppendCustomEntry(context.Background(), "new", nil)
		requireHarnessOK(t, err)
		entries, err := lane.FindEntries(context.Background(), &session.BranchScan{Order: session.OrderOldestFirst})
		requireHarnessOK(t, err)
		requireHarnessEqual(t, harnessEntryIDs(entries), []string{first, second, appended})
		requireHarnessEqual(t, lane.SnapshotState().Inbox, []session.InboxItem{})
		stored := readHarnessValue(t, opened, session.LaneStateValue("main"))
		if stored == nil {
			t.Fatal("missing lane state")
		}
		requireHarnessEqual(t, stored.Value.Inbox, []session.InboxItem{})
		for _, id := range []string{first, second} {
			if readHarnessValue(t, opened, session.PendingEntryValue(id)) != nil {
				t.Fatalf("pending entry %q retained", id)
			}
		}
	})

	configured := session.LaneConfiguration{Model: session.ModelRef{Provider: "faux", ModelID: "faux-1"}, ThinkingLevel: "low", ActiveToolNames: []string{"read"}}
	// upstream: packages/agent/test/harness/runtime/harness.test.ts:295-319.
	t.Run("restores complete lanes without requiring main", func(t *testing.T) {
		opened := harnessSession(t, "session-0", nil)
		harnessCommit(t, opened, session.SetValue(session.BranchTip("review"), (*string)(nil)), session.SetValue(session.LaneConfig("review"), configured), session.SetValue(session.LaneStateValue("review"), session.LaneState{Inbox: []session.InboxItem{}}))
		created, err := CreateAgentHarness(context.Background(), fauxHarnessOptions(opened))
		requireHarnessOK(t, err)
		t.Cleanup(func() { requireHarnessOK(t, created.Harness.Close(context.Background())) })
		requireHarnessEqual(t, created.Open, []agentharness.OpenOperation{})
		lanes, err := created.Harness.Lanes(context.Background())
		requireHarnessOK(t, err)
		names := make([]string, len(lanes))
		for i, lane := range lanes {
			names[i] = lane.Name
		}
		requireHarnessEqual(t, names, []string{"review"})
		if acquireHarnessLane(t, created.Harness, "review") == nil {
			t.Fatal("missing review lane")
		}
		branch, err := opened.Branch(context.Background(), "main")
		requireHarnessOK(t, err)
		if branch != nil {
			t.Fatal("implicit main Branch")
		}
	})

	// upstream: packages/agent/test/harness/runtime/harness.test.ts:321-335.
	t.Run("rejects partial durable lane state as a Harness fault", func(t *testing.T) {
		opened := harnessSession(t, "session-0", nil)
		harnessCommit(t, opened, session.SetValue(session.BranchTip("main"), (*string)(nil)), session.SetValue(session.LaneConfig("main"), configured))
		_, err := CreateAgentHarness(context.Background(), fauxHarnessOptions(opened))
		if _, ok := errors.AsType[*harness.HarnessFault](err); !ok {
			t.Fatalf("got %v; want HarnessFault", err)
		}
	})
}

func TestPortWave02HarnessGlobalMetadata(t *testing.T) {
	isolateHarnessTest(t)
	// upstream: packages/agent/test/harness/runtime/harness.test.ts:339-353.
	t.Run("preserves value_update publication and delivery", func(t *testing.T) {
		owner := attachedHarness(t, nil)
		seen := []string{}
		listenHarness(t, owner, agentharness.EventValueUpdate, func(_ harness.Context, event agentharness.HarnessEvent) error {
			payload := event.Payload.(agentharness.ValueUpdatePayload)
			if payload.Value == "session_name" {
				name, err := owner.GetName(context.Background())
				if err != nil {
					return err
				}
				if name == nil {
					return errors.New("published name is absent")
				}
				seen = append(seen, "name:"+*name)
			} else {
				seen = append(seen, payload.Value)
			}
			return nil
		})
		requireHarnessOK(t, owner.SetName(context.Background(), new("named")))
		requireHarnessOK(t, owner.SetLabel(context.Background(), "entry", new("label")))
		name, err := owner.GetName(context.Background())
		requireHarnessOK(t, err)
		requireHarnessEqual(t, name, new("named"))
		label, err := owner.GetLabel(context.Background(), "entry")
		requireHarnessOK(t, err)
		requireHarnessEqual(t, label, new("label"))
		requireHarnessEqual(t, seen, []string{"name:named", "entry_label"})
	})

	// upstream: packages/agent/test/harness/runtime/harness.test.ts:355-379.
	t.Run("publishes previous and current data-bearing global configuration", func(t *testing.T) {
		owner := attachedHarness(t, nil)
		events := []agentharness.HarnessEvent{}
		listenHarness(t, owner, agentharness.EventConfigUpdate, func(_ harness.Context, event agentharness.HarnessEvent) error {
			events = append(events, event)
			return nil
		})
		options := harness.AgentHarnessStreamOptions{TimeoutMs: new(123)}
		requireHarnessOK(t, owner.SetStreamOptions(context.Background(), options))
		requireHarnessOK(t, owner.SetSteeringMode(context.Background(), "one-at-a-time"))
		requireHarnessEqual(t, len(events), 2)
		requireHarnessEqual(t, events[0].Type(), agentharness.EventConfigUpdate)
		requireHarnessEqual(t, events[0].Payload, agentharness.HarnessEventPayload(agentharness.ConfigUpdatePayload{Property: "streamOptions", Previous: harness.AgentHarnessStreamOptions{}, Value: options}))
		requireHarnessEqual(t, events[1].Type(), agentharness.EventConfigUpdate)
		requireHarnessEqual(t, events[1].Payload, agentharness.HarnessEventPayload(agentharness.ConfigUpdatePayload{Property: "steeringMode", Previous: agent.QueueMode("all"), Value: agent.QueueMode("one-at-a-time")}))
	})

	// upstream: packages/agent/test/harness/runtime/harness.test.ts:381-388.
	t.Run("closes every lane and rejects later acquisition", func(t *testing.T) {
		owner := attachedHarness(t, nil)
		lane := acquireHarnessLane(t, owner, "main")
		requireHarnessOK(t, owner.Close(context.Background()))
		_, err := owner.Lane(context.Background(), "other", nil)
		if err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("acquisition error = %v; want closed", err)
		}
		_, err = lane.GetTipID(context.Background())
		if err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("tip read error = %v; want closed", err)
		}
	})

	// upstream: packages/agent/test/harness/runtime/harness.test.ts:390-396.
	t.Run("MemorySessionRepo creation also remains branchless", func(t *testing.T) {
		repo := session.NewMemorySessionRepo(nil)
		t.Cleanup(func() { requireHarnessOK(t, repo.Close(context.Background())) })
		opened, err := repo.Create(context.Background(), session.SessionCreateOptions{ID: "repo-session"})
		requireHarnessOK(t, err)
		t.Cleanup(func() { requireHarnessOK(t, opened.Close(context.Background())) })
		branch, err := opened.Branch(context.Background(), "main")
		requireHarnessOK(t, err)
		if branch != nil {
			t.Fatal("implicit main Branch")
		}
		requireHarnessOK(t, opened.Close(context.Background()))
		requireHarnessOK(t, repo.Close(context.Background()))
	})
}
