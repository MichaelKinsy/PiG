package main

// Ports packages/coding-agent/src/modes/rpc/rpc-mode.ts rebindSession and the runtime host wiring.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// rpcSessionState is the RPC-mode state of one Session.
type rpcSessionState struct {
	Build           *cliBuild
	PromptTemplates []codingagent.PromptTemplate
	Skills          []*codingagent.SkillDef
	SourceInfo      map[string]codingagent.ResourceSourceInfo
	// AgentToolNames and PromptOptions are set when the Session's extension runner exists, because the prompt lists extension tools in registration order.
	AgentToolNames []string
	PromptOptions  prompts.Options
	Catalog        headlessCommandCatalog
	published      atomic.Pointer[headlessCommandCatalog]
	// join counts the extension commands of this Session's extension host whose responses are not written.
	join atomic.Pointer[rpcCommandJoin]
	// StartupName is the --name of the process's first Session. Replacements carry none.
	StartupName string
	// Refresh replaces the resources above with the build's reloaded ones and derives the system prompt for runner's tools. Reload is AgentSession.reload for this build's Session; without it ctx.reload() fails.
	Refresh func(runner *inproc.Runner) coding.SessionStartOptions
	Reload  headlessReload
}

// rpcStartup carries the startup-only inputs of the first RPC Session.
type rpcStartup struct {
	Manager     *coding.SessionManager
	ResumePath  string
	SessionName string
	SessionDir  string
}

// rpcInputs builds RPC mode's Session inputs from a build. Replacement Sessions rebuild through the same builder for their destination cwd.
func (b *cliRuntimeBuilder) rpcInputs(build *cliBuild, startup *rpcStartup) (cliSessionInputs, *rpcSessionState) {
	flags, settings := build.Flags, build.Settings
	resourceInfo := build.RPCSourceInfo
	if resourceInfo == nil {
		resourceInfo = resourceSourceInfoProvider(build.CWD, b.agentDir, build.Services.SettingsManager(), build.ResourceFlags, build.SourceResolver.Resolve)()
	}
	state := &rpcSessionState{
		Build:           build,
		PromptTemplates: codingagent.LoadPromptTemplates("", "", build.PromptPaths...).Templates,
		SourceInfo:      resourceInfo,
	}
	state.Skills = build.SkillCatalog.WithSkillSources(rpcResolvedSkills(build.skills(), b.activePiglet))
	if startup != nil {
		state.StartupName = startup.SessionName
	}
	state.Refresh = func(runner *inproc.Runner) coding.SessionStartOptions {
		if build.RPCSourceInfo != nil {
			state.SourceInfo = build.RPCSourceInfo
		}
		state.PromptTemplates = codingagent.LoadPromptTemplates("", "", build.PromptPaths...).Templates
		state.Skills = build.SkillCatalog.WithSkillSources(rpcResolvedSkills(build.skills(), b.activePiglet))
		var start coding.SessionStartOptions
		b.rpcPrepare(build, state, runner, &start)
		return start
	}
	state.Reload = func(call, owner context.Context, session *coding.Session, rebind headlessRebind) error {
		return b.reloadHeadless(call, owner, build, session, rebind)
	}
	scopePatterns := flags.Models
	if len(scopePatterns) == 0 {
		scopePatterns = settings.EnabledModels
	}
	start := coding.SessionStartOptions{
		ScopedModels:  extensionScopedModels(build.Services, scopePatterns),
		Model:         build.Model,
		ThinkingLevel: ai.ThinkingLevel(flags.Thinking),
		// The tool selection is the build's, the one print, JSON and interactive modes start their Sessions with (main.ts:822-830 passes the same tools, excludeTools and noTools to every mode).
		AllowedTools:       build.Allowed,
		ActiveBuiltinTools: build.ActiveBuiltin,
		ExcludedTools:      build.ExcludedTools,
		SkipBuiltinTools:   build.SkipBuiltinTools,
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
		Services: build.Services, Extensions: build.Extensions, Start: start, Host: build.Host,
		Prepare: func(runner *inproc.Runner, start *coding.SessionStartOptions) {
			b.rpcPrepare(build, state, runner, start)
		},
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
	}, state
}

// rpcPrepare lists the startup agent loadout in the prompt: the build's builtin selection, which already applies --tools, --no-tools, --no-builtin-tools, --exclude-tools and the defaultTools setting, then the extension tools that selection allows. The prompt lists extension tools in the runner's first-wins registration order, as AgentSession._refreshToolRegistry does.
func (b *cliRuntimeBuilder) rpcPrepare(build *cliBuild, state *rpcSessionState, runner *inproc.Runner, start *coding.SessionStartOptions) {
	agentToolNames := slices.Clone(build.AgentToolNames)
	if runner != nil {
		for _, tool := range runner.Tools() {
			name := tool.Definition.Name
			if _, ok := build.ExcludedTools[name]; ok {
				continue
			}
			if _, ok := build.Allowed[name]; build.Allowed != nil && !ok {
				continue
			}
			// upstream: agent-session.ts:3487-3506: a tool that does not start active is registered but not listed or declared.
			if coding.ExtensionToolStartsActive(tool.Definition, build.Allowed) {
				agentToolNames = append(agentToolNames, name)
			}
		}
	}
	promptOptions := prompts.Options{
		Cwd:                build.CWD,
		Tools:              agentToolNames,
		ToolHints:          prompts.DefaultToolSnippets(),
		ToolGuidelines:     tools.DefaultToolGuidelines(),
		Skills:             promptSkillsFor(state.Skills),
		PigDocsPath:        filepath.Join(codingagent.ConfigRoot(), "docs"),
		ContextFiles:       toPromptContextFiles(build.ContextFiles),
		CustomPrompt:       build.ResolvedPrompts.custom,
		AppendSystemPrompt: build.ResolvedPrompts.append,
	}
	if build.ResolvedPrompts.custom != "" {
		promptOptions.AppendMode = "replace"
	}
	start.SystemPromptSections = prompts.BuildSystemPromptSections(promptOptions)
	start.SystemPromptResources = sessionPromptResources(build.ResolvedPrompts, build.ContextFiles, state.Skills)
	start.ResourceLoader = coding.NoResources // the RPC command catalog expands skills and prompts itself
	start.SystemPrompt = prompts.BuildDefaultPrompt(promptOptions)
	state.AgentToolNames = agentToolNames
	state.PromptOptions = promptOptions
}

// rpcHost is RPC mode's binding of a Runtime to the protocol streams. It owns what Pi's rebindSession re-creates for every replacement Session: extension bindings, the event subscription and the extension UI wiring.
type rpcHost struct {
	ctx    context.Context
	cancel context.CancelFunc
	flags  CLIFlags
	piglet *piglet.Piglet

	ui        *rpcUIContext
	writeRPC  func(any)
	responses *rpcResponseTurn

	factory *cliSessionFactory[*rpcSessionState]
	rt      *coding.Runtime

	promptWG *sync.WaitGroup
	tasks    *rpcTaskGroup

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
	detachModelRegistry func()
	unsubscribeName     func()
	// commandJoin joins the current Session's in-flight extension commands for the shutdown flush; nil when its runtime cannot report a suspended command.
	commandJoin atomic.Pointer[rpcCommandJoin]
	// unsubscribeEvents detaches every Session's event serializer and stdout-backpressure listener.
	unsubscribeEvents []func()
}

func (h *rpcHost) current() *coding.Session { return h.rt.Session() }

func (h *rpcHost) state(session *coding.Session) (*rpcSessionState, bool) {
	return h.factory.StateFor(session)
}

// bind wires session to RPC mode as rpc-mode.ts rebindSession does and emits session_start for event: the Session's own reason, which is startup for the first Session and the replacement reason otherwise. A reload binds the same Session to a replacement runner: the resources and the prompt are the reloaded ones, and the Session's subscriptions and startup state stay (agent-session.ts:3575-3625).
func (h *rpcHost) bind(ctx context.Context, session *coding.Session, event extension.SessionStartEvent, reloading bool) error {
	st, ok := h.state(session)
	if !ok {
		return errors.New("rpc: replacement Session has no mode state")
	}
	build := st.Build
	services := build.Services
	runner := session.ExtensionRunner()
	bridge := build.Bridge
	current := h.current
	var reloaded coding.SessionStartOptions
	if reloading {
		reloaded = st.Refresh(runner)
	}

	if bridge != nil {
		bridge.SetUIContext(h.ui)
		bridge.SetWidgetRequestFunc(func(_ string, key string, lines []string, opts extension.ExtensionWidgetOptions) {
			h.ui.SetWidget(key, lines, opts)
		})
		var widgetMu sync.Mutex
		previousWidgets := make(map[string][]string)
		bridge.SetWidgetSyncFunc(func(widgets map[string]*subprocess.PushProxy) {
			widgetMu.Lock()
			defer widgetMu.Unlock()
			keys := make([]string, 0, len(widgets))
			for key := range widgets {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			for _, qualified := range keys {
				lines := widgets[qualified].Lines()
				previous, exists := previousWidgets[qualified]
				if exists && slices.Equal(previous, lines) {
					continue
				}
				key := qualified
				if _, suffix, ok := strings.Cut(qualified, ":"); ok {
					key = suffix
				}
				h.ui.SetWidget(key, lines, nil)
				previousWidgets[qualified] = append([]string(nil), lines...)
			}
			for qualified := range previousWidgets {
				if _, exists := widgets[qualified]; exists {
					continue
				}
				key := qualified
				if _, suffix, ok := strings.Cut(qualified, ":"); ok {
					key = suffix
				}
				h.ui.SetWidget(key, nil, nil)
				delete(previousWidgets, qualified)
			}
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
		runner: runner, mode: "rpc", promptTemplates: st.PromptTemplates, skills: st.Skills,
		cwd: build.CWD, agentDir: services.AgentDir(), sourceInfo: st.SourceInfo,
		llama: build.Llama, notify: h.ui.Notify,
	}
	// Extension host calls read the published copy of the catalog; announce alone changes it.
	st.Catalog = commandCatalog
	st.published.Store(new(commandCatalog))
	if bridge != nil && build.Host != nil && h.drain != nil {
		build.Host.SetRuntimeDrainHandler(func() {
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
		session.SetSystemPromptSections(reloaded.SystemPromptSections)
		session.SetSystemPromptResources(*reloaded.SystemPromptResources)
		detach := h.bindExtensionActions(session, st, runner, current)
		h.mu.Lock()
		if h.detachModelRegistry != nil {
			h.detachModelRegistry()
		}
		h.detachModelRegistry = detach
		h.mu.Unlock()
		return h.announce(ctx, session, st, event)
	}

	if st.StartupName != "" {
		if _, err := session.Inner().AppendSessionInfo(st.StartupName); err != nil {
			return fmt.Errorf("set session name: %w", err)
		}
	}
	rpcSetInitialActiveTools(session, st.AgentToolNames)

	if h.piglet != nil {
		allTools := session.GetAllTools()
		infos := make([]piglet.ToolInfo, len(allTools))
		owner := pigletToolOwner(func() []extension.Extension { return st.Build.Extensions })
		for i, t := range allTools {
			source := owner(t)
			if source == "" {
				source = "builtin"
			}
			infos[i] = piglet.ToolInfo{Name: t.Name, Source: source}
		}
		session.SetActiveToolsByName(piglet.ScopeTools(h.piglet, infos))
	}

	detach := h.bindExtensionActions(session, st, runner, current)

	// Publish name changes before the setter response, including extension startup calls.
	unsubscribeName := session.Subscribe(func(event agent.AgentEvent) {
		if name, ok := event.(agent.SessionInfoChangedEvent); ok && !h.detached.Load() {
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

	session.SetRetryContinuationScheduler(h.responses.after)
	// The forwarder subscribes before session_start so no event of the Session is missed.
	h.forward(session)
	return h.announce(ctx, session, st, event)
}

// announce drives the extension session lifecycle in RPC mode. Upstream fires session_start via session.bindExtensions (rpc-mode.ts:318) after wiring the mode's host-action bindings, then lets extensions add resources.
func (h *rpcHost) announce(ctx context.Context, session *coding.Session, st *rpcSessionState, event extension.SessionStartEvent) error {
	runner := session.ExtensionRunner()
	session.EmitSessionStartTransition(event.Reason, event.PreviousSessionFile)
	catalog := st.Catalog
	catalog.runner = runner
	skillsChanged, err := catalog.extendFromExtensions(ctx, runner, codingagent.ResourcesDiscoverReason(event.Reason))
	if err != nil {
		return err
	}
	if skillsChanged {
		st.PromptOptions.Skills = promptSkillsFor(catalog.skills)
		session.SetSystemPromptSections(prompts.BuildSystemPromptSections(st.PromptOptions))
		session.SetSystemPromptResources(*sessionPromptResources(st.Build.ResolvedPrompts, st.Build.ContextFiles, catalog.skills))
	}
	st.Catalog = catalog
	st.published.Store(&catalog)
	var join *rpcCommandJoin
	if st.Build.Host != nil {
		join = attachRPCCommandJoin(st.Build.Host)
		st.Build.Host.SetSettleTailHandler(h.settle.tailHandlerReporter())
	}
	st.join.Store(join)
	h.commandJoin.Store(join)
	h.admission.Store(h.newAdmission(session, runner, catalog, st.Build.Services, join))
	return nil
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

// rebindAgain repeats the rebind Pi's rpc-mode runs after a replacement command: rpc-mode.ts calls rebindSession() once more after runtimeHost.newSession, switchSession and fork return, so the replacement's extensions receive session_start and resources_discover a second time. Extension-initiated replacements rebind once.
func (h *rpcHost) rebindAgain(ctx context.Context) error {
	session := h.current()
	st, ok := h.state(session)
	if !ok {
		return errors.New("rpc: replacement Session has no mode state")
	}
	return h.announce(ctx, session, st, session.StartEvent())
}

func (h *rpcHost) newAdmission(session *coding.Session, runner *inproc.Runner, catalog headlessCommandCatalog, services *coding.Services, commands *rpcCommandJoin) *rpcAdmission {
	registry := services.Registry()
	return &rpcAdmission{
		ctx: h.ctx, turn: h.responses, session: session, runner: runner, catalog: catalog,
		write: h.responses.write, runs: h.promptWG, shared: h.tasks, commandDone: h.commandDone, commands: commands,
		validateModel: func() error {
			model := session.Model()
			if model == nil {
				return errors.New(codingagent.FormatNoAPIKeyFoundMessage("unknown"))
			}
			providerID := model.ProviderMeta.ProviderID
			if providerID == "" && model.Provider != nil {
				providerID = model.Provider.ID()
			}
			if providerID != "test-faux" && !registry.HasConfiguredAuth(providerID) {
				return errors.New(codingagent.FormatNoAPIKeyFoundMessage(providerID))
			}
			return nil
		},
	}
}

// forward writes the Session's events as upstream-compatible AgentSessionEvent JSON objects. Upstream pi RPC mode subscribes to session events and writes each event as a JSONL object after awaited extension handling and before Session persistence (rpc-mode.ts:346-363); rpcAgentEvent adapts pig's internal event structs to that wire shape. The Events consumer only drains and acknowledges delivery barriers.
func (h *rpcHost) forward(session *coding.Session) {
	// A replaced Session's unsettled tail is over: its run was joined before the replacement.
	h.settle.reset()
	// The gate sees an event ahead of its writer (agent_end) or after it (agent_settled), so a held command answers after the event that releases it.
	gateBefore := session.Subscribe(h.settle.before)
	// Upstream shutdown() unsubscribes the stdout forwarder, so nothing is written after detach.
	unsubscribe := subscribeRPCEvents(session, func(v any) {
		if !h.detached.Load() {
			h.writeRPC(v)
		}
	}, func(err error) {
		h.conversionOnce.Do(func() { h.conversionErr <- err })
		h.writeRPC(RPCErrorEvent{Type: "error", Message: "event conversion error: " + err.Error()})
		h.cancel()
	})
	gateAfter := session.Subscribe(h.settle.after)
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
func (h *rpcHost) bindExtensionActions(session *coding.Session, st *rpcSessionState, runner *inproc.Runner, current func() *coding.Session) func() {
	build := st.Build
	services := build.Services
	bridge := build.Bridge
	sessionDir := session.Inner().GetSessionDir()
	// Session-backed actions (sendUserMessage, isIdle, abort,
	// hasPendingMessages, waitForIdle) for in-process and subprocess
	// extensions, as upstream rpc-mode binds the session in bindExtensions.
	if runner != nil || bridge != nil {
		bindSessionExtensionActions(runner, bridge, current,
			extension.ContextActions{
				GetAllTools: func() []extension.ToolInfo {
					return current().GetAllTools()
				},
				GetActiveTools: func() []string {
					agentTools := current().Agent().Tools()
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
					return build.Flags.UnknownFlags[name]
				},
				IsProjectTrusted: func() bool {
					return build.ProjectTrusted
				},
				ModelRegistry: services.Registry(),
				Shutdown:      h.requestShutdown,
			}, h.rt.ExtensionCommandActions)
		if bridge != nil {
			bridge.SetHostAction("shutdown", h.requestShutdown)
		}
	}
	var detach func()
	if bridge != nil {
		detachModelRegistry, stopRunSignal := wireSubprocessModelRegistry(bridge, session, services), watchSessionRunSignal(bridge, session)
		detach = func() {
			stopRunSignal()
			detachModelRegistry()
		}
		registryAllowed, registryExcluded := toolRegistryFilters(build.Flags)
		bridge.SetHostAction("getAllTools", func() []subprocess.ToolInfo {
			return codingagent.ExtensionToolInfos(runner, registryAllowed, registryExcluded)
		})
		bridge.SetHostAction("getCommands", func() []subprocess.CommandInfo {
			return st.published.Load().slashCatalog().SubprocessCommands()
		})
		bindSessionReadActions(bridge, current, services.CWD(), sessionDir)
		// getActiveTools and setActiveTools come from bindSessionExtensionActions
		// (upstream getActiveToolNames and setActiveToolsByName).
		bridge.SetHostAction("appendEntry", func(customType string, data any, direct *subprocess.DirectEntryAppend) error {
			entry, err := codingagent.AppendExtensionEntry(current().Inner(), customType, data, direct)
			if err != nil {
				return err
			}
			if direct != nil {
				// ctx.sessionManager.appendCustomEntry writes the log only;
				// upstream emits entry_appended for pi.appendEntry alone.
				return nil
			}
			if h.detached.Load() {
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
