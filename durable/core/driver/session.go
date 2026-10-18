// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/abi/payload"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// ErrUnsupported is the error of a public operation the native session does not serve yet (PROTOCOL.md, "Not supported").
var ErrUnsupported = errors.New("not supported by the Durable core session yet")

// RootConversationID is the reserved ID of the root conversation.
const RootConversationID durable.ConversationId = 1

// SessionConfig configures one native session on the core (ADR D17).
type SessionConfig struct {
	// Core is the ABI session the owner goroutine drives; SQL is its sql_table.
	Core sqlhost.Stepper
	SQL  []string
	// DB is the store connection. The session owns it and closes it with the session.
	DB *sqlhost.DB
	// Harness carries the host code and policy: Models, Registry, Settings, Env, ConversationCreated, Now, OnReport.
	Harness harness.HarnessOptions
	// ModelsInUse are the catalogue entries the core may name.
	ModelsInUse []*ai.Model
	// Sidecar is the cold-open mode of ADR D6; default "index".
	Sidecar string
	// Mode is "harness" (default) or "session" (the tables-and-documents Session without the Harness).
	Mode string
	// Phases runs the phases and abort handlers of custom tasks; nil means the host has no custom task runtime.
	Phases func(ctx context.Context, effect PhaseEffect) error
	// OnCommitted observes every commit after the store applied it (trace capture).
	OnCommitted func(c abi.Commit, sql []string)
	// Monotonic is the clock a model request's duration is measured on (pi-ai: performance.now); nil uses Harness.Now.
	Monotonic func() float64
	// TimerLead and Settle are sqlhost.Options.TimerLead and Settle.
	TimerLead time.Duration
	Settle    func()
	// TimerFired is sqlhost.Options.TimerFired: a virtual Harness.Now clock moves to a timer's time as it fires.
	TimerFired func(at float64)
	// OnEvent observes events in the order the owner delivers them is not offered; use OnCommitted and the Counters instead.

	// onCommit runs on the owner goroutine before the store applies each commit: only the pigfault test build sets it
	// (fault_on.go), to hold the line between a step's commits as Pi's ControlledStorage holds a commit.
	onCommit func(c abi.Commit, sql []string)
}

// Session is one native Harness over one core session and one SQLite connection: the owner goroutine of sqlhost holds the
// core, and every public call below is a request on a channel of the ABI (event, api request, transaction) or a read of Pi's
// own tables on the owner's connection (CONTRACT section 3.10).
type coreSession struct {
	cfg      SessionConfig
	host     *sqlhost.Host
	client   *Client
	table    *handlerTable
	settings durable.Settings
	envs     *Envs
	tasks    *taskRuntimes
	timers   durable.Timers
	unsub    func()
	watches  *watchHub
	commits  commitListeners

	mu          sync.Mutex
	resumed     bool
	closed      bool
	admitted    int           // calls admitted before Close began that have not returned (session.ts #tail)
	drained     chan struct{} // closed when Close began and the last admitted call returned
	ended       chan struct{} // closed once the Session ended, with end set
	end         durable.SessionEnd
	instruction map[string]*durable.PromptSection
	callbacks   map[string]Callback
	// wakes are the wake notices waiting for OnWake, lastWake the last value it took; wakeRunning is set while deliverWakes runs.
	wakes       []harness.Wake
	lastWake    *harness.Wake
	wakeRunning bool

	closeListeners    map[int]func()
	nextCloseListener int
}

func (s *coreSession) now() float64 {
	if s.cfg.Harness.Now != nil {
		return s.cfg.Harness.Now()
	}
	return float64(time.Now().UnixMilli())
}

// Open opens a session over the core. In the default "harness" mode the result is the core's harness.Harness; in "session"
// mode it is also a durable.Session.
func Open(ctx context.Context, cfg SessionConfig) (harness.Harness, error) {
	s, err := open(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// Open cold-opens the core over cfg.DB and returns the session. The registry is read once now and again on every publication.
func open(ctx context.Context, cfg SessionConfig) (*coreSession, error) {
	if cfg.Core == nil || cfg.DB == nil || (cfg.Harness.Models == nil && cfg.Harness.ModelsFunc == nil) || cfg.Harness.Registry == nil {
		return nil, errors.New("driver.Open needs Core, DB, Harness.Models and Harness.Registry")
	}
	// Pi harness.ts:178-179: `const report = safeReport(options.onReport); const now = safeNow(options.now, report)`.
	cfg.Harness.OnReport = harness.SafeReport(cfg.Harness.OnReport)
	if cfg.Harness.Now != nil {
		cfg.Harness.Now = harness.SafeNow(cfg.Harness.Now, cfg.Harness.OnReport)
	}
	s := &coreSession{ended: make(chan struct{}), cfg: cfg, table: newHandlerTable(), instruction: map[string]*durable.PromptSection{}, callbacks: map[string]Callback{}}
	s.timers = durable.HostTimers(cfg.Harness.SetTimer, func(err error) {
		s.report(err, durable.ReportInfo{Type: durable.ReportHostError, Callback: durable.HostCallbackSetTimer})
	})
	s.watches = newWatchHub(s.reportListener)
	s.settings = s.resolveSettings()
	report := func(err error, info durable.ReportInfo) { s.report(err, info) }
	now := cfg.Harness.Now
	if now == nil {
		now = func() float64 { return 0 }
	}
	handlers := map[abi.EffectKind]sqlhost.Handler{}
	models := NewModels(cfg.Harness.CurrentModels, now, func(err error) {
		report(err, durable.ReportInfo{Type: durable.ReportHostError, Callback: durable.HostCallbackModels})
	})
	models.monotonic = cfg.Monotonic
	maps.Copy(handlers, models.Handlers())
	var client *Client
	getClient := func() *Client { return client }
	s.envs = &Envs{Build: cfg.Harness.Env, Reader: func() durable.DocumentReader { return &invocation{s: s} }}
	hooks := &Hooks{Lookup: s.table.lookupHook, Task: func(kind string) (durable.AnyTask, bool) {
		task := s.cfg.Harness.Registry.Snapshot().Task(kind)
		return task, task.Definition != nil
	}, API: func(task durable.TaskId, conversation durable.ConversationId) harness.HookApi {
		return &invocation{s: s, task: task, conversation: conversation}
	}}
	handlers[abi.EffectHook] = s.hookHandler(hooks)
	handlers[abi.EffectTool] = (&Tools{Lookup: s.lookupTool, Backend: func(c ToolCall) ToolBackend {
		return &invocation{s: s, task: c.TaskID, conversation: c.ConversationID}
	}}).Handler()
	handlers[abi.EffectSection] = (&Sections{Envs: s.envs, Backend: func(task durable.TaskId, conversation durable.ConversationId) SectionBackend {
		return &invocation{s: s, task: task, conversation: conversation}
	}}).Handler()
	handlers[abi.EffectEnv] = s.envs.Handler()
	if cfg.Phases != nil {
		handlers[abi.EffectPhase] = (&Phases{Run: cfg.Phases}).Handler()
	} else {
		s.tasks = newTaskRuntimes(s)
		handlers[abi.EffectPhase] = s.tasks.handler()
	}
	callbackHandler := &callbackLate{get: getClient, named: s.callbacks}
	handlers[abi.EffectCallback] = callbackHandler.handle
	s.host = sqlhost.New(sqlhost.Options{
		Core: cfg.Core, SQL: cfg.SQL, DB: cfg.DB, OwnDB: true, Clock: cfg.Harness.Now, TimerLead: cfg.TimerLead, Settle: cfg.Settle,
		SetTimer: s.armTimer,
		Handlers: handlers, OnNotice: func(n abi.Notice) { s.watches.notice(n); s.commitNotice(n); s.reportNotice(n); s.wakeNotice(n) }, OnCommit: cfg.onCommit, OnCommitted: cfg.OnCommitted, TimerFired: cfg.TimerFired,
		OnRejected: func(kind abi.EventKind, err *sqlhost.RejectedError) {
			report(err, durable.ReportInfo{Type: durable.ReportListenerError})
		},
		// Pi reads HarnessSettings at each use: the owner resends them to the core when they changed (sqlhost Options.Settings).
		// A settings reader that throws fails the reads that need it (resolveAgent, the task runtime), not the owner: the
		// core keeps the copy it has.
		Settings: func() (text []byte) {
			defer func() {
				if recovered := recover(); recovered != nil {
					report(fmt.Errorf("Harness settings: %s", durable.ErrorMessage(recovered)), durable.ReportInfo{Type: durable.ReportHostError, Callback: durable.HostCallbackSettings})
					text = nil
				}
			}()
			//portlint:allow mapkeyorder driver-to-core wire (durable/core/host/PROTOCOL.md): the core reads the settings members by name, as open's
			text, _ = json.Marshal(s.settingsJSON())
			return text
		},
		OnCancel: func(id uint32) {
			if s.tasks != nil {
				s.tasks.ended(id)
			}
		},
	})
	client = NewClient(s.host)
	client.created = cfg.Harness.ConversationCreated
	s.client = client
	step, err := s.openPayload()
	if err != nil {
		_ = s.host.Close(ctx)
		return nil, err
	}
	if err := s.host.Send(ctx, abi.Event{Kind: abi.EventOpen, Payload: step}); err != nil {
		_ = s.host.Close(ctx)
		return nil, err
	}
	// Harness.open resolves when the scheduler's open commit has landed (harness.ts open: `await tasks.open()`): its recovery of
	// the tasks a crash cut off, and the reports of the ones it parks, are in by then. A Harness over the core waits for the
	// same request the cf host sends.
	if cfg.Mode != "session" {
		if _, err := client.API(ctx, "opened", nil); err != nil {
			_ = s.host.Close(ctx)
			return nil, err
		}
	}
	s.unsub = cfg.Harness.Registry.Subscribe(func() {
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return
		}
		if body, err := s.registryPayload(); err == nil {
			_ = s.host.Send(context.Background(), abi.Event{Kind: abi.EventRegistry, Payload: body})
		}
	})
	return s, nil
}

func (s *coreSession) resolveSettings() durable.Settings {
	if s.cfg.Harness.Settings == nil {
		return harness.ResolveSettings(nil)
	}
	return harness.ResolveSettings(s.cfg.Harness.Settings())
}

// settingsJSON is the resolved settings as data: extensions are names (PROTOCOL.md, open).
func (s *coreSession) settingsJSON() map[string]any {
	st := s.resolveSettings()
	names := []string(nil)
	if st.Extensions != nil {
		names = make([]string, 0, len(st.Extensions))
		for _, e := range st.Extensions {
			names = append(names, e.Name)
		}
	}
	out := map[string]any{
		"stream": st.Stream, "retry": st.Retry, "compaction": st.Compaction, "progress": st.Progress,
		"toolExecution": st.ToolExecution, "steeringMode": st.SteeringMode, "followUpMode": st.FollowUpMode,
		"contextRetentionMs": retentionJSON(st.ContextRetentionMs), "documentRetentionMs": retentionJSON(st.DocumentRetentionMs),
		"afterFailedRun": st.AfterFailedRun, "restartLimit": retentionJSON(st.RestartLimit),
	}
	if names != nil {
		out["extensions"] = names
	}
	return out
}

// retentionJSON is a retention as JSON can carry it: Infinity, which keeps for good, becomes the largest finite number.
func retentionJSON(ms float64) float64 {
	if math.IsInf(ms, 1) {
		return math.MaxFloat64
	}
	return ms
}

func (s *coreSession) openPayload() ([]byte, error) {
	snapshot, err := s.table.snapshot(s.cfg.Harness.Registry.Snapshot())
	if err != nil {
		return nil, err
	}
	mode, sidecar := s.cfg.Mode, s.cfg.Sidecar
	if mode == "" {
		mode = "harness"
	}
	if sidecar == "" {
		sidecar = "index"
	}
	//portlint:allow mapkeyorder driver-to-core wire (durable/core/host/PROTOCOL.md): the core reads its members by name and stores none of this object
	return json.Marshal(map[string]any{
		"mode": mode, "settings": s.settingsJSON(), "models": s.catalogue(), "registry": snapshot, "sidecar": sidecar, "delta": true, "hostAdmission": true,
		"hasEnv": s.cfg.Harness.Env != nil, "onWake": s.cfg.Harness.OnWake != nil,
	})
}

// catalogue is open's models: the entries the core may name, as {provider, id, contextWindow} (core turnhost.go
// contextWindow reads exactly these). ModelsInUse when set, else every model of the Harness models object that lists
// its models, as the JS host does (host/cf facade harness.ts, PROTOCOL: default every model the models object lists).
func (s *coreSession) catalogue() []map[string]any {
	models := s.cfg.ModelsInUse
	if models == nil {
		if lister, ok := s.cfg.Harness.CurrentModels().(interface {
			GetModels(provider ...string) []*ai.Model
		}); ok {
			models = lister.GetModels()
		}
	}
	out := make([]map[string]any, 0, len(models))
	for _, m := range models {
		if m == nil || m.Provider == nil {
			continue
		}
		out = append(out, map[string]any{"provider": m.Provider.ID(), "id": m.ID, "contextWindow": m.Capabilities.ContextWindow})
	}
	return out
}

func (s *coreSession) registryPayload() ([]byte, error) {
	snapshot, err := s.table.snapshot(s.cfg.Harness.Registry.Snapshot())
	if err != nil {
		return nil, err
	}
	//portlint:allow mapkeyorder driver-to-core wire (durable/core/host/PROTOCOL.md): the core reads its members by name and stores none of this object
	return json.Marshal(map[string]any{"registry": snapshot})
}

// registryJSON is the registry snapshot as open.registry carries it.
func (s *coreSession) registryJSON() ([]byte, error) {
	snapshot, err := s.table.snapshot(s.cfg.Harness.Registry.Snapshot())
	if err != nil {
		return nil, err
	}
	return json.Marshal(snapshot)
}

// Resume enables task scheduling and makes sleeping tasks that are due runnable: the core keeps scheduling paused until the host
// sends the resume event (ABI event 17), and a host wakeup, such as an alarm, sends it again (scheduler.ts resume).
func (s *coreSession) Resume() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		panic(harness.ErrClosed)
	}
	s.resumed = true
	s.mu.Unlock()
	//portlint:allow ctxbackground Harness.resume() takes no context (harness.ts resume): the event is a notification, not cancellable work
	_ = s.host.Send(context.Background(), abi.Event{Kind: abi.EventResume})
}

// kick is the scheduler's own resume, which the calls that ask for progress make (scheduler.ts resume: it enables scheduling and
// never fails). Only Harness.resume asserts that the Harness is open (harness.ts :238), so a call that races Close is rejected
// as closed by the call itself and must not panic here.
func (s *coreSession) kick() { s.resume() }

// resume enables scheduling and reports false when the Session is closed.
func (s *coreSession) resume() bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	if s.resumed {
		s.mu.Unlock()
		return true
	}
	s.resumed = true
	s.mu.Unlock()
	_ = s.host.Send(context.Background(), abi.Event{Kind: abi.EventResume})
	return true
}

// Host exposes the owner goroutine's host for counters and tests.
func (s *coreSession) Host() *sqlhost.Host { return s.host }

// Close seals the core, cancels effects, closes the store.
func (s *coreSession) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.drained = make(chan struct{})
	if s.admitted == 0 {
		close(s.drained)
	}
	listeners := make([]int, 0, len(s.closeListeners))
	for id := range s.closeListeners {
		listeners = append(listeners, id)
	}
	slices.Sort(listeners)
	calls := make([]func(), len(listeners))
	for i, id := range listeners {
		calls[i] = s.closeListeners[id]
	}
	s.closeListeners = nil
	drained := s.drained
	s.mu.Unlock()
	// A close listener's panic is reported and the others still run (session.ts subscribeClose).
	for _, call := range calls {
		session.Contained(call, s.reportListener)
	}
	// Harness.close()'s synchronous part (session.ts close :352-366): the scheduler seals at the call, so a step the owner has queued
	// already (a failed phase's) sees the closing flag and writes no fault. The seal queues behind the events that arrived before
	// the call, as the microtasks that ran before close() did in Pi.
	s.host.Post(abi.Event{Kind: abi.EventSeal})
	// Admission is sealed; work admitted before it settles before the core and the store close (session.ts close :351-368,
	// where the close job is enqueued behind the line). The caller's context bounds only the wait, as awaitWithContext does:
	// the close itself finishes on its own.
	closed := make(chan error, 1)
	go func() {
		<-drained
		closed <- s.finishClose(context.WithoutCancel(ctx))
	}()
	select {
	case err := <-closed:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// finishClose ends the watches, seals the core and closes the store, once nothing admitted is in flight.
func (s *coreSession) finishClose(ctx context.Context) error {
	if s.unsub != nil {
		s.unsub()
	}
	s.watches.closeSession()
	// The handlers join while the core still takes events: a run's end is reported to the core, which ends its invocation and
	// answers the abort that joined it (scheduler.ts abort awaits run.done; close joins every invocation before it closes Storage).
	if err := s.host.WaitEffects(ctx); err != nil {
		return err
	}
	if s.tasks != nil {
		s.tasks.closeAll()
	}
	// The core closes behind its line (session.ts close): the write-back of the tasks it stopped and the statements of the jobs
	// before it land on later steps, so the host closes once the core has answered the close (request 0).
	_, _ = s.client.request(ctx, abi.NoticeAPIResult, 0, abi.Event{Kind: abi.EventClose}, always)
	err := s.host.Close(ctx)
	s.settleEnd()
	return err
}

// Closed returns a channel closed once the Session ended: after close reached the core and the store.
func (s *coreSession) Closed() <-chan struct{} { return s.ended }

// End returns how the Session ended once Closed is closed.
func (s *coreSession) End() durable.SessionEnd {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.end
}

// settleEnd records the Session's end: failed with the commit failure that ended it, otherwise closed.
func (s *coreSession) settleEnd() {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.ended:
		return
	default:
	}
	s.end = durable.SessionEnd{Reason: durable.SessionEndClosed}
	if fatal := s.client.poisoned.Load(); fatal != nil {
		s.end = durable.SessionEnd{Reason: durable.SessionEndFailed, Error: fatal}
	}
	close(s.ended)
}

// admit admits one call that asks the owner for work, or rejects it as closed once Close began (session.ts #assertUsable). The
// returned release marks the call done; Close waits for every admitted call before it closes the core.
func (s *coreSession) admit() (release func(), err error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, harness.ErrClosed
	}
	s.admitted++
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		s.admitted--
		if s.closed && s.admitted == 0 {
			close(s.drained)
		}
		s.mu.Unlock()
	}, nil
}

// lookupTool is Tools.Lookup: the core has resolved the called tool among the tools the model is offered (a model-issued
// call) or the tools callable by tools (a nested call, tool.ts findTool), so the handler finds it in either list.
func (s *coreSession) lookupTool(ctx context.Context, conversation durable.ConversationId, name string) (durable.ToolRegistration, bool, error) {
	agent, err := s.agent(ctx, conversation)
	if err != nil {
		return durable.ToolRegistration{}, false, err
	}
	for _, tool := range slices.Concat(agent.Tools, agent.Callable) {
		if tool.Name == name {
			return *tool, true, nil
		}
	}
	return durable.ToolRegistration{}, false, nil
}

// callbackLate defers to the client the session creates after the host starts.
type callbackLate struct {
	get   func() *Client
	named map[string]Callback
}

func (c *callbackLate) handle(ctx context.Context, call *sqlhost.Call) error {
	return (&Callbacks{Client: c.get(), Named: c.named}).run(ctx, call)
}

// hookHandler answers the hooks the host owns (resolveAgent, registrySnapshot, prepareArguments, validateArguments and complete
// take no registry handler) and hands the rest to Hooks.
func (s *coreSession) hookHandler(hooks *Hooks) sqlhost.Handler {
	return func(ctx context.Context, call *sqlhost.Call) error {
		var head struct {
			Name           string                 `json:"name"`
			ConversationId durable.ConversationId `json:"conversationId"`
			Payload        json.RawMessage        `json:"payload"`
		}
		if err := json.Unmarshal(call.Payload, &head); err != nil {
			return fmt.Errorf("hook payload: %w", err)
		}
		switch head.Name {
		case "checkStructuredOutput":
			return s.structuredOutputHook(call, head.Payload)
		case "prepareArguments", "validateArguments":
			return s.argumentsHook(call, head.Name, head.Payload)
		case "complete":
			return s.completeHook(ctx, call, head.Payload)
		case "registrySnapshot":
			// registry.snapshot() where Pi's #decide calls it (scheduler.ts:955): the snapshot JSON of open.registry.
			body, err := s.registryJSON()
			if err != nil {
				return fmt.Errorf("registrySnapshot result: %w", err)
			}
			call.Post(abi.Event{Kind: abi.EventHookDone, ID: call.ID, Phase: abi.OutcomeResult, Payload: body})
			return nil
		case "resolveAgent":
		default:
			return hooks.Handler()(ctx, call)
		}
		var in struct {
			State *harness.AgentState `json:"state"`
		}
		if len(head.Payload) > 0 {
			if err := json.Unmarshal(head.Payload, &in); err != nil {
				return fmt.Errorf("resolveAgent payload: %w", err)
			}
		}
		agent := harness.ResolveAgent(in.State, s.cfg.Harness.Registry.Snapshot(), s.resolveSettings(), func(err error, extension string) {
			s.report(err, durable.ReportInfo{Type: durable.ReportExtensionError, ConversationId: &head.ConversationId, Extension: extension})
		})
		//portlint:allow mapkeyorder driver-to-core wire (durable/core/host/PROTOCOL.md): the core reads its members by name and stores none of this object
		body, err := json.Marshal(s.agentJSON(agent))
		if err != nil {
			return fmt.Errorf("resolveAgent result: %w", err)
		}
		call.Post(abi.Event{Kind: abi.EventHookDone, ID: call.ID, Phase: abi.OutcomeResult, Payload: body})
		return nil
	}
}

// structuredOutputHook answers checkStructuredOutput, which the core asks of the host after afterTool and before a result is
// settled: the tool's schema lives in the host's registration, so the host judges the structured output (harness/tool.ts
// structuredOutputError) and answers {error} with why it breaks the contract, or {} when it keeps it.
func (s *coreSession) structuredOutputHook(call *sqlhost.Call, raw json.RawMessage) error {
	var in struct {
		ToolName string          `json:"toolName"`
		Result   json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return fmt.Errorf("checkStructuredOutput payload: %w", err)
	}
	tool, err := s.namedTool(in.ToolName, nil)
	if err != nil {
		return err
	}
	result, err := decodeToolResult(in.Result)
	if err != nil {
		return fmt.Errorf("checkStructuredOutput result: %w", err)
	}
	var answer struct {
		Error string `json:"error,omitempty"`
	}
	answer.Error = harness.StructuredOutputError(tool, result)
	body, err := json.Marshal(answer)
	if err != nil {
		return fmt.Errorf("checkStructuredOutput answer: %w", err)
	}
	call.Post(abi.Event{Kind: abi.EventHookDone, ID: call.ID, Phase: abi.OutcomeResult, Payload: body})
	return nil
}

// completeHook is the model call of a compaction summary (compaction.ts summarize: runtime.models.completeSimple; the JS
// host's complete hook). It answers the terminal assistant message, which carries a provider failure as its stopReason and
// errorMessage. A model the catalogue does not hold is a throw, as completeSimple's caller sees it in the JS host.
func (s *coreSession) completeHook(ctx context.Context, call *sqlhost.Call, raw json.RawMessage) error {
	var in struct {
		Model   payload.ModelRef `json:"model"`
		Context struct {
			Messages json.RawMessage `json:"messages"`
		} `json:"context"`
		Options json.RawMessage `json:"options"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return fmt.Errorf("complete payload: %w", err)
	}
	messages, err := durable.DecodeMessages(in.Context.Messages)
	if err != nil {
		return fmt.Errorf("complete messages: %w", err)
	}
	var options ai.StreamOptions
	if len(in.Options) > 0 {
		if err := json.Unmarshal(in.Options, &options); err != nil {
			return fmt.Errorf("complete options: %w", err)
		}
	}
	models := s.cfg.Harness.CurrentModels()
	var model *ai.Model
	if models != nil {
		model = models.GetModel(in.Model.Provider, in.Model.ModelID)
	}
	if model == nil {
		call.Post(abi.Event{Kind: abi.EventHookDone, ID: call.ID, Phase: abi.OutcomeThrown, Payload: wireError(fmt.Errorf("Model %s/%s is not available", in.Model.Provider, in.Model.ModelID))})
		return nil
	}
	// The JS host passes the hook's abort signal: a provider callback (a faux step) reads options.Signal.
	options.Signal = ctx
	message := models.CompleteSimple(ctx, model, ai.Context{Messages: messages}, options)
	body, err := json.Marshal(message)
	if err != nil {
		// A message that is not strict JSON cannot reach the core. In Pi the compaction's commit rejects it and the scheduler
		// faults the task (harness-compaction.test.ts:1954); a throw of the model call faults it the same way and keeps the
		// Session up, with the strict-JSON error the reference reports.
		if _, strict := durable.ToJsonValue(message); strict != nil {
			err = strict
		}
		call.Post(abi.Event{Kind: abi.EventHookDone, ID: call.ID, Phase: abi.OutcomeThrown, Payload: wireError(err)})
		return nil
	}
	call.Post(abi.Event{Kind: abi.EventHookDone, ID: call.ID, Phase: abi.OutcomeResult, Payload: body})
	return nil
}

// argumentsHook answers prepareArguments and validateArguments, which the core asks of the host by name (core
// turnhost.go checkArguments; the JS host's hostHooks in host/cf facade harness.ts): {arguments} or {error} with the text
// of what the repair or validation threw (harness/tool.ts prepare, validate). The tool is the one the core sent by
// handler, else the first installed tool of that name (host/cf registry.ts tool).
func (s *coreSession) argumentsHook(call *sqlhost.Call, name string, raw json.RawMessage) error {
	var in struct {
		ToolName  string          `json:"toolName"`
		Handler   *int            `json:"handler"`
		Call      json.RawMessage `json:"call"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return fmt.Errorf("%s payload: %w", name, err)
	}
	tool, err := s.namedTool(in.ToolName, in.Handler)
	if err != nil {
		return err
	}
	args := in.Arguments
	if len(args) == 0 {
		args = json.RawMessage("null")
	}
	// The answer keeps the member order of the arguments the core sent: Pi validates and repairs a clone of the call's
	// arguments object (validation.ts validateToolArguments), and the execute checkpoint stores the result (harness/tool.ts).
	var answer struct {
		Arguments json.RawMessage `json:"arguments,omitempty"`
		Error     *string         `json:"error,omitempty"`
	}
	if name == "prepareArguments" {
		var decoded map[string]any
		if err := json.Unmarshal(args, &decoded); err != nil {
			return fmt.Errorf("prepareArguments arguments: %w", err)
		}
		prepared, failure := prepareToolArguments(tool, decoded)
		if failure != "" {
			answer.Error = &failure
		} else {
			if object, isObject := prepared.(map[string]any); isObject {
				answer.Arguments, err = ai.MarshalJSONInSourceOrder(object, args)
			} else {
				answer.Arguments, err = json.Marshal(prepared)
			}
			if err != nil {
				return fmt.Errorf("prepareArguments result: %w", err)
			}
		}
	} else {
		if len(in.Call) > 0 {
			var toolCall ai.ToolCall
			if err := json.Unmarshal(in.Call, &toolCall); err != nil {
				return fmt.Errorf("validateArguments call: %w", err)
			}
		}
		validated, err := ai.ValidateToolArgumentsSchema(tool.Name, tool.Parameters, args)
		if err != nil {
			text := err.Error()
			answer.Error = &text
		} else {
			answer.Arguments = validated
		}
	}
	body, err := json.Marshal(answer)
	if err != nil {
		return fmt.Errorf("%s result: %w", name, err)
	}
	call.Post(abi.Event{Kind: abi.EventHookDone, ID: call.ID, Phase: abi.OutcomeResult, Payload: body})
	return nil
}

func (s *coreSession) namedTool(name string, handler *int) (*durable.ToolRegistration, error) {
	if handler != nil {
		if tool, ok := s.table.tool(*handler); ok {
			return tool, nil
		}
		return nil, fmt.Errorf("handler %d is not a tool", *handler)
	}
	for _, installed := range s.cfg.Harness.Registry.Snapshot().Tools() {
		if installed.Tool.Name == name {
			return installed.Tool, nil
		}
	}
	return nil, fmt.Errorf("tool %s is not installed", name)
}

// prepareToolArguments is harness/tool.go prepareArguments: the repaired arguments, or the text of what the repair
// threw. A typed result is its JSON copy; a result that is not an object reaches validation as it is.
func prepareToolArguments(tool *durable.ToolRegistration, args map[string]any) (out any, failure string) {
	if tool.PrepareArguments == nil {
		return args, ""
	}
	var prepared any
	err := func() (err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				if e, ok := recovered.(error); ok {
					err = e
				} else {
					err = fmt.Errorf("%v", recovered)
				}
			}
		}()
		prepared, err = tool.PrepareArguments(args)
		return err
	}()
	if err != nil {
		return nil, err.Error()
	}
	if _, ok := prepared.(map[string]any); ok || prepared == nil {
		return prepared, ""
	}
	// Any JSON value goes on to validation (tool.ts prepare casts the result, it does not check it). ToJsonValue copies a
	// typed object (a struct, a map of another element type) into an object that keeps its encoding order.
	copied, err := durable.ToJsonValue(prepared)
	if err != nil {
		return nil, err.Error()
	}
	return copied, ""
}

// reportNotice forwards a failure the core reported (runtime.report in a built-in task) to the host's OnReport.
func (s *coreSession) reportNotice(n abi.Notice) {
	if n.Kind != abi.NoticeReport {
		return
	}
	var body reportBody
	// Other report notices (a fired timer) carry no error.
	if json.Unmarshal(n.Payload, &body) != nil || body.Name == "" {
		return
	}
	s.report(errors.New(body.Message), body.info())
}

// reportBody is the report notice's payload: the error {name, message} with the ReportInfo the core attached.
type reportBody struct {
	Name    string `json:"name"`
	Message string `json:"message"`
	Info    struct {
		Type           durable.ReportType      `json:"type"`
		Task           *durable.ReportTask     `json:"task"`
		Extension      string                  `json:"extension"`
		Reason         string                  `json:"reason"`
		Callback       durable.HostCallback    `json:"callback"`
		ConversationId *durable.ConversationId `json:"conversationId"`
	} `json:"info"`
}

func (b reportBody) info() durable.ReportInfo {
	i := b.Info
	if i.Type == "" {
		return durable.ReportInfo{Type: durable.ReportListenerError}
	}
	return durable.ReportInfo{Type: i.Type, Task: i.Task, Extension: i.Extension, Reason: i.Reason, Callback: i.Callback, ConversationId: i.ConversationId}
}

// Report is session.ts report: it hands a non-fatal failure, such as the error of a state listener or of the typed listener of
// durable.DocumentStateOf (session.ts documentState), to HarnessOptions.OnReport as a listener failure.
func (s *coreSession) Report(err error) { s.reportListener(err) }

// reportListener is report for a listener's failure, the default source of a report.
func (s *coreSession) reportListener(err error) {
	s.report(err, durable.ReportInfo{Type: durable.ReportListenerError})
}

func (s *coreSession) report(err error, info durable.ReportInfo) {
	if s.cfg.Harness.OnReport != nil {
		s.cfg.Harness.OnReport(err, info)
	}
}

// parametersJSON is a tool's JSON Schema as the core compares it (turn.DeclarationsEqual compares declarations as JSON text,
// so key order is part of the declaration). ai.ToolSchema retains the declaration order of a schema it decoded from JSON in its
// own encoding; marshalling the Parameters map alone would sort the keys.
func parametersJSON(tool *durable.ToolRegistration) json.RawMessage {
	whole, err := json.Marshal(tool.ToolSchema)
	if err == nil {
		var parts struct {
			Parameters json.RawMessage `json:"parameters"`
		}
		if json.Unmarshal(whole, &parts) == nil && len(parts.Parameters) > 0 {
			return parts.Parameters
		}
	}
	//portlint:allow mapkeyorder fallback for a schema whose ToolSchema encoding fails; the ToolSchema encoding above keeps the declaration order
	parameters, _ := json.Marshal(tool.Parameters)
	return parameters
}

// agentJSON is the resolved agent as the core caches it (PROTOCOL.md): handler indexes into the table.
func (s *coreSession) agentJSON(agent durable.Agent) map[string]any {
	extensions := make([]string, 0, len(agent.Extensions))
	for _, e := range agent.Extensions {
		extensions = append(extensions, e.Name)
	}
	snapshotTools := func(list []*durable.ToolRegistration) []snapshotTool {
		tools := make([]snapshotTool, 0, len(list))
		for _, tool := range list {
			parameters := parametersJSON(tool)
			replay := string(tool.Replay)
			if replay == "" {
				replay = string(durable.ReplayUnsafe)
			}
			t := snapshotTool{Extension: harness.ToolExtension(agent, tool.Name), Name: tool.Name, Description: tool.Description, Parameters: parameters, Replay: replay, Handler: s.table.id(tool, "tool", toolHandle{tool}), ExecutionMode: string(tool.ExecutionMode), StructuredOutput: tool.StructuredOutputSchema != nil}
			if limits, err := outputLimitsJSON(tool); err == nil {
				t.OutputLimits = limits
			} else {
				s.reportListener(err)
			}
			if tool.PrepareArguments != nil {
				id := s.table.id(tool, "prepare", prepareHandle{PrepareArguments(tool.PrepareArguments)})
				t.Prepare = &id
			}
			tools = append(tools, t)
		}
		return tools
	}
	tools := snapshotTools(agent.Tools)
	sections := make([]snapshotSection, 0, len(agent.Sections))
	for _, section := range agent.Sections {
		sections = append(sections, snapshotSection{Extension: harness.SectionExtension(agent, section.Key), Key: section.Key, Tag: section.Tag == nil || *section.Tag, Handler: s.table.id(section, "section", sectionHandle{section})})
	}
	// The hooks of the selected extensions, in extension order (agent.ts agentHooks; the JS host's agentJSON): hook name to handler.
	hooks := []snapshotHook{}
	for _, e := range agent.Extensions {
		for i := range e.Hooks {
			hook := s.table.hook(&e.Hooks[i])
			hook.Extension = e.Name
			hooks = append(hooks, hook)
		}
	}
	out := map[string]any{"thinkingLevel": agent.ThinkingLevel, "extensions": extensions, "tools": tools, "callable": snapshotTools(agent.Callable), "sections": sections, "hooks": hooks}
	if agent.Model != nil {
		out["model"] = agent.Model
	}
	if agent.Instructions != nil {
		out["instructions"] = *agent.Instructions
	}
	if agent.Cwd != nil {
		out["cwd"] = *agent.Cwd
	}
	return out
}

// requestID mints a UUID for a submission that has none.
func requestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// published decodes a published notice.
func published(n abi.Notice) (payload.Published, bool) {
	if n.Kind != abi.NoticePublished {
		return payload.Published{}, false
	}
	var p payload.Published
	return p, json.Unmarshal(n.Payload, &p) == nil
}

// armTimer arms a timer effect of the core through HarnessOptions.SetTimer (sqlhost Options.SetTimer): the host's timers pace
// the core's work, and a background timer the host declines is not kept.
func (s *coreSession) armTimer(callback func(), delayMs float64, background bool) func() {
	if background {
		return s.timers.Background(callback, delayMs)
	}
	return s.timers.Foreground(callback, delayMs)
}

// wakeNotice delivers a change of the Harness's wake to HarnessOptions.OnWake, off the owner goroutine (scheduler.ts #scheduleWake).
// A panic is reported, and the core offers the value again at its next change.
func (s *coreSession) wakeNotice(n abi.Notice) {
	if n.Kind != abi.NoticeWake || s.cfg.Harness.OnWake == nil {
		return
	}
	var body struct {
		Active bool     `json:"active"`
		At     *float64 `json:"at"`
	}
	if json.Unmarshal(n.Payload, &body) != nil {
		return
	}
	s.mu.Lock()
	s.wakes = append(s.wakes, harness.Wake{Active: body.Active, At: body.At})
	start := !s.wakeRunning
	s.wakeRunning = true
	s.mu.Unlock()
	if start {
		//portlint:allow golifetime the worker ends when the queue is empty or the Session closed
		go s.deliverWakes()
	}
}

// deliverWakes calls HarnessOptions.OnWake with each queued wake in order, off the owner goroutine: only a changed value is
// delivered, and one whose delivery panicked is offered again at the next recomputation (scheduler.ts #scheduleWake).
func (s *coreSession) deliverWakes() {
	for {
		s.mu.Lock()
		if len(s.wakes) == 0 || s.closed {
			s.wakes, s.wakeRunning = nil, false
			s.mu.Unlock()
			return
		}
		wake := s.wakes[0]
		s.wakes = s.wakes[1:]
		last := s.lastWake
		s.mu.Unlock()
		if last != nil && last.Active == wake.Active && (last.At == nil) == (wake.At == nil) && (wake.At == nil || *last.At == *wake.At) {
			continue
		}
		if err := s.callOnWake(wake); err != nil {
			s.report(err, durable.ReportInfo{Type: durable.ReportHostError, Callback: durable.HostCallbackOnWake})
			continue
		}
		s.mu.Lock()
		s.lastWake = &wake
		s.mu.Unlock()
	}
}

func (s *coreSession) callOnWake(wake harness.Wake) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if failure, ok := recovered.(error); ok {
				err = failure
			} else {
				err = errors.New(durable.ErrorMessage(recovered))
			}
		}
	}()
	s.cfg.Harness.OnWake(wake)
	return nil
}

// Wake is when this Session next needs a running process, answered by the core from its scheduler's memory.
func (s *coreSession) Wake() harness.Wake {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		panic(session.ErrSessionClosed) // assertUsable (harness.ts:267, session.ts:870-872), not the host's own closed error
	}
	//portlint:allow ctxbackground Harness.wake() is synchronous and takes no context (harness.ts wake): an immediate answer from the core's memory
	raw, err := s.client.API(context.Background(), "wake", nil)
	if err != nil {
		panic(err)
	}
	var body struct {
		Active bool     `json:"active"`
		At     *float64 `json:"at"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		panic(err)
	}
	return harness.Wake{Active: body.Active, At: body.At}
}
