package durableagent

// Ports packages/coding-agent/src/experimental/durable/runtime.ts and packages/coding-agent/src/experimental/vacation/runtime.ts
//
// The vacation planner's runtime is a copy of the coding agent's in which only the registry, the environments and the root agent differ; Profile carries those differences, so one implementation serves both.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	durablechord "github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// ModelSummary is a model the user can select.
type ModelSummary struct {
	durable.ModelRef
	Name          string
	ContextWindow int
}

// Notice is a message shown below the transcript.
type Notice struct {
	ID      int
	Level   NoticeLevel
	Message string
}

// NoticeLevel is "info", "warning", or "error".
type NoticeLevel string

const (
	NoticeInfo    NoticeLevel = "info"
	NoticeWarning NoticeLevel = "warning"
	NoticeError   NoticeLevel = "error"
)

// ConversationSummary is a conversation the user can switch to: the main one, or a subagent's.
type ConversationSummary struct {
	ID    durable.ConversationId
	Label string
	// Title is the first user message, for a subagent its task. The main conversation needs no title; nil when absent.
	Title *string
}

// SessionSummary identifies the open session.
type SessionSummary struct {
	ID        string
	Directory string
	CWD       string
}

// DurableView is everything the TUI renders. Plain values; no Harness objects cross this boundary. A view is replaced, never changed in place.
type DurableView struct {
	Session SessionSummary
	// Conversation is the conversation shown and talked to.
	Conversation  harness.ConversationView
	Conversations []ConversationSummary
	Models        []ModelSummary
	Notices       []Notice
	// Tasks is the live task graph while the task panel is open; nil otherwise.
	Tasks *harness.TaskGraph
}

// DurableViewSource publishes the view. Listeners run off the Session line, once per burst of changes.
type DurableViewSource interface {
	Current() DurableView
	// Subscribe registers listener and returns the function that removes it.
	Subscribe(listener func()) func()
}

// Completion is closed when a command has run. A command reports its failure as a notice, so it has no error.
type Completion <-chan struct{}

// DurableController is what the TUI may ask for. Commands run one at a time in the order they were asked, so toggles, switches and key presses apply in order; each returns without waiting.
type DurableController interface {
	// Submit prompts when idle; otherwise it steers or queues a follow-up.
	Submit(text string, whenBusy durable.WhenBusy) Completion
	Compact(instructions *string) Completion
	// Abort is not queued: it waits until the conversation is idle.
	Abort() Completion
	CycleThinking() Completion
	SetModel(model durable.ModelRef) Completion
	ToggleTasks() Completion
	// SwitchConversation shows and talks to another conversation.
	SwitchConversation(id durable.ConversationId) Completion
}

// OpenDurableOptions select the session to open.
type OpenDurableOptions struct {
	// CWD is the working directory; empty is the process's.
	CWD             string
	ContinueSession bool
}

// OpenDurableResult is an open session.
type OpenDurableResult struct {
	View       DurableViewSource
	Controller DurableController
	// Settings are pi's settings, for the TUI's theme and terminal capabilities.
	Settings *codingagent.SettingsManager
	// Close closes the Harness, which writes no outcome, and releases the session lock. Repeated calls return the first result.
	Close func() error
}

// Profile is what separates an agent from the coding agent: the sessions it keeps, its registry, whether its conversations have execution environments, and the agent of its root conversation.
type Profile struct {
	// Kind is the session kind: "durable" or "vacation".
	Kind string
	// Registry builds the registry the Harness opens with.
	Registry func(settings *codingagent.SettingsManager, cwd string) (harness.Registry, error)
	// Environments gives every conversation a Node execution environment of its directory.
	Environments bool
	// RootAgent is the agent the root conversation starts with, before the initial model.
	RootAgent func(cwd string) harness.AgentChange
}

// CodingProfile is the coding agent: pi's coding registry with the subagent tool, execution environments, and a root conversation in the session's directory.
var CodingProfile = Profile{
	Kind: "durable",
	Registry: func(settings *codingagent.SettingsManager, cwd string) (harness.Registry, error) {
		registry, err := CreateCodingRegistry(settings, cwd)
		if err != nil {
			return nil, err
		}
		return registry, registry.Install(Subagent)
	},
	Environments: true,
	RootAgent:    func(cwd string) harness.AgentChange { return harness.AgentChange{Cwd: harness.SetTo(cwd)} },
}

// AgentOf is the agent document of a view; absent while the conversation has none.
func AgentOf(view harness.ConversationView) harness.AgentState {
	var agent harness.AgentState
	if document, _ := view.Docs.Get("pi.agent"); document != nil {
		if decoded, err := durable.DecodeDoc[harness.AgentState](document); err == nil && decoded != nil {
			agent = *decoded
		}
	}
	return agent
}

// userText is the text of a user message: its string, or its text blocks joined by blank.
func userText(content ai.UserContent) string {
	switch content := content.(type) {
	case ai.UserText:
		return string(content)
	case ai.UserContentBlocks:
		var texts []string
		for _, block := range content {
			if block, ok := block.(ai.TextContent); ok {
				texts = append(texts, block.Text)
			}
		}
		return strings.Join(texts, " ")
	}
	return ""
}

// titleOf is the text of a user entry as a one-line title; nil for any other entry.
func titleOf(entry *durable.EntryRecord) *string {
	if entry == nil || len(entry.Model) == 0 {
		return nil
	}
	message, ok := entry.Model[0].(ai.UserMessage)
	if !ok {
		return nil
	}
	return new(strings.Join(strings.FieldsFunc(userText(message.Content), widthx.IsJSSpace), " "))
}

// firstInput is a subagent's task: the oldest user message of its conversation. The main conversation needs no title.
func firstInput(ctx context.Context, opened harness.Harness, id durable.ConversationId) (*string, error) {
	if id == durable.ROOT_CONVERSATION_ID {
		return nil, nil
	}
	conversation, err := opened.Conversation(ctx, id)
	if err != nil {
		return nil, err
	}
	if conversation == nil {
		return nil, fmt.Errorf("Conversation %d does not exist", id)
	}
	var first *durable.EntryRecord
	var cursor durable.Cursor
	for {
		page, err := conversation.Entries(ctx, durable.EntryQuery{}, 256, cursor)
		if err != nil {
			return nil, err
		}
		for index := len(page.Items) - 1; index >= 0; index-- {
			if page.Items[index].Kind == "pi.user" {
				first = &page.Items[index]
				break
			}
		}
		if page.Next == nil {
			return titleOf(first), nil
		}
		cursor = *page.Next
	}
}

// durableRuntime is the state behind the view and the controller of one open session.
type durableRuntime struct {
	ctx          context.Context
	opened       harness.Harness
	modelRuntime *coding.ModelRuntime
	label        func(durable.ConversationId) string

	mu        sync.Mutex
	state     DurableView
	listeners map[int]func()
	nextKey   int
	notifying bool
	nextNote  int

	// current, conversation and unsubscribe change only in commands, which run one at a time; mu guards them for Abort.
	current      harness.Conversation
	conversation durable.AttachedReplicatedState[harness.ConversationView]
	unsubscribe  func()

	tasks            durable.AttachedReplicatedState[harness.TaskGraph]
	unsubscribeTasks func()

	tail       chan struct{}
	background sync.WaitGroup
}

func (runtime *durableRuntime) Current() DurableView {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.state
}

func (runtime *durableRuntime) Subscribe(listener func()) func() {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	key := runtime.nextKey
	runtime.nextKey++
	runtime.listeners[key] = listener
	return func() {
		runtime.mu.Lock()
		defer runtime.mu.Unlock()
		delete(runtime.listeners, key)
	}
}

// update replaces the state. Commit listeners and Chord frames call it on the Session line; the listeners run afterwards on their own goroutine, once per burst.
func (runtime *durableRuntime) update(patch func(*DurableView)) {
	runtime.mu.Lock()
	patch(&runtime.state)
	if runtime.notifying {
		runtime.mu.Unlock()
		return
	}
	runtime.notifying = true
	runtime.mu.Unlock()
	runtime.background.Go(func() {
		runtime.mu.Lock()
		runtime.notifying = false
		listeners := make([]func(), 0, len(runtime.listeners))
		for _, key := range slices.Sorted(mapKeys(runtime.listeners)) {
			listeners = append(listeners, runtime.listeners[key])
		}
		runtime.mu.Unlock()
		for _, listener := range listeners {
			listener()
		}
	})
}

func mapKeys[V any](values map[int]V) func(yield func(int) bool) {
	return func(yield func(int) bool) {
		for key := range values {
			if !yield(key) {
				return
			}
		}
	}
}

func (runtime *durableRuntime) notice(level NoticeLevel, message string) {
	runtime.update(func(state *DurableView) {
		runtime.nextNote++
		notices := append(slices.Clone(state.Notices), Notice{ID: runtime.nextNote, Level: level, Message: message})
		state.Notices = notices[max(0, len(notices)-20):]
	})
}

func (runtime *durableRuntime) fail(err error) { runtime.notice(NoticeError, err.Error()) }

// command runs operation after every earlier command, in the order commands were asked. Its failure is a notice.
func (runtime *durableRuntime) command(operation func() error) Completion {
	done := make(chan struct{})
	runtime.mu.Lock()
	previous := runtime.tail
	runtime.tail = done
	runtime.mu.Unlock()
	runtime.background.Go(func() {
		defer close(done)
		if previous != nil {
			<-previous
		}
		if err := operation(); err != nil {
			runtime.fail(err)
		}
	})
	return done
}

func (runtime *durableRuntime) shown() harness.Conversation {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.current
}

// watchAnswer reports a prompt that ended without an answer. A turn that ends without one shows a notice; one recovered after a restart does not, since only submissions made by this process are watched.
func (runtime *durableRuntime) watchAnswer(submission durable.Submission) {
	runtime.background.Go(func() {
		settled, err := submission.Wait(runtime.ctx)
		if err != nil {
			if runtime.ctx.Err() == nil {
				runtime.fail(err)
			}
			return
		}
		if settled.Status == durable.SubmissionUnanswered && (settled.Reason == nil || *settled.Reason != "aborted") {
			reason := ""
			if settled.Reason != nil {
				reason = *settled.Reason
			}
			detail := ""
			if settled.Detail != nil {
				detail = " " + ai.SafeJsonStringify(settled.Detail)
			}
			runtime.notice(NoticeError, "No answer: "+reason+detail)
		}
	})
}

func (runtime *durableRuntime) Submit(text string, whenBusy durable.WhenBusy) Completion {
	return runtime.command(func() error {
		submission, err := runtime.shown().Submit(runtime.ctx, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(text), WhenBusy: whenBusy})
		if err != nil {
			return err
		}
		runtime.watchAnswer(submission)
		return nil
	})
}

func (runtime *durableRuntime) Compact(instructions *string) Completion {
	return runtime.command(func() error {
		id, err := runtime.shown().Compact(runtime.ctx, instructions)
		if err != nil {
			return err
		}
		// Report the outcome once it is known; the status line shows the compaction meanwhile.
		runtime.background.Go(func() {
			receipt, err := runtime.opened.WaitForTask(runtime.ctx, id)
			if err == nil {
				err = runtime.reportCompaction(receipt)
			}
			if err != nil && runtime.ctx.Err() == nil {
				runtime.fail(err)
			}
		})
		return nil
	})
}

func (runtime *durableRuntime) reportCompaction(receipt durable.SettledTask[durable.JsonValue]) error {
	outcome := receipt.State.Outcome
	if outcome == nil {
		return fmt.Errorf("Compaction task %d settled without an outcome", receipt.Id)
	}
	switch outcome.Status {
	case durable.OutcomeCompleted:
		var result harness.CompactionResult
		if outcome.Result != nil {
			decoded, err := durable.FromJsonValue[harness.CompactionResult](*outcome.Result)
			if err != nil {
				return err
			}
			result = decoded
		}
		// A summary written while busy is a submission: placed now, queued, or dropped as stale.
		var status durable.SubmissionStatus
		if result.SubmissionId != nil {
			submission, err := runtime.opened.Submission(runtime.ctx, *result.SubmissionId)
			if err != nil {
				return err
			}
			if submission != nil {
				record, err := submission.Status(runtime.ctx)
				if err != nil {
					return err
				}
				status = record.Status
			}
		}
		switch {
		case result.EntryId != nil || status == durable.SubmissionDone:
			runtime.notice(NoticeInfo, "Compacted.")
		case status == durable.SubmissionQueued:
			runtime.notice(NoticeInfo, "Compaction summary queued; it is placed at the next turn boundary.")
		case status == durable.SubmissionUnanswered:
			runtime.notice(NoticeInfo, "Compaction summary dropped: the context changed under it.")
		default:
			runtime.notice(NoticeInfo, "Nothing to compact: the context fits in the recent window that is kept verbatim.")
		}
	case durable.OutcomeAborted:
		runtime.notice(NoticeInfo, "Compaction aborted.")
	default:
		detail := ""
		if outcome.Error != nil {
			detail = outcome.Error.Message
		} else if outcome.Reason != nil {
			detail = *outcome.Reason
		}
		runtime.notice(NoticeError, "Compaction "+string(outcome.Status)+": "+detail)
	}
	return nil
}

func (runtime *durableRuntime) Abort() Completion {
	done := make(chan struct{})
	runtime.background.Go(func() {
		defer close(done)
		if err := runtime.shown().Abort(runtime.ctx, nil); err != nil && runtime.ctx.Err() == nil {
			runtime.fail(err)
		}
	})
	return done
}

// agent is the shown conversation's committed agent document. Upstream reads the view's copy, which a delivery that runs before the awaiting command continues has already updated; Go deliveries run after the committing call returns, so a command that follows a configuration reads the committed document itself.
func (runtime *durableRuntime) agent() (harness.AgentState, error) {
	state, err := durable.Snapshot[harness.AgentState](runtime.ctx, runtime.opened, harness.AgentDoc, runtime.shown().Id())
	if err != nil || state == nil {
		return harness.AgentState{}, err
	}
	return *state, nil
}

// agentModel is the model of the shown conversation's agent, with the agent.
func (runtime *durableRuntime) agentModel() (*ai.Model, harness.AgentState, error) {
	agent, err := runtime.agent()
	if err != nil {
		return nil, agent, err
	}
	if agent.Model == nil {
		return nil, agent, errors.New("No model selected")
	}
	model := runtime.modelRuntime.GetModel(agent.Model.Provider, agent.Model.ModelId)
	if model == nil {
		return nil, agent, errors.New("Current model is unavailable")
	}
	return model, agent, nil
}

func thinkingOf(agent harness.AgentState) ai.ModelThinkingLevel {
	if agent.ThinkingLevel != "" {
		return agent.ThinkingLevel
	}
	return ai.ThinkingOff
}

func (runtime *durableRuntime) CycleThinking() Completion {
	return runtime.command(func() error {
		model, agent, err := runtime.agentModel()
		if err != nil {
			return err
		}
		if !model.ProviderMeta.Reasoning {
			return errors.New("Current model does not support thinking")
		}
		levels := ai.GetSupportedThinkingLevels(model)
		next := ai.ThinkingOff
		if len(levels) > 0 {
			next = levels[(slices.Index(levels, thinkingOf(agent))+1)%len(levels)]
		}
		return runtime.shown().Configure(runtime.ctx, harness.AgentChange{ThinkingLevel: harness.SetTo(next)})
	})
}

func (runtime *durableRuntime) SetModel(ref durable.ModelRef) Completion {
	return runtime.command(func() error {
		model := runtime.modelRuntime.GetModel(ref.Provider, ref.ModelId)
		if model == nil {
			return fmt.Errorf("Unknown model: %s/%s", ref.Provider, ref.ModelId)
		}
		agent, err := runtime.agent()
		if err != nil {
			return err
		}
		return runtime.shown().Configure(runtime.ctx, harness.AgentChange{
			Model:         harness.SetTo(ref),
			ThinkingLevel: harness.SetTo(ai.ClampThinkingLevel(model, thinkingOf(agent))),
		})
	})
}

// closeTasks stops observing the task graph; it reports whether the panel was open.
func (runtime *durableRuntime) closeTasks() bool {
	runtime.mu.Lock()
	unsubscribe, tasks := runtime.unsubscribeTasks, runtime.tasks
	runtime.unsubscribeTasks, runtime.tasks = nil, nil
	runtime.mu.Unlock()
	if unsubscribe != nil {
		unsubscribe()
	}
	if tasks != nil {
		tasks.Dispose()
	}
	return tasks != nil
}

func (runtime *durableRuntime) ToggleTasks() Completion {
	return runtime.command(func() error {
		if runtime.closeTasks() {
			runtime.update(func(state *DurableView) { state.Tasks = nil })
			return nil
		}
		graph, err := runtime.opened.TaskGraph(runtime.ctx)
		if err != nil {
			return err
		}
		runtime.mu.Lock()
		runtime.tasks = graph
		runtime.mu.Unlock()
		unsubscribe, err := graph.Subscribe(func(value harness.TaskGraph, _ context.Context, _ durablechord.ReplicatedStateDelivery) {
			runtime.update(func(state *DurableView) { state.Tasks = &value })
		})
		runtime.mu.Lock()
		runtime.unsubscribeTasks = unsubscribe
		runtime.mu.Unlock()
		return err
	})
}

func (runtime *durableRuntime) subscribeConversation(state durable.AttachedReplicatedState[harness.ConversationView]) (func(), error) {
	return state.Subscribe(func(value harness.ConversationView, _ context.Context, _ durablechord.ReplicatedStateDelivery) {
		runtime.update(func(view *DurableView) { view.Conversation = value })
	})
}

func (runtime *durableRuntime) SwitchConversation(id durable.ConversationId) Completion {
	return runtime.command(func() error {
		next, err := runtime.opened.Conversation(runtime.ctx, id)
		if err != nil {
			return err
		}
		if next == nil {
			return fmt.Errorf("Conversation %d does not exist", id)
		}
		nextState, err := next.ViewState(runtime.ctx)
		if err != nil {
			return err
		}
		runtime.mu.Lock()
		unsubscribe, previous := runtime.unsubscribe, runtime.conversation
		runtime.mu.Unlock()
		unsubscribe()
		previous.Dispose()
		runtime.mu.Lock()
		runtime.current, runtime.conversation = next, nextState
		runtime.mu.Unlock()
		unsubscribe, err = runtime.subscribeConversation(nextState)
		runtime.mu.Lock()
		runtime.unsubscribe = unsubscribe
		runtime.mu.Unlock()
		return err
	})
}

// modelSummaries are the models the Model Runtime has available.
func modelSummaries(modelRuntime *coding.ModelRuntime) []ModelSummary {
	var summaries []ModelSummary
	for _, model := range modelRuntime.GetAvailableSnapshot() {
		summaries = append(summaries, ModelSummary{ModelRef: durable.ModelRef{Provider: model.ProviderMeta.ProviderID, ModelId: model.ID}, Name: model.DisplayName, ContextWindow: model.Capabilities.ContextWindow})
	}
	return summaries
}

// reports holds what the Harness reports before the runtime can show notices.
type reports struct {
	mu      sync.Mutex
	pending []error
	sink    func(error)
}

func (queue *reports) add(err error) {
	queue.mu.Lock()
	sink := queue.sink
	if sink == nil {
		queue.pending = append(queue.pending, err)
	}
	queue.mu.Unlock()
	if sink != nil {
		sink(err)
	}
}

// attach delivers the reports made so far to sink, then every later one.
func (queue *reports) attach(sink func(error)) {
	queue.mu.Lock()
	pending := queue.pending
	queue.pending, queue.sink = nil, sink
	queue.mu.Unlock()
	for _, err := range pending {
		sink(err)
	}
}

// Open opens a session of the profile's kind: the Harness over the session's SQLite storage with the profile's registry and pi's settings, the model runtime, and the view and controller over them. Work that an interrupted turn left behind resumes now.
func Open(ctx context.Context, options OpenDurableOptions, profile Profile) (result *OpenDurableResult, err error) {
	cwdInput := options.CWD
	if cwdInput == "" {
		cwdInput = "."
	}
	location, err := SelectSession(ctx, profile.Kind, cwdInput, options.ContinueSession)
	if err != nil {
		return nil, err
	}
	var envs *ExecutionEnvs
	if profile.Environments {
		envs = NewExecutionEnvs(location.CWD)
	}
	lifetime, cancel := context.WithCancel(ctx)
	runtime := &durableRuntime{ctx: lifetime, listeners: map[int]func(){}}
	defer func() {
		if err == nil {
			return
		}
		cancel()
		if runtime.opened != nil {
			_ = runtime.opened.Close(context.WithoutCancel(ctx))
		}
		_ = location.Release()
	}()

	collaborators, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: location.CWD, AgentDir: codingagent.AgentDir()})
	if err != nil {
		return nil, err
	}
	modelRuntime := collaborators.ModelRuntime()
	runtime.modelRuntime = modelRuntime
	// ModelRuntime.create awaits an offline refresh by default and leaves provider diagnostics in the runtime.
	_ = modelRuntime.Refresh(ctx, ai.ModelsRefreshOptions{AllowNetwork: new(false)})
	settings := collaborators.SettingsManager()
	if err := ConfigureHarnessHTTP(settings); err != nil {
		return nil, err
	}
	registry, err := profile.Registry(settings, location.CWD)
	if err != nil {
		return nil, err
	}

	queue := &reports{}
	harnessOptions := harness.HarnessOptions{Models: modelRuntime, Registry: registry, Settings: CreateHarnessSettings(settings), OnReport: queue.add}
	if envs != nil {
		harnessOptions.Env = envs.Env
	}
	store, err := sqlitenode.OpenNodeSqliteStorage(location.Database, sqlitenode.NodeSqliteStorageOptions{})
	if err != nil {
		return nil, err
	}
	runtime.opened, err = harness.OpenHarness(ctx, store, harnessOptions)
	if err != nil {
		return nil, err
	}
	opened := runtime.opened

	var initial InitialModel
	rootAgent := profile.RootAgent(location.CWD)
	if location.Created {
		initial, err = FindInitialAgentModel(ModelSelection{Runtime: modelRuntime, ConfiguredAuth: collaborators.Registry().ModelRegistry.HasConfiguredAuth}, settings, "", "")
		if err != nil {
			return nil, err
		}
		if initial.Model != nil {
			rootAgent.Model = harness.SetTo(*initial.Model)
		}
		if initial.ThinkingLevel != "" {
			rootAgent.ThinkingLevel = harness.SetTo(ai.ModelThinkingLevel(initial.ThinkingLevel))
		}
	}
	root, err := opened.Root(ctx, &harness.RootOptions{Agent: &rootAgent})
	if err != nil {
		return nil, err
	}
	runtime.label = func(id durable.ConversationId) string {
		if id == root.Id() {
			return "main"
		}
		return "subagent " + strconv.FormatInt(int64(id), 10)
	}
	var summaries []ConversationSummary
	var cursor durable.Cursor
	for {
		page, err := durable.Commit(ctx, opened, func(tx durable.Tx) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
			return tx.ScanConversations(durable.ConversationQuery{}, 256, cursor)
		})
		if err != nil {
			return nil, err
		}
		for _, record := range page.Items {
			title, err := firstInput(ctx, opened, record.Id)
			if err != nil {
				return nil, err
			}
			summaries = append(summaries, ConversationSummary{ID: record.Id, Label: runtime.label(record.Id), Title: title})
		}
		if page.Next == nil {
			break
		}
		cursor = *page.Next
	}
	runtime.current = root
	runtime.conversation, err = root.ViewState(ctx)
	if err != nil {
		return nil, err
	}
	runtime.state = DurableView{
		Session:       SessionSummary{ID: location.ID, Directory: location.Directory, CWD: location.CWD},
		Conversation:  runtime.conversation.Value(),
		Conversations: summaries,
		Models:        modelSummaries(modelRuntime),
	}
	queue.attach(func(err error) { runtime.notice(NoticeWarning, err.Error()) })
	runtime.unsubscribe, err = runtime.subscribeConversation(runtime.conversation)
	if err != nil {
		return nil, err
	}
	// Subagents appear as their conversations are created. A commit listener only records; it calls no Session API.
	unsubscribeCommits := opened.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
		runtime.recordPublication(publication)
	})

	saved := AgentOf(runtime.state.Conversation).Model
	if saved == nil {
		runtime.notice(NoticeWarning, "No model configured; select one with /model.")
	} else if modelRuntime.GetModel(saved.Provider, saved.ModelId) == nil {
		runtime.notice(NoticeWarning, "Saved model is unavailable: "+saved.Provider+"/"+saved.ModelId)
	}
	if initial.FallbackMessage != "" {
		runtime.notice(NoticeInfo, initial.FallbackMessage)
	}
	// The task panel starts open; /tasks hides it.
	<-runtime.ToggleTasks()
	// Recovered work from an interrupted turn continues now.
	opened.Resume()

	var closing sync.Once
	var closeErr error
	return &OpenDurableResult{
		View:       runtime,
		Controller: runtime,
		Settings:   settings,
		Close: func() error {
			closing.Do(func() {
				runtime.mu.Lock()
				unsubscribe, conversation := runtime.unsubscribe, runtime.conversation
				runtime.mu.Unlock()
				unsubscribe()
				unsubscribeCommits()
				conversation.Dispose()
				runtime.closeTasks()
				// Close writes no outcome: a running turn resumes with --continue.
				closeErr = opened.Close(ctx)
				cancel()
				runtime.background.Wait()
				if envs != nil {
					closeErr = errors.Join(closeErr, envs.Cleanup(ctx))
				}
				closeErr = errors.Join(closeErr, location.Release())
			})
			return closeErr
		},
	}, nil
}

// recordPublication adds the conversations a commit created and titles a subagent by its first user entry.
func (runtime *durableRuntime) recordPublication(publication durable.CommitPublication) {
	runtime.mu.Lock()
	conversations := runtime.state.Conversations
	runtime.mu.Unlock()
	changed := false
	for _, change := range publication.Changes {
		switch change := change.(type) {
		case durable.ConversationWrite:
			conversations = append(slices.Clone(conversations), ConversationSummary{ID: change.Value.Id, Label: runtime.label(change.Value.Id)})
			changed = true
		case durable.EntryWrite:
			if change.Value.Kind != "pi.user" {
				continue
			}
			conversations = slices.Clone(conversations)
			for index, summary := range conversations {
				if summary.ID == change.Value.ConversationId && summary.Title == nil {
					conversations[index].Title = titleOf(&change.Value)
					changed = true
				}
			}
		}
	}
	if changed {
		runtime.update(func(state *DurableView) { state.Conversations = conversations })
	}
}
