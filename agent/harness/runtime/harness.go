package runtime

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/compaction"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

// Ports packages/agent/src/harness/runtime/harness.ts.

// AgentHarnessOptions selects the session, provider collection and initial configuration. Nil ActiveToolNames derives the initial selection from Tools; an empty slice selects no tools.
// Ports packages/agent/src/harness/agent-harness.ts (AgentHarnessOptions).
type AgentHarnessOptions struct {
	Session            session.Session
	Models             Models
	Model              *ai.Model
	ThinkingLevel      ai.ThinkingLevel
	ActiveToolNames    []string
	Tools              []harness.AgentHarnessTool
	ToolContext        ToolContextFactory
	SystemPrompt       SystemPromptFactory
	Resources          agentharness.Resources
	StreamOptions      harness.AgentHarnessStreamOptions
	Retry              *ai.RetryPolicy
	Compaction         *harness.CompactionSettings
	SteeringMode       agent.QueueMode
	FollowUpMode       agent.QueueMode
	ToolExecution      agent.ToolExecutionMode
	ToProviderMessages ProviderMessageConverter
	EntryProjectors    map[string]session.EntryProjector
}

// CreatedHarness contains the attached runtime and the operations left open by its previous owner.
type CreatedHarness struct {
	Harness *Harness
	Open    []agentharness.OpenOperation
}

// Harness manages configured lanes attached to one Session. It is not itself a lane.
type Harness struct {
	Session session.Session
	Models  Models

	mu          sync.Mutex
	seed        session.LaneConfiguration
	config      Config
	lanesByName map[string]*Lane
	laneOrder   []string
	hooks       *agentharness.HookRegistry
	events      *agentharness.HarnessEventBus
	closedError error
	faultError  *harness.HarnessFault
	closeOnce   sync.Once
	closeError  error
	deliveries  sync.WaitGroup
}

// CreateAgentHarness restores durable control state without starting provider, tool, hook or timer effects.
func CreateAgentHarness(ctx context.Context, options AgentHarnessOptions) (CreatedHarness, error) {
	retry := harness.DefaultRetryPolicy
	if options.Retry != nil {
		retry = *options.Retry
	}
	settings := compaction.DefaultCompactionSettings
	if options.Compaction != nil {
		settings = *options.Compaction
	}
	if err := harness.ValidateToolNames(options.Tools); err != nil {
		return CreatedHarness{}, err
	}
	if err := harness.ValidateRetryPolicy(retry); err != nil {
		return CreatedHarness{}, err
	}
	if err := harness.ValidateCompactionSettings(settings); err != nil {
		return CreatedHarness{}, err
	}
	active := slices.Clone(options.ActiveToolNames)
	if active == nil {
		active = make([]string, 0, len(options.Tools))
		for _, tool := range options.Tools {
			active = append(active, tool.Name)
		}
	}
	thinking := options.ThinkingLevel
	if thinking == "" {
		thinking = "off"
	}
	seed := session.LaneConfiguration{Model: session.ModelRef{Provider: options.Model.ProviderMeta.ProviderID, ModelID: options.Model.ID}, ThinkingLevel: thinking, ActiveToolNames: active}
	restored, err := RestoreSession(ctx, options.Session)
	if err != nil {
		return CreatedHarness{}, &harness.HarnessFault{Message: "AgentHarness storage or invariant fault", Cause: err}
	}
	owner := &Harness{Session: options.Session, Models: options.Models, seed: seed, lanesByName: map[string]*Lane{}, events: agentharness.NewHarnessEventBus()}
	owner.hooks = agentharness.NewHookRegistry(func(ctx harness.Context, err error, hook agentharness.HookName, lane string) error {
		owner.events.Emit(ctx, agentharness.HarnessEvent{Lane: lane, Payload: agentharness.HandlerErrorPayload{Kind: "hook", Hook: string(hook), Error: err.Error()}})
		return nil
	})
	owner.config = Config{Tools: options.Tools, Resources: options.Resources, StreamOptions: options.StreamOptions, RetryPolicy: retry, Compaction: settings, SteeringMode: options.SteeringMode, FollowUpMode: options.FollowUpMode, ToolExecution: options.ToolExecution, ToolContext: options.ToolContext, SystemPrompt: options.SystemPrompt, ToProviderMessages: options.ToProviderMessages, EntryProjectors: options.EntryProjectors}
	if owner.config.SteeringMode == "" {
		owner.config.SteeringMode = "all"
	}
	if owner.config.FollowUpMode == "" {
		owner.config.FollowUpMode = "all"
	}
	if owner.config.ToolExecution == "" {
		owner.config.ToolExecution = "parallel"
	}
	if owner.config.ToProviderMessages == nil {
		owner.config.ToProviderMessages = defaultProviderMessages
	}
	open := make([]agentharness.OpenOperation, 0)
	for _, restoredLane := range restored {
		owner.lanesByName[restoredLane.Name] = owner.buildLane(restoredLane.Name, restoredLane.State)
		owner.laneOrder = append(owner.laneOrder, restoredLane.Name)
		if operation := restoredLane.State.Operation; operation != nil {
			open = append(open, agentharness.OpenOperation{Lane: restoredLane.Name, OperationID: operation.Meta.OperationID, Kind: agentharness.OperationKind(operation.Meta.Intent.Kind), StartedAt: operation.Meta.StartedAt, Aborting: operation.State.Control.Status == session.ControlCancelRequested})
		}
	}
	return CreatedHarness{Harness: owner, Open: open}, nil
}

// Events returns the runtime's passive event bus.
func (owner *Harness) Events() *agentharness.HarnessEventBus { return owner.events }

// Hooks returns the runtime's hook registry.
func (owner *Harness) Hooks() *agentharness.HookRegistry { return owner.hooks }

// ReadConfig captures the current process-local configuration.
func (owner *Harness) ReadConfig() Config {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	return owner.config
}

func (owner *Harness) assertOpen() error {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.faultError != nil {
		return owner.faultError
	}
	return owner.closedError
}

func (owner *Harness) buildLane(name string, state LaneState) *Lane {
	return NewLane(name, owner.Session, owner.Models, owner.hooks, state, owner.Fault,
		func(ctx harness.Context, events []agentharness.HarnessEvent) (func() error, error) {
			deliver := owner.events.PrepareBatch(ctx, events)
			return func() error { deliver(); return nil }, nil
		},
		func(_ harness.Context, snapshot agentharness.LaneSnapshot, filter func(agentharness.HarnessEvent) bool, capture agentharness.ResnapshotCapture[agentharness.LaneSnapshot]) (agentharness.WatchHandle[agentharness.LaneSnapshot], error) {
			return agentharness.Watch(owner.events, snapshot, filter, capture)
		}, owner.ReadConfig)
}

// Lane atomically gets or creates a complete lane. CreateAt applies only to an absent Branch.
func (owner *Harness) Lane(ctx context.Context, name string, options *agentharness.AcquireLaneOptions) (*Lane, error) {
	if err := owner.assertOpen(); err != nil {
		return nil, err
	}
	if name == "" || strings.ContainsRune(name, 0) {
		reason := "lane name must not be empty"
		if name != "" {
			reason = "lane name must not contain \\u0000"
		}
		return nil, &harness.InvalidLane{Lane: name, Reason: reason, Message: "Invalid lane " + restoreQuote(name) + ": " + reason}
	}
	var lane *Lane
	var delivery func()
	_, err := owner.Session.Mutate(ctx, func(ctx context.Context, reader session.SessionMutator) (any, error) {
		if err := owner.assertOpen(); err != nil {
			return nil, err
		}
		owner.mu.Lock()
		lane = owner.lanesByName[name]
		owner.mu.Unlock()
		if lane != nil {
			return nil, nil
		}
		stored, err := ReadLaneStorage(ctx, reader, name)
		if err != nil {
			return nil, err
		}
		if stored.Kind == "lane" {
			state, err := RestoreLaneState(ctx, reader, name, stored)
			if err != nil {
				return nil, err
			}
			lane = owner.buildLane(name, state)
			owner.publishLane(name, lane)
			return nil, nil
		}
		var tip *string
		if stored.Kind == "branch" {
			tip = stored.Tip.Value
		} else if options != nil {
			tip = options.CreateAt
		}
		if stored.Kind == "absent" && tip != nil {
			entries, err := reader.GetEntries(ctx, []string{*tip})
			if err != nil {
				return nil, err
			}
			if _, found := entries[*tip]; !found {
				return nil, &harness.UnknownTarget{TargetID: *tip, Message: "Unknown target: " + *tip}
			}
		}
		configuration := owner.seed
		configuration.ActiveToolNames = slices.Clone(owner.seed.ActiveToolNames)
		state := LaneState{TipID: tip, Configuration: configuration, Inbox: []session.InboxItem{}}
		writes := make([]session.Write, 0, 3)
		if stored.Kind == "absent" {
			writes = append(writes, session.SetValue(session.BranchTip(name), tip))
		}
		writes = append(writes, session.SetValue(session.LaneConfig(name), configuration), session.SetValue(session.LaneStateValue(name), session.LaneState{Inbox: []session.InboxItem{}}))
		if _, err := reader.Commit(ctx, writes); err != nil {
			return nil, err
		}
		lane = owner.buildLane(name, state)
		owner.publishLane(name, lane)
		delivery = owner.events.PrepareBatch(ctx, []agentharness.HarnessEvent{{Lane: name, Payload: agentharness.LaneCreatedPayload{At: tip}}})
		return nil, nil
	})
	if err != nil {
		if closed := owner.assertOpen(); closed != nil {
			return nil, closed
		}
		switch err.(type) { //nolint:errorlint // Upstream instanceof checks the outer error, not a wrapped cause.
		case *harness.InvalidLane, *harness.UnknownTarget:
			return nil, err
		}
		return nil, owner.Fault(ctx, err)
	}
	if delivery != nil {
		delivery()
	}
	if lane == nil {
		return nil, owner.Fault(ctx, &session.SessionInvariantError{Message: "Lane " + restoreQuote(name) + " was not published"})
	}
	return lane, nil
}

func (owner *Harness) publishLane(name string, lane *Lane) {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	owner.lanesByName[name] = lane
	owner.laneOrder = append(owner.laneOrder, name)
}

func (owner *Harness) lanesSnapshot() []*Lane {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	lanes := make([]*Lane, 0, len(owner.laneOrder))
	for _, name := range owner.laneOrder {
		lanes = append(lanes, owner.lanesByName[name])
	}
	return lanes
}

// Lanes returns configured lanes in publication order.
func (owner *Harness) Lanes(ctx context.Context) ([]agentharness.LaneInfo, error) {
	if err := owner.assertOpen(); err != nil {
		return nil, err
	}
	lanes := owner.lanesSnapshot()
	infos := make([]agentharness.LaneInfo, len(lanes))
	errs := make([]error, len(lanes))
	var group sync.WaitGroup
	for i, lane := range lanes {
		group.Go(func() {
			execution, err := lane.InspectExecution(ctx)
			errs[i] = err
			infos[i] = agentharness.LaneInfo{Name: execution.Lane, TipID: execution.TipID, Operation: execution.Current}
		})
	}
	group.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return infos, nil
}

// GetName reads the Session's global name.
func (owner *Harness) GetName(ctx context.Context) (*string, error) {
	if err := owner.assertOpen(); err != nil {
		return nil, err
	}
	return owner.Session.GetName(ctx)
}

// GetLabel reads one global entry label.
func (owner *Harness) GetLabel(ctx context.Context, targetID string) (*string, error) {
	if err := owner.assertOpen(); err != nil {
		return nil, err
	}
	return owner.Session.GetLabel(ctx, targetID)
}

func (owner *Harness) setValue(ctx context.Context, write session.Write, event agentharness.HarnessEvent) error {
	if err := owner.assertOpen(); err != nil {
		return err
	}
	var delivery func()
	_, err := owner.Session.Mutate(ctx, func(ctx context.Context, mutator session.SessionMutator) (any, error) {
		if err := owner.assertOpen(); err != nil {
			return nil, err
		}
		if _, err := mutator.Commit(ctx, []session.Write{write}); err != nil {
			return nil, err
		}
		delivery = owner.events.PrepareBatch(ctx, []agentharness.HarnessEvent{event})
		return nil, nil
	})
	if err != nil {
		if closed := owner.assertOpen(); closed != nil {
			return closed
		}
		return owner.Fault(ctx, err)
	}
	delivery()
	return nil
}

// SetName commits the global name before delivering its value_update event. Nil clears the name.
func (owner *Harness) SetName(ctx context.Context, name *string) error {
	var write session.Write = session.DeleteValue(session.SessionName)
	if name != nil {
		write = session.SetValue(session.SessionName, *name)
	}
	return owner.setValue(ctx, write, agentharness.HarnessEvent{Payload: agentharness.ValueUpdatePayload{Value: "session_name", Name: name}})
}

// SetLabel commits an entry label before delivering its value_update event. Nil clears the label.
func (owner *Harness) SetLabel(ctx context.Context, targetID string, label *string) error {
	var write session.Write = session.DeleteValue(session.EntryLabel(targetID))
	if label != nil {
		write = session.SetValue(session.EntryLabel(targetID), *label)
	}
	return owner.setValue(ctx, write, agentharness.HarnessEvent{Payload: agentharness.ValueUpdatePayload{Value: "entry_label", TargetID: targetID, Label: label}})
}

// Fault seals every lane and publishes the first storage or invariant fault.
func (owner *Harness) Fault(ctx harness.Context, cause error) error {
	owner.mu.Lock()
	if owner.faultError != nil {
		err := owner.faultError
		owner.mu.Unlock()
		return err
	}
	if owner.closedError != nil {
		err := owner.closedError
		owner.mu.Unlock()
		return err
	}
	fault := &harness.HarnessFault{Message: "AgentHarness storage or invariant fault", Cause: cause}
	owner.faultError = fault
	deliver := owner.events.PrepareBatch(ctx, []agentharness.HarnessEvent{{Payload: agentharness.FaultPayload{Code: "harness_fault", Message: fault.Message}}})
	owner.deliveries.Add(1)
	owner.mu.Unlock()
	for _, lane := range owner.lanesSnapshot() {
		lane.Seal(fault)
	}
	owner.hooks.Close(fault)
	owner.events.Close(fault)
	go func() { defer owner.deliveries.Done(); deliver() }()
	return fault
}

// Close seals all lanes and waits for admitted Session mutations, idle callbacks and owned fault publication.
func (owner *Harness) Close(ctx context.Context) error {
	owner.closeOnce.Do(func() {
		err := &harness.HarnessClosed{}
		owner.mu.Lock()
		owner.closedError = err
		owner.mu.Unlock()
		var idle []<-chan struct{}
		for _, lane := range owner.lanesSnapshot() {
			idle = append(idle, lane.Seal(err))
		}
		owner.hooks.Close(err)
		owner.events.Close(err)
		owner.closeError = owner.Session.Close(ctx)
		for _, done := range idle {
			<-done
		}
		owner.deliveries.Wait()
	})
	return owner.closeError
}

// WatchSession reports the pinned runtime's unimplemented session watcher.
func (owner *Harness) WatchSession(context.Context) (agentharness.WatchHandle[agentharness.SessionSnapshot], error) {
	return nil, &SliceNotImplemented{Operation: "watchSession"}
}
