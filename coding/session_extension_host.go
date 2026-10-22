package coding

// Ports packages/coding-agent/src/core/agent-session.ts _bindExtensionCore (:3275-3408): what a Session binds for its extensions in every mode. The CLI modes and a Session created by createAgentSession share these bindings.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
	"github.com/MichaelKinsy/PiG/tui"
)

// ExtensionHostSessionActions is the Session-owned messages, names, and context actions in every mode. The current getter supplies the live Session, or nil while it is being created. stage, when non-nil, returns the stage of a run an extension sends at that moment, or nil to start it as the Session does.
func ExtensionHostSessionActions(current func() *Session, stage func() *ExtensionRunStage) (extension.ExtensionActions, extension.ContextActions) {
	actions := extension.ExtensionActions{
		SetSessionName: func(name string) error {
			if sess := current(); sess != nil {
				return sess.SetSessionName(name)
			}
			return ErrSessionNotReady
		},
		GetSessionName: func() string {
			if sess := current(); sess != nil {
				return sess.SessionName()
			}
			return ""
		},
		SendMessage: func(message extension.CustomMessageRef, opts *extension.SendMessageOptions) error {
			return sendSessionCustomMessage(current(), message, opts, stage)
		},
		SendUserMessage: func(content any, opts *extension.SendUserMessageOptions) error {
			return sendSessionUserMessage(current(), content, opts, stage)
		},
	}
	// upstream: agent-session.ts:3339 binds getSettings to the session's settings manager in every mode.
	actions.GetSettings = func() extension.Settings {
		if sess := current(); sess != nil {
			return sess.Services().SettingsManager().ExtensionSettings()
		}
		return nil
	}
	// upstream: agent-session.ts:3361-3384 binds getCommands to the session in every mode.
	actions.GetCommands = func() []extension.SlashCommandInfo {
		if sess := current(); sess != nil {
			return sess.SlashCommands()
		}
		return []extension.SlashCommandInfo{}
	}
	// upstream: agent-session.ts:3343-3349, 3355 bind setModel, setThinkingLevel and setLabel to the Session in every mode; a subprocess extension reaches them through the bridge host actions below.
	actions.SetModel = func(ctx context.Context, model extension.Model) (bool, error) {
		sess := current()
		if sess == nil {
			return false, ErrSessionNotReady
		}
		if model == nil {
			return false, fmt.Errorf("setModel requires a model, got %T", nil)
		}
		return sess.ExtensionSetModel(ctx, model)
	}
	actions.SetThinkingLevel = func(level extension.ThinkingLevel) {
		sess := current()
		if sess == nil {
			return
		}
		// The call is fire-and-forget on the wire, so a failure to persist the change reaches the error listeners, as Pi reports its unawaited Session actions (agent-session.ts:3303-3310).
		if err := sess.SetThinkingLevel(ai.ModelThinkingLevel(level)); err != nil {
			if runner := sess.ExtensionRunner(); runner != nil {
				runner.EmitError(&extension.ExtensionError{ExtensionPath: "<runtime>", Event: "set_thinking_level", Error: err.Error()})
			}
		}
	}
	contextActions := extension.ContextActions{
		GetScopedModels: func() []extension.ScopedModel {
			if sess := current(); sess != nil {
				return sess.ScopedModels()
			}
			return []extension.ScopedModel{}
		},
		GetSystemPrompt: func() string {
			if sess := current(); sess != nil {
				return sess.SystemPrompt()
			}
			return ""
		},
		GetContextUsage: func() *extension.ContextUsage {
			sess := current()
			if sess == nil {
				return nil
			}
			usage := sess.ContextUsage()
			if usage == nil {
				return nil
			}
			return &extension.ContextUsage{Tokens: usage.Tokens, ContextWindow: usage.ContextWindow, Percent: usage.Percent}
		},
		GetSystemPromptOptions: func() *extension.BuildSystemPromptOptions {
			if sess := current(); sess != nil {
				return sess.GetSystemPromptOptions()
			}
			return new(extension.NormalizeBuildSystemPromptOptions(extension.BuildSystemPromptOptions{}))
		},
		IsIdle: func() bool {
			sess := current()
			return sess == nil || sess.IsIdle()
		},
		// upstream: agent-session.ts:3368 binds getSignal to `this.agent.signal` in every mode; with no Session there is no run.
		GetSignal: func() context.Context {
			if sess := current(); sess != nil {
				return sess.Signal()
			}
			return nil
		},
		Abort: func() {
			if sess := current(); sess != nil {
				sess.RequestAbort()
			}
		},
		HasPendingMessages: func() bool {
			sess := current()
			return sess != nil && sess.HasPendingMessages()
		},
	}
	return actions, contextActions
}

func sendSessionCustomMessage(sess *Session, message extension.CustomMessageRef, options *extension.SendMessageOptions, stage func() *ExtensionRunStage) error {
	if sess == nil {
		return ErrSessionNotReady
	}
	if stage == nil {
		return sess.SendMessage(message, options)
	}
	return sess.SendMessageStaged(message, options, stage())
}

func sendSessionUserMessage(sess *Session, content any, options *extension.SendUserMessageOptions, stage func() *ExtensionRunStage) error {
	if sess == nil {
		return ErrSessionNotReady
	}
	if stage == nil {
		return sess.SendExtensionUserMessage(content, options)
	}
	return sess.SendExtensionUserMessageStaged(content, options, stage())
}

// BindSessionExtensionActions binds the session-backed actions for in-process
// extensions (runner) and subprocess extensions (bridge host actions).
// contextActions carries the mode's other context actions, including the extension flag values supplied before session_start.
//
// stage, when non-nil, places the runs that sendUserMessage and sendMessage start (ExtensionHostSessionActions); in-process and subprocess extensions share it.
//
// replacement optionally supplies the command actions of a Session, so a mode that owns a Runtime routes newSession, fork and switchSession through it. Without it the Session's own actions apply.
func BindSessionExtensionActions(runner *inproc.Runner, bridge *subprocess.UIBridge, current func() *Session, contextActions extension.ContextActions, stage func() *ExtensionRunStage, replacement ...func(*Session) extension.CommandActions) {
	commandActions := func(sess *Session) extension.CommandActions {
		if len(replacement) > 0 && replacement[0] != nil {
			return replacement[0](sess)
		}
		return sess.ExtensionCommandActions()
	}
	actions, sessionContext := ExtensionHostSessionActions(current, stage)
	if runner != nil {
		// Upstream _bindExtensionCore binds getAllTools, getActiveTools and
		// setActiveTools to the session in every mode. Modes that supply their
		// own (RPC, interactive) keep them; print mode relies on these defaults.
		if contextActions.GetAllTools == nil {
			contextActions.GetAllTools = func() []extension.ToolInfo {
				if sess := current(); sess != nil {
					return sess.GetAllTools()
				}
				return nil
			}
		}
		if contextActions.GetActiveTools == nil {
			contextActions.GetActiveTools = func() []string {
				if sess := current(); sess != nil {
					return sess.ActiveToolNames()
				}
				return []string{}
			}
		}
		if contextActions.SetActiveTools == nil {
			contextActions.SetActiveTools = func(names []string) {
				if sess := current(); sess != nil {
					sess.SetActiveToolsByName(names)
				}
			}
		}
		// upstream: agent-session.ts:3353 binds refreshTools to the session in every mode, so a tool an extension registers
		// after load is admitted and declared.
		if contextActions.RefreshTools == nil {
			contextActions.RefreshTools = func() error {
				if sess := current(); sess != nil {
					return sess.RefreshTools()
				}
				return ErrSessionNotReady
			}
		}
		// upstream: agent-session.ts:3361-3390 binds getCommands to the runner's commands plus the Session's prompt templates and skills in every mode.
		actions.GetCommands = func() []extension.SlashCommandInfo {
			if sess := current(); sess != nil {
				return sess.SlashCommandsFor(runner)
			}
			return []extension.SlashCommandInfo{}
		}
		contextActions.GetSystemPrompt = sessionContext.GetSystemPrompt
		contextActions.GetSystemPromptOptions = sessionContext.GetSystemPromptOptions
		contextActions.IsIdle = sessionContext.IsIdle
		contextActions.GetSignal = sessionContext.GetSignal
		contextActions.Abort = sessionContext.Abort
		contextActions.HasPendingMessages = sessionContext.HasPendingMessages
		contextActions.GetScopedModels = sessionContext.GetScopedModels
		contextActions.GetContextUsage = sessionContext.GetContextUsage
		contextActions.GetSystemPromptOptions = sessionContext.GetSystemPromptOptions
		runner.BindCore(actions, contextActions, nil)
	}
	if bridge == nil {
		return
	}
	bridge.SetHostAction("setSessionName", actions.SetSessionName)
	// upstream: agent-session.ts:3339, 3382-3383 bind getSettings, executeTool and getCallableTools to the session in every mode. A subprocess extension reaches them through these host actions.
	bridge.SetHostAction("getSettings", actions.GetSettings)
	bridge.SetHostAction("getSettingsJSON", sessionSettingsJSON(current))
	bridge.SetHostAction("getCallableTools", func() []extension.AgentTool {
		if sess := current(); sess != nil {
			return sess.ToolActions().GetCallableTools()
		}
		return nil
	})
	bridge.SetHostAction("executeTool", func(ctx context.Context, callerID, name string, args json.RawMessage, options extension.ExecuteToolOptions) (extension.AgentToolCallOutcome, error) {
		if sess := current(); sess != nil {
			return sess.ToolActions().ExecuteTool(ctx, callerID, name, args, options)
		}
		return subprocess.UnavailableNestedCall(callerID, name), nil
	})
	bridge.SetHostAction("getSessionName", actions.GetSessionName)
	BindExtensionHostSessionActions(bridge, current)
	if stage != nil {
		// The staged sendMessage replaces the Session's own binding for subprocess extensions as the runner's does for in-process ones.
		bridge.SetHostAction("sendMessage", func(message extension.CustomMessageRef, opts subprocess.SendMessageOptions) error {
			return actions.SendMessage(message, &extension.SendMessageOptions{TriggerTurn: opts.TriggerTurn, DeliverAs: extension.DeliverAs(opts.DeliverAs)})
		})
	}
	bridge.SetHostAction("sendUserMessage", func(content any, opts subprocess.SendUserMessageOptions) error {
		return actions.SendUserMessage(content, &extension.SendUserMessageOptions{DeliverAs: extension.DeliverAs(opts.DeliverAs)})
	})
	bridge.SetHostAction("refreshTools", func() error {
		if sess := current(); sess != nil {
			return sess.RefreshTools()
		}
		return ErrSessionNotReady
	})
	bridge.SetHostAction("getFlag", func(_ string, name string) any {
		if contextActions.GetFlagValue != nil {
			return contextActions.GetFlagValue(name)
		}
		return nil
	})
	bridge.SetHostAction("getScopedModels", sessionContext.GetScopedModels)
	// Headless Pi binds the Session's own thinking level, context usage and base prompt options (agent-session.ts:3125); the host's unbound defaults would be fabricated values.
	bridge.SetHostAction("getThinkingLevel", func() string {
		if sess := current(); sess != nil {
			return string(sess.ThinkingLevel())
		}
		return ""
	})
	bridge.SetHostAction("getContextUsage", sessionContext.GetContextUsage)
	bridge.SetHostAction("getSignal", sessionContext.GetSignal)

	bridge.SetHostAction("getSystemPrompt", sessionContext.GetSystemPrompt)
	bridge.SetHostAction("getSystemPromptOptions", func() extension.BuildSystemPromptOptions {
		return *sessionContext.GetSystemPromptOptions()
	})
	// Pi binds setModel, setThinkingLevel, isProjectTrusted and compact to the Session in every mode (agent-session.ts:3343-3349, 3355, 3369-3379 _bindExtensionCore).
	bridge.SetHostAction("setModel", func(ctx context.Context, spec string) (bool, error) {
		extension.CallInitiated(ctx)
		sess := current()
		if sess == nil {
			return false, ErrSessionNotReady
		}
		model, err := BuildModel(spec, sess.Services())
		if err != nil {
			return false, err
		}
		return actions.SetModel(ctx, model)
	})
	bridge.SetHostAction("setThinkingLevel", func(level string) { actions.SetThinkingLevel(extension.ThinkingLevel(level)) })
	bridge.SetHostAction("isProjectTrusted", func() bool {
		if contextActions.IsProjectTrusted != nil {
			return contextActions.IsProjectTrusted()
		}
		// Pi answers from the Session's settings manager (agent-session.ts:3355); with no Session there is nothing that established trust, so the project is not trusted.
		sess := current()
		return sess != nil && sess.Services().SettingsManager().IsProjectTrusted()
	})
	bridge.SetHostAction("compact", func(ctx context.Context, opts *extension.CompactOptions) {
		extension.CallInitiated(ctx)
		if sess := current(); sess != nil {
			sess.ExtensionCompact(opts)
		} else if opts != nil && opts.OnError != nil {
			opts.OnError(ErrSessionNotReady)
		}
	})
	bridge.SetHostAction("isIdle", sessionContext.IsIdle)
	bridge.SetHostAction("abort", sessionContext.Abort)
	bridge.SetHostAction("hasPendingMessages", sessionContext.HasPendingMessages)
	// exec is mode-independent in upstream: loader.ts's createExtensionContext
	// binds ExtensionContext.exec the same way for every mode (print, JSON,
	// RPC, interactive). Bind it once here so print, JSON, and RPC mode share
	// the same implementation instead of each mode wiring its own; interactive
	// mode binds the identical extension.ExecCommand call directly against its
	// session in wireSubprocessHostCallbacks.
	bridge.SetHostAction("exec", func(ctx context.Context, command string, args []string, opts *extension.ExecOptions) (extension.ExecResult, error) {
		cwd := ""
		if sess := current(); sess != nil {
			cwd = sess.CWD()
		}
		return extension.ExecCommand(ctx, cwd, command, args, opts)
	})
	bridge.BindCommandActions(extension.CommandActions{
		NewSessionContext: func(ctx context.Context, opts *extension.NewSessionOptions) (extension.CancelledResult, error) {
			if sess := current(); sess != nil {
				return commandActions(sess).NewSessionContext(ctx, opts)
			}
			return extension.CancelledResult{}, ErrSessionNotReady
		},
		ForkContext: func(ctx context.Context, entryID string, opts *extension.ForkOptions) (extension.CancelledResult, error) {
			if sess := current(); sess != nil {
				return commandActions(sess).ForkContext(ctx, entryID, opts)
			}
			return extension.CancelledResult{}, ErrSessionNotReady
		},
		SwitchSessionContext: func(ctx context.Context, path string, opts *extension.SwitchSessionOptions) (extension.CancelledResult, error) {
			if sess := current(); sess != nil {
				return commandActions(sess).SwitchSessionContext(ctx, path, opts)
			}
			return extension.CancelledResult{}, ErrSessionNotReady
		},
		WaitForIdle: func() error {
			if sess := current(); sess != nil {
				if waitForIdleSettlesAtOnce(sess, stage) {
					return nil
				}
				return sess.ExtensionCommandActions().WaitForIdle()
			}
			return nil
		},
		WaitForIdleBlocks: func() bool {
			sess := current()
			return sess != nil && !waitForIdleSettlesAtOnce(sess, stage)
		},
		NavigateTree: func(targetID string, opts *extension.NavigateTreeOptions) (extension.CancelledResult, error) {
			if sess := current(); sess != nil {
				return sess.ExtensionCommandActions().NavigateTree(targetID, opts)
			}
			return extension.CancelledResult{}, ErrSessionNotReady
		},
		// print-mode.ts:97-99 and rpc-mode.ts:341-343 bind reload to session.reload() in every headless mode.
		ReloadContext: func(ctx context.Context) error {
			sess := current()
			if sess == nil {
				extension.CallInitiated(ctx)
				return ErrSessionNotReady
			}
			reload := commandActions(sess).ReloadContext
			if reload == nil {
				extension.CallInitiated(ctx)
				return errors.New("reload is not available without a mode runtime")
			}
			return reload(ctx)
		},
	})
}

// sessionSettingsJSON is the getSettings action as JSON in Pi's member order: the live Session's settings encoding, which its settings manager
// makes once per settings change, or null while no Session exists.
func sessionSettingsJSON(current func() *Session) func() json.RawMessage {
	return func() json.RawMessage {
		if sess := current(); sess != nil {
			return sess.Services().SettingsManager().ExtensionSettingsJSON()
		}
		return json.RawMessage("null")
	}
}

// BindSessionReadActions binds the session manager reads subprocess
// extensions make, for print, JSON and RPC mode. Upstream hands every mode's
// extensions the session's SessionManager (runner.ts createContext
// sessionManager). cwd and sessionDir are the session manager's own.
func BindSessionReadActions(bridge *subprocess.UIBridge, current func() *Session, cwd, sessionDir string) {
	if bridge == nil {
		return
	}
	bridge.SetHostAction("getSessionName", func() string {
		if sess := current(); sess != nil {
			return sess.SessionName()
		}
		return ""
	})
	bridge.SetHostAction("getSessionID", func() string {
		if sess := current(); sess != nil {
			return sess.ID()
		}
		return ""
	})
	bridge.SetHostAction("getSessionFile", func() string {
		if sess := current(); sess != nil {
			return sess.Path()
		}
		return ""
	})
	bridge.SetHostAction("getLeafID", func() string {
		if sess := current(); sess != nil {
			if leaf := sess.LeafID(); leaf != nil {
				return sessionentry.WireID(*leaf)
			}
		}
		return ""
	})
	bridge.SetHostAction("getEntriesPage", func(cursor, maxBytes int) ([]json.RawMessage, int, bool, string) {
		sess := current()
		if sess == nil {
			return nil, 0, false, ""
		}
		page, next, total := sess.Inner().RawEntriesPage(cursor, maxBytes)
		if page == nil {
			page = make([]json.RawMessage, 0)
		}
		leafID := ""
		if leaf := sess.LeafID(); leaf != nil {
			leafID = *leaf
		}
		return page, next, next < total, leafID
	})
	// pi.setLabel is the handler the Session binds into its runner's runtime (agent-session.ts:3419 _bindExtensionCore); a subprocess extension
	// reaches that one handler. An empty label clears it (types.ts:2106 SetLabelHandler takes `string | undefined`).
	bridge.SetHostAction("setLabel", func(entryID, label string) error {
		sess := current()
		if sess == nil {
			return ErrSessionNotReady
		}
		runner := sess.ExtensionRunner()
		if runner == nil || runner.Runtime().SetLabel == nil {
			return ErrSessionNotReady
		}
		var value *string
		if label != "" {
			value = &label
		}
		return runner.Runtime().SetLabel(entryID, value)
	})
	var info icodingagent.ExtensionSessionInfoMemo
	bridge.SetHostAction("sessionRead", func(method string, args json.RawMessage) (any, error) {
		view := icodingagent.ExtensionSessionView{CWD: cwd, SessionDir: icodingagent.ExtensionSessionDir(sessionDir)}
		if sess := current(); sess != nil {
			view.Session = sess.Inner()
		}
		if method == "info" {
			// Every state push reads the info; it is rebuilt only when a value it is built from changed.
			return info.Encoded(view)
		}
		return icodingagent.ExtensionSessionRead(view, method, args)
	})
}

// BindSessionModelOperations binds the model operations of session (its model, the model runtime's catalog, setModel and the image and classify calls) to the subprocess extensions of bridge. The returned function unbinds them.
func BindSessionModelOperations(bridge *subprocess.UIBridge, session *Session, services *AgentSessionServices) func() {
	if bridge == nil || session == nil || services == nil {
		return func() {}
	}
	runtime := session.ModelRuntime()
	return icodingagent.WireModelOperations(bridge, icodingagent.ModelOperationBindings{
		CurrentModel: session.Model, ModelLookup: runtime.GetModel, ModelCatalog: runtime.GetModels, Classify: runtime.Classify, GenerateImages: runtime.GenerateImages,
		Registry: services.Registry().ModelRegistry, ModelBuilder: func(spec string) (*ai.Model, error) { return BuildModel(spec, services) },
		SessionHandle: session,
	})
}

// WatchSessionRunSignal reports each run the Session's agent begins or ends to the extension host, and returns the function that stops. Pi's ctx.signal is `() => this.agent.signal` (agent-session.ts:3368), read live by the extension that holds ctx, so every runtime must learn of a run change when it happens, not at its next request.
func WatchSessionRunSignal(bridge *subprocess.UIBridge, session *Session) func() {
	if bridge == nil || session == nil {
		return func() {}
	}
	return session.Agent().ObserveRunSignal(bridge.RunSignalChanged)
}

// WatchHostState reports each change of the host state the extension runtimes replicate and no extension call announces (the Session's model, thinking level and session name, the settings, and the active theme) to the extension host, and returns the function that stops. Pi's `ctx.model`, `pi.getSettings()`, `pi.getSessionName()` and `ctx.ui.theme` read the live Session, settings and global theme (runner.ts:906, loader.ts:411-414, interactive-mode.ts:2665), so a runtime that reads between two requests must learn of a change when it happens, not at its next request.
func WatchHostState(bridge *subprocess.UIBridge, session *Session) func() {
	if bridge == nil || session == nil {
		return func() {}
	}
	stopSession := session.ObserveReplicatedState(bridge.HostStateChanged)
	stopTheme := tui.ObserveActiveTheme(bridge.HostStateChanged)
	return func() {
		stopSession()
		stopTheme()
	}
}

// waitForIdleSettlesAtOnce reports whether `await ctx.waitForIdle()` returns without waiting. Pi's returns at once while the Session is idle (agent-session.ts waitForIdle: `if (this.isIdle) return`), and that includes the run a command's own sendUserMessage requested: its input handlers, compaction check and before_agent_start run after microtasks (agent-session.ts prompt, _startRun), later than the handler's next statement. Here that run is prepared ahead of the handler's continuation and its agent_start is held until the command's handlers stop running (rpcAdmission.extensionRunStage), so the Session counts it busy; a run a sendUserMessage requested that has not emitted agent_start does not count. A triggering sendMessage does: Pi's sendCustomMessage awaits _startRun, which sets the run active before the send returns, so the command's waitForIdle waits for the run.
func waitForIdleSettlesAtOnce(sess *Session, stage func() *ExtensionRunStage) bool {
	return sess.IsIdle() || (stage != nil && stage() != nil && sess.ExtensionUserMessageRunPending())
}
