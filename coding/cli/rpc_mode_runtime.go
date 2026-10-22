package cli

// Ports packages/coding-agent/src/modes/rpc/rpc-mode.ts rebindSession and the runtime host wiring.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/jsloop"
)

// rpcModeSession is RPC mode's own state for one Session it serves (rpc-mode.ts keeps it in runRpcMode's closure).
type rpcModeSession struct {
	Catalog   headlessCommandCatalog
	published atomic.Pointer[headlessCommandCatalog]
	// join counts the extension commands of this Session's extension host whose responses are not written.
	join atomic.Pointer[rpcCommandJoin]
	// writing is set once the first session_start of this Session is dispatched. Pi subscribes its stdout writer after `await session.bindExtensions()` (rpc-mode.ts:319-361), so what a session_start handler emits before then (entry_appended, session_info_changed) is not written. The second bind a replacement command runs finds it set, and its events are written.
	writing atomic.Bool
}

// rpcStartup carries the startup-only inputs of the first RPC Session.
type rpcStartup struct {
	Manager    *coding.SessionManager
	ResumePath string
	SessionDir string
}

// rpcInputs builds RPC mode's Session inputs from a build; owner is the process context its reloaded extension processes live under. Replacement Sessions rebuild through the same builder for their destination cwd. The factory keeps no RPC state for a Session: RPC mode reads the Session and its resource loader.
func (b *cliRuntimeBuilder) rpcInputs(owner context.Context, build *cliBuild, startup *rpcStartup) cliSessionInputs {
	flags, settings := build.Flags, build.Settings
	resources := b.buildResourceLoader(owner, build)
	scopePatterns := flags.Models
	if scopePatterns == nil {
		scopePatterns = settings.EnabledModels
	}
	start := coding.SessionStartOptions{
		// The Session builds its default system prompt from this loader, as AgentSession._rebuildSystemPrompt does.
		ResourceLoader: resources,
		ScopedModels:   extensionScopedModels(build.Services, scopePatterns),
		Model:          build.Model,
		ThinkingLevel:  ai.ModelThinkingLevel(flags.Thinking),
		// The tool selection is the build's, the one print, JSON and interactive modes start their Sessions with (main.ts:822-830 passes the same tools, excludeTools and noTools to every mode).
		AllowedTools:           build.Allowed,
		InitialActiveToolNames: build.InitialActiveToolNames,
		DefaultToolModifiers:   build.DefaultToolModifiers,
		NoTools:                build.NoTools,
		ExcludedTools:          build.ExcludedTools,
		SkipBuiltinTools:       build.SkipBuiltinTools,
	}
	if startup != nil {
		start.SessionManager = startup.Manager
		start.ResumePath = startup.ResumePath
		start.SessionDir = startup.SessionDir
		start.SessionID = flags.SessionID
		start.NoSession = flags.NoSession
		start.CWDOverride = flags.sessionCwdOverride
	}
	return cliSessionInputs{
		Services: build.Services, Extensions: build.Extensions, Start: start, Host: build.Host, Bridge: build.Bridge, Runtime: build.Runtime,
		Invalidate: func(message string) {
			if build.Host != nil {
				build.Host.Invalidate(message)
			}
		},
		Release: func(reason string) {
			if build.Host != nil {
				build.Host.Shutdown(reason)
			}
			build.Services.Close()
		},
	}
}

// rpcHost is RPC mode's binding of a Runtime to the protocol streams. It owns what Pi's rebindSession re-creates for every replacement Session: extension bindings, the event subscription and the extension UI wiring.
type rpcHost struct {
	ctx    context.Context
	cancel context.CancelFunc
	flags  Args

	ui        *rpcUIContext
	writeRPC  func(any)
	responses *rpcResponseTurn

	rt *coding.Runtime

	promptWG *sync.WaitGroup
	tasks    *rpcTaskGroup
	// commandsInFlight counts the extension commands whose prompt response is not yet written; while one is, an extension's sendUserMessage or sendMessage run is staged on the executor (rpcAdmission.extensionRunStage).
	commandsInFlight atomic.Int32

	// background holds the extension UI calls no request started while a replacement an RPC command requested runs (rpcBackgroundGate).
	background rpcBackgroundGate

	// detached is set once shutdown stops publishing Session events (rpc-mode.ts shutdown unsubscribes first).
	detached atomic.Bool
	// forwarding counts running event forwarders; shutdown flushes only while one runs.
	forwarding atomic.Int32
	// requestShutdown is an extension's ctx.shutdown(); afterSettled and commandDone are where upstream checks for it.
	requestShutdown func()
	afterSettled    func()
	commandDone     func()
	// settle holds stdin between a published agent_end and agent_settled.
	settle rpcSettleGate
	// turns models where Pi's event loop turns (D189); loop reports the run's chain to it.
	turns jsloop.Turns
	loop  rpcLoopChain
	// drain is the process-owner callback for a Node event-loop drain after input end.
	drain func()
	// stdoutWait waits for the raw-stdout tail, the barrier of Pi's waitForRawStdoutBackpressure.
	stdoutWait func(context.Context)

	admission atomic.Pointer[rpcAdmission]
	// forwarders joins every Session's event forwarder. A replaced Session's channel closes with the Session.
	forwarders sync.WaitGroup
	// conversionErr receives the first event conversion failure; conversionOnce keeps a later forwarder from blocking on the full channel.
	conversionErr  chan error
	conversionOnce sync.Once

	mu                  sync.Mutex
	sessions            map[*coding.Session]*rpcModeSession
	detachModelRegistry func()
	unsubscribeName     func()
	// commandJoin joins the current Session's in-flight extension commands for the shutdown flush; nil when its runtime cannot report a suspended command.
	commandJoin atomic.Pointer[rpcCommandJoin]
	// unsubscribeEvents detaches every Session's event serializer and stdout-backpressure listener.
	unsubscribeEvents []func()
}

func (h *rpcHost) current() *coding.Session { return h.rt.Session() }

// state returns RPC mode's state for session. Binding a new Session drops the state of the Sessions it replaced; handlers bound to a replaced Session keep the state they captured.
func (h *rpcHost) state(session *coding.Session) *rpcModeSession {
	h.mu.Lock()
	defer h.mu.Unlock()
	st, ok := h.sessions[session]
	if !ok {
		st = &rpcModeSession{}
		clear(h.sessions)
		if h.sessions == nil {
			h.sessions = map[*coding.Session]*rpcModeSession{}
		}
		h.sessions[session] = st
	}
	return st
}

// bind wires session to RPC mode as rpc-mode.ts rebindSession does and emits session_start for event: the Session's own reason, which is startup for the first Session and the replacement reason otherwise. A reload binds the same Session to a replacement runner: the resources and the prompt are the reloaded ones, and the Session's subscriptions and startup state stay (agent-session.ts:3575-3625).
func (h *rpcHost) bind(ctx context.Context, session *coding.Session, event extension.SessionStartEvent, reloading bool) error {
	st := h.state(session)
	services := session.Services()
	runner := session.ExtensionRunner()
	extensionHost, bridge := rpcExtensionHost(session)
	current := h.current

	// The replacement's Session dispatches session_start below: its extensions' deferred UI calls follow the command's response.
	if !acquireRPCReplacementHold(ctx) && h.commandsInFlight.Load() > 0 {
		h.background.holdForCommand()
	}
	if extensionHost != nil {
		extensionHost.SetBackgroundCallGate(h.background.wait)
	}

	if bridge != nil {
		bridge.SetUIContext(h.ui)
		bridge.SetWidgetRequestFunc(func(_ string, key string, lines []string, opts *extension.ExtensionWidgetOptions) {
			h.ui.SetWidget(key, lines, opts)
		})
	}

	if runner != nil {
		runner.AddErrorListener(func(err *extension.ExtensionError) {
			h.writeRPC(rpcExtensionErrorEvent(err))
		})
		runner.SetUIContext(h.ui, extension.ModeRPC)
		// Subprocess dialogs reach the RPC UI through the bridge; report their
		// ui_prompt_start/ui_prompt_end through this runner.
		if bridge != nil {
			bridge.SetUIPromptScope(runner)
		}
		runner.BindCommandActions(h.rt.ExtensionCommandActions(session))
	}
	commandCatalog := headlessCommandCatalog{
		runner: runner, mode: "rpc", promptTemplates: session.PromptTemplates(), skills: session.ResourceLoader().GetSkills().Skills,
		cwd: session.CWD(), agentDir: services.AgentDir(),
		notify: h.ui.Notify,
	}
	// Extension host calls read the published copy of the catalog; announce alone changes it.
	st.Catalog = commandCatalog
	st.published.Store(new(commandCatalog))
	if bridge != nil && h.drain != nil {
		extensionHost.SetRuntimeDrainHandler(func() {
			join := st.join.Load()
			if join == nil {
				h.drain()
				return
			}
			// The drain notification can follow the response frame of a command the host has not delivered yet: Pi's loop was still alive until that response was written. Waiting here would block the host's frame loop, which the join's suspension reports may need, so the exit waits apart from it. The host takes the loop-drain checkpoint before it calls this handler, so the join awaits a command that waits on exec, a session change or another host call, and a Node command until its own process reports that nothing keeps its loop alive; it does not await a command that waits only on a dialog, which stdin can no longer answer. The goroutine belongs to the process, not to runRPC: it ends in h.drain, the process exit, so no owner joins it.
			go func() {
				<-join.idle()
				h.drain()
			}()
		})
	}

	if reloading {
		detach := h.bindExtensionActions(session, st, runner, current)
		h.mu.Lock()
		if h.detachModelRegistry != nil {
			h.detachModelRegistry()
		}
		h.detachModelRegistry = detach
		h.mu.Unlock()
		return h.announce(ctx, session, st, event)
	}

	detach := h.bindExtensionActions(session, st, runner, current)

	// Publish name changes before the setter response. The listener subscribes before session_start to keep its order among the Session's listeners, and writes only once session_start is dispatched (st.writing), as Pi's writer subscribes after bindExtensions.
	unsubscribeName := session.Subscribe(func(event agent.AgentEvent) {
		if name, ok := event.(agent.SessionInfoChangedEvent); ok && st.writing.Load() && !h.detached.Load() {
			h.writeRPC(rpcSessionInfoChanged(name.Name))
		}
	})
	h.mu.Lock()
	if h.detachModelRegistry != nil {
		h.detachModelRegistry()
	}
	if h.unsubscribeName != nil {
		h.unsubscribeName()
	}
	h.detachModelRegistry, h.unsubscribeName = detach, unsubscribeName
	h.mu.Unlock()

	session.SetRetryContinuationScheduler(h.responses.Microtask)
	// The forwarder subscribes before session_start, which keeps its order among the Session's listeners, but writes only after it: rpc-mode.ts rebindSession subscribes after `await session.bindExtensions()`, so an event a session_start listener emits, such as a message it sends, is not written.
	writing := h.forward(session)
	if err := h.announce(ctx, session, st, event); err != nil {
		return err
	}
	writing.Store(true)
	// The name listener and the later binds read the Session's own flag.
	st.writing.Store(true)
	return nil
}

// announce drives the extension session lifecycle in RPC mode. Upstream fires session_start via session.bindExtensions (rpc-mode.ts:318) after wiring the mode's host-action bindings, then lets extensions add resources.
func (h *rpcHost) announce(ctx context.Context, session *coding.Session, st *rpcModeSession, event extension.SessionStartEvent) error {
	runner := session.ExtensionRunner()
	// The Session emits session_start, extends its resource loader with what resources_discover returns and rebuilds its system prompt from the loader (agent-session.ts bindExtensions).
	if err := session.EmitSessionStartTransition(event.Reason, event.PreviousSessionFile); err != nil {
		return err
	}
	catalog := st.Catalog
	catalog.runner = runner
	catalog.promptTemplates = session.PromptTemplates()
	catalog.skills = session.ResourceLoader().GetSkills().Skills
	st.Catalog = catalog
	st.published.Store(&catalog)
	var join *rpcCommandJoin
	if extensionHost, bridge := rpcExtensionHost(session); bridge != nil {
		join = attachRPCCommandJoin(extensionHost)
		extensionHost.SetSettleTailHandler(h.settle.tailHandlerReporter())
		extensionHost.SetTimerTurns(&h.turns)
	}
	st.join.Store(join)
	h.commandJoin.Store(join)
	h.admission.Store(h.newAdmission(session, runner, catalog, session.Services(), join))
	return nil
}

// rpcExtensionHost returns the extension host of session and its UI bridge. Both are nil for a Session whose Runtime has no extension host; the bridge is nil when no subprocess extension host runs.
func rpcExtensionHost(session *coding.Session) (coding.ExtensionHost, *subprocess.UIBridge) {
	if session == nil {
		return nil, nil
	}
	extensionHost := session.ExtensionHost()
	if extensionHost == nil {
		return nil, nil
	}
	return extensionHost, extensionHost.UIBridge()
}

// commandSuspendSource is the extension host side of a command join: it reports how many of its in-flight commands are suspended.
type commandSuspendSource interface {
	SetCommandSuspendHandler(fn func(suspended int))
	SetCommandWindowHandler(fn func(suspended int))
}

// attachRPCCommandJoin gives a Session's extension host a join of its own. Each host reports its own suspended count, so a replacement host cannot overwrite the count of a host that still has suspended commands.
func attachRPCCommandJoin(host commandSuspendSource) *rpcCommandJoin {
	join := &rpcCommandJoin{}
	host.SetCommandSuspendHandler(join.setSuspended)
	host.SetCommandWindowHandler(join.setWindowSuspended)
	return join
}

// extensionRunStage is the stage of a run an extension sends while one of its commands is in flight, or nil: a send outside a command runs as the Session runs it.
func (h *rpcHost) extensionRunStage() *coding.ExtensionRunStage {
	admission := h.admission.Load()
	if admission == nil || h.commandsInFlight.Load() == 0 {
		return nil
	}
	return admission.extensionRunStage()
}

// rebindAgain repeats the rebind Pi's rpc-mode runs after a replacement command: rpc-mode.ts calls rebindSession() once more after runtimeHost.newSession, switchSession and fork return, so the replacement's extensions receive session_start and resources_discover a second time. Extension-initiated replacements rebind once.
func (h *rpcHost) rebindAgain(ctx context.Context) error {
	session := h.current()
	return h.announce(ctx, session, h.state(session), session.StartEvent())
}

func (h *rpcHost) newAdmission(session *coding.Session, runner *inproc.Runner, catalog headlessCommandCatalog, services *coding.AgentSessionServices, commands *rpcCommandJoin) *rpcAdmission {
	registry := services.Registry()
	return &rpcAdmission{
		ctx: h.ctx, turn: h.responses, session: session, runner: runner, catalog: catalog,
		write: h.responses.write, turns: &h.turns, runs: h.promptWG, shared: h.tasks, commandDone: h.commandDone, commands: commands, commandsInFlight: &h.commandsInFlight, background: &h.background,
		validateModel: func() error {
			model := session.Model()
			if model == nil {
				return errors.New(codingagent.FormatNoAPIKeyFoundMessage("unknown"))
			}
			providerID := model.ProviderMeta.ProviderID
			if providerID == "" && model.Provider != nil {
				providerID = model.Provider.ID()
			}
			if providerID != "test-faux" && !registry.ModelRegistry.HasConfiguredAuth(providerID) {
				return errors.New(codingagent.FormatNoAPIKeyFoundMessage(providerID))
			}
			return nil
		},
	}
}

// forward writes the Session's events as upstream-compatible AgentSessionEvent JSON objects. Upstream pi RPC mode subscribes to session events and writes each event as a JSONL object after awaited extension handling and before Session persistence (rpc-mode.ts:346-363); rpcAgentEvent adapts pig's internal event structs to that wire shape. The Events consumer only drains and acknowledges delivery barriers. Nothing is written until the returned flag is set, which the caller does once session_start is dispatched (rpc-mode.ts:319-361).
func (h *rpcHost) forward(session *coding.Session) *atomic.Bool {
	writing := new(atomic.Bool)
	// A replaced Session's unsettled tail is over: its run was joined before the replacement.
	h.settle.reset()
	h.loop.turns = &h.turns
	h.loop.reset()
	h.loop.observeCommands(&h.responses.Loop)
	session.Agent().SetLoopTurns(&h.turns)
	// The gate sees an event ahead of its writer (agent_end) or after it (agent_settled), so a held command answers after the event that releases it.
	gateBefore := session.Subscribe(func(event agent.AgentEvent) { h.settle.before(event); h.loop.before(event) })
	// Upstream shutdown() unsubscribes the stdout forwarder, so nothing is written after detach.
	unsubscribe := subscribeRPCEvents(session, func(v any) {
		if writing.Load() && !h.detached.Load() {
			h.writeRPC(v)
		}
	}, func(err error) {
		h.conversionOnce.Do(func() { h.conversionErr <- err })
		h.writeRPC(RPCErrorEvent{Type: "error", Message: "event conversion error: " + err.Error()})
		h.cancel()
	})
	gateAfter := session.Subscribe(func(event agent.AgentEvent) { h.settle.after(event); h.loop.after(event) })
	backpressure := subscribeStdoutBackpressure(session.Agent(), func() { h.stdoutWait(h.ctx) })
	h.mu.Lock()
	h.unsubscribeEvents = append(h.unsubscribeEvents, gateBefore, unsubscribe, gateAfter, backpressure)
	h.mu.Unlock()
	h.forwarding.Add(1)
	h.forwarders.Go(func() {
		defer h.forwarding.Add(-1)
		for ev := range session.Events() {
			if coding.AcknowledgeEvent(ev) || h.detached.Load() {
				continue
			}
			if _, settled := ev.(agent.AgentSettledEvent); settled && h.afterSettled != nil {
				// The forwarder must keep acknowledging the shutdown flush barrier.
				h.afterSettled()
			}
		}
	})
	return writing
}

// closeHost releases the mode's per-Session subscriptions after the Runtime closed.
func (h *rpcHost) closeHost() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.detachModelRegistry != nil {
		h.detachModelRegistry()
		h.detachModelRegistry = nil
	}
	if h.unsubscribeName != nil {
		h.unsubscribeName()
		h.unsubscribeName = nil
	}
	for _, unsubscribe := range h.unsubscribeEvents {
		unsubscribe()
	}
	h.unsubscribeEvents = nil
}

// bindExtensionActions binds session to the in-process runner and the subprocess extension bridge as rpc-mode.ts rebindSession's session.bindExtensions does (rpc-mode.ts:317-351 over agent-session.ts:3275-3408 _bindExtensionCore), and returns the model-registry detach, or nil without a bridge.
func (h *rpcHost) bindExtensionActions(session *coding.Session, st *rpcModeSession, runner *inproc.Runner, current func() *coding.Session) func() {
	services := session.Services()
	flagValues := services.ExtensionFlagValues()
	_, bridge := rpcExtensionHost(session)
	sessionDir := session.Inner().GetSessionDir()
	// Session-backed actions (sendUserMessage, isIdle, abort,
	// hasPendingMessages, waitForIdle) for in-process and subprocess
	// extensions, as upstream rpc-mode binds the session in bindExtensions.
	if runner != nil || bridge != nil {
		coding.BindSessionExtensionActions(runner, bridge, current,
			extension.ContextActions{
				GetAllTools: func() []extension.ToolInfo {
					return current().GetAllTools()
				},
				GetActiveTools: func() []string {
					agentTools := current().State().Tools()
					names := make([]string, len(agentTools))
					for i, t := range agentTools {
						names[i] = t.Name()
					}
					return names
				},
				SetActiveTools: func(names []string) {
					current().SetActiveToolsByName(names)
				},
				GetFlagValue: func(name string) any {
					return flagValues[name]
				},
				IsProjectTrusted: func() bool {
					// upstream: agent-session.ts:3440 isProjectTrusted reads the Session's settings manager.
					return current().Services().SettingsManager().IsProjectTrusted()
				},
				ModelRegistry: services.Registry(),
				Shutdown:      h.requestShutdown,
			}, h.extensionRunStage, h.rt.ExtensionCommandActions)
		if bridge != nil {
			bridge.SetHostAction("shutdown", h.requestShutdown)
		}
	}
	var detach func()
	if bridge != nil {
		detachModelRegistry, stopRunSignal, stopHostState := coding.BindSessionModelOperations(bridge, session, services), coding.WatchSessionRunSignal(bridge, session), coding.WatchHostState(bridge, session)
		detach = func() {
			stopHostState()
			stopRunSignal()
			detachModelRegistry()
		}
		registryAllowed, registryExcluded := toolRegistryFilters(h.flags)
		bridge.SetHostAction("getAllTools", func() []subprocess.ToolInfo {
			return codingagent.ExtensionToolInfos(runner, registryAllowed, registryExcluded)
		})
		bridge.SetHostAction("getCommands", func() []subprocess.CommandInfo {
			return st.published.Load().slashCatalog().SubprocessCommands()
		})
		// A state push encodes the catalog only when the published catalog or the runner's commands changed.
		var commandsVersion codingagent.CommandCatalogVersion
		bridge.SetHostAction("getCommandsVersion", func() uint64 {
			catalog := st.published.Load()
			return commandsVersion.Version(catalog, catalog.slashCatalog().Runner)
		})
		coding.BindSessionReadActions(bridge, current, services.CWD(), sessionDir)
		// getActiveTools and setActiveTools come from coding.BindSessionExtensionActions
		// (upstream getActiveToolNames and setActiveToolsByName).
		codingagent.BindAppendEntryAction(bridge, func() (*codingagent.Session, error) { return current().Inner(), nil },
			func(entry codingagent.CustomEntry, direct *subprocess.DirectEntryAppend) error {
				if direct != nil && !direct.Event {
					// ctx.sessionManager.appendCustomEntry writes the log only;
					// upstream emits entry_appended for pi.appendEntry alone.
					return nil
				}
				if h.detached.Load() || !st.writing.Load() {
					return nil
				}
				h.writeRPC(RPCEntryAppendedEvent{
					Type: "entry_appended",
					Entry: RPCEntryAppendedEntry{
						Type: entry.Type, CustomType: entry.CustomType, Data: entry.Data,
						ID: entry.ID, ParentID: entry.ParentID, Timestamp: entry.Timestamp,
					},
				})
				return nil
			})
	}

	return detach
}
