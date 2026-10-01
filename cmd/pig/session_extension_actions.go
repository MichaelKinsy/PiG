package main

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// sessionExtensionActions binds Session-owned messages, names, and context actions in every mode. The current getter supplies the live Session, or nil while it is being created.
func sessionExtensionActions(current func() *coding.Session) (extension.ExtensionActions, extension.ContextActions) {
	actions := extension.ExtensionActions{
		SetSessionName: func(name string) error {
			if sess := current(); sess != nil {
				return sess.SetSessionName(name)
			}
			return errSessionNotReady
		},
		GetSessionName: func() string {
			if sess := current(); sess != nil {
				return sess.SessionName()
			}
			return ""
		},
		SendMessage: func(message extension.CustomMessageRef, opts *extension.SendMessageOptions) error {
			return sendSessionCustomMessage(current(), message, opts)
		},
		SendUserMessage: func(content any, opts *extension.SendUserMessageOptions) error {
			return sendSessionUserMessage(current(), content, opts)
		},
	}
	// upstream: agent-session.ts:3339 binds getSettings to the session's settings manager in every mode.
	actions.GetSettings = func() extension.Settings {
		if sess := current(); sess != nil {
			return sess.Services().SettingsManager().ExtensionSettings()
		}
		return nil
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

func sendSessionCustomMessage(sess *coding.Session, message extension.CustomMessageRef, options *extension.SendMessageOptions) error {
	if sess == nil {
		return errSessionNotReady
	}
	return sess.SendMessage(message, options)
}

func sendSessionUserMessage(sess *coding.Session, content any, options *extension.SendUserMessageOptions) error {
	if sess == nil {
		return errSessionNotReady
	}
	return sess.SendExtensionUserMessage(content, options)
}

// errSessionNotReady rejects a session action before the session exists.
var errSessionNotReady = errors.New("agent session not initialized")

// bindSessionExtensionActions binds the session-backed actions for in-process
// extensions (runner) and subprocess extensions (bridge host actions).
// contextActions carries the mode's other context actions, including the extension flag values supplied before session_start.
//
// replacement optionally supplies the command actions of a Session, so a mode that owns a Runtime routes newSession, fork and switchSession through it. Without it the Session's own actions apply.
func bindSessionExtensionActions(runner *inproc.Runner, bridge *subprocess.UIBridge, current func() *coding.Session, contextActions extension.ContextActions, replacement ...func(*coding.Session) extension.CommandActions) {
	commandActions := func(sess *coding.Session) extension.CommandActions {
		if len(replacement) > 0 && replacement[0] != nil {
			return replacement[0](sess)
		}
		return sess.ExtensionCommandActions()
	}
	actions, sessionContext := sessionExtensionActions(current)
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
				return errSessionNotReady
			}
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
	bridge.SetHostAction("sendMessage", func(message extension.CustomMessageRef, opts subprocess.SendMessageOptions) error {
		return sendSessionCustomMessage(current(), message, &extension.SendMessageOptions{
			TriggerTurn: opts.TriggerTurn,
			DeliverAs:   extension.DeliverAs(opts.DeliverAs),
		})
	})
	bridge.SetHostAction("sendUserMessage", func(content any, opts subprocess.SendUserMessageOptions) error {
		return sendSessionUserMessage(current(), content, &extension.SendUserMessageOptions{DeliverAs: extension.DeliverAs(opts.DeliverAs)})
	})
	// Upstream _bindExtensionCore binds getActiveTools and setActiveTools to
	// the session in every mode (getActiveToolNames, setActiveToolsByName).
	bridge.SetHostAction("getActiveTools", func() []string {
		if sess := current(); sess != nil {
			return sess.ActiveToolNames()
		}
		return []string{}
	})
	bridge.SetHostAction("setActiveTools", func(names []string) {
		if sess := current(); sess != nil {
			sess.SetActiveToolsByName(names)
		}
	})
	bridge.SetHostAction("refreshTools", func() error {
		if sess := current(); sess != nil {
			return sess.RefreshTools()
		}
		return errSessionNotReady
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
			return false, errSessionNotReady
		}
		model, err := coding.BuildModel(spec, sess.Services())
		if err != nil {
			return false, err
		}
		return sess.ExtensionSetModel(ctx, model)
	})
	bridge.SetHostAction("setThinkingLevel", func(level string) {
		sess := current()
		if sess == nil {
			return
		}
		// The call is fire-and-forget on the wire, so a failure to persist the change reaches the error listeners, as Pi reports its unawaited Session actions (agent-session.ts:3303-3310).
		if err := sess.SetThinkingLevel(ai.ThinkingLevel(level)); err != nil {
			if runner := sess.ExtensionRunner(); runner != nil {
				runner.EmitError(&extension.ExtensionError{ExtensionPath: "<runtime>", Event: "set_thinking_level", Error: err.Error()})
			}
		}
	})
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
			opts.OnError(errSessionNotReady)
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
	bridge.SetHostAction("exec", func(command string, args []string, opts *extension.ExecOptions) (extension.ExecResult, error) {
		cwd := ""
		if sess := current(); sess != nil {
			cwd = sess.CWD()
		}
		return extension.ExecCommand(context.Background(), cwd, command, args, opts)
	})
	bridge.BindCommandActions(extension.CommandActions{
		NewSessionContext: func(ctx context.Context, opts *extension.NewSessionOptions) (extension.CancelledResult, error) {
			if sess := current(); sess != nil {
				return commandActions(sess).NewSessionContext(ctx, opts)
			}
			return extension.CancelledResult{}, errSessionNotReady
		},
		ForkContext: func(ctx context.Context, entryID string, opts *extension.ForkOptions) (extension.CancelledResult, error) {
			if sess := current(); sess != nil {
				return commandActions(sess).ForkContext(ctx, entryID, opts)
			}
			return extension.CancelledResult{}, errSessionNotReady
		},
		SwitchSessionContext: func(ctx context.Context, path string, opts *extension.SwitchSessionOptions) (extension.CancelledResult, error) {
			if sess := current(); sess != nil {
				return commandActions(sess).SwitchSessionContext(ctx, path, opts)
			}
			return extension.CancelledResult{}, errSessionNotReady
		},
		WaitForIdle: func() error {
			if sess := current(); sess != nil {
				return sess.ExtensionCommandActions().WaitForIdle()
			}
			return nil
		},
		NavigateTree: func(targetID string, opts *extension.NavigateTreeOptions) (extension.CancelledResult, error) {
			if sess := current(); sess != nil {
				return sess.ExtensionCommandActions().NavigateTree(targetID, opts)
			}
			return extension.CancelledResult{}, errSessionNotReady
		},
		// print-mode.ts:97-99 and rpc-mode.ts:341-343 bind reload to session.reload() in every headless mode.
		ReloadContext: func(ctx context.Context) error {
			sess := current()
			if sess == nil {
				extension.CallInitiated(ctx)
				return errSessionNotReady
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

// bindSessionReadActions binds the session manager reads subprocess
// extensions make, for print, JSON and RPC mode. Upstream hands every mode's
// extensions the session's SessionManager (runner.ts createContext
// sessionManager). cwd and sessionDir are the session manager's own.
func bindSessionReadActions(bridge *subprocess.UIBridge, current func() *coding.Session, cwd, sessionDir string) {
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
				return *leaf
			}
		}
		return ""
	})
	bridge.SetHostAction("getEntriesPage", func(cursor, maxBytes int) ([]json.RawMessage, int, bool, string) {
		sess := current()
		if sess == nil {
			return nil, 0, false, ""
		}
		entries := sess.Inner().Entries()
		if cursor < 0 || cursor > len(entries) {
			cursor = 0
		}
		page := make([]json.RawMessage, 0)
		bytes := 0
		next := cursor
		for next < len(entries) {
			raw := entries[next].Raw()
			entryBytes := len(raw) + 1
			if len(page) > 0 && bytes+entryBytes > maxBytes {
				break
			}
			if len(raw) > 0 {
				page = append(page, json.RawMessage(raw))
				bytes += entryBytes
			}
			next++
		}
		leafID := ""
		if leaf := sess.LeafID(); leaf != nil {
			leafID = *leaf
		}
		return page, next, next < len(entries), leafID
	})
	// pi.setLabel is upstream's sessionManager.appendLabelChange in every
	// mode (agent-session.ts _bindExtensionCore). An empty label clears it.
	bridge.SetHostAction("setLabel", func(entryID, label string) error {
		sess := current()
		if sess == nil {
			return errSessionNotReady
		}
		var value *string
		if label != "" {
			value = &label
		}
		return sess.Inner().AppendLabelChange(entryID, value)
	})
	bridge.SetHostAction("sessionRead", func(method string, args json.RawMessage) (any, error) {
		view := codingagent.ExtensionSessionView{CWD: cwd, SessionDir: codingagent.ExtensionSessionDir(sessionDir)}
		if sess := current(); sess != nil {
			view.Session = sess.Inner()
		}
		return codingagent.ExtensionSessionRead(view, method, args)
	})
}

// bindSessionAppendEntry binds the custom-entry append for print and JSON
// mode: upstream's appendEntry and ctx.sessionManager.appendCustomEntry both
// write the session's log there (agent-session.ts _bindExtensionCore).
func bindSessionAppendEntry(bridge *subprocess.UIBridge, current func() *coding.Session) {
	if bridge == nil {
		return
	}
	bridge.SetHostAction("appendEntry", func(customType string, data any, direct *subprocess.DirectEntryAppend) error {
		sess := current()
		if sess == nil {
			return errSessionNotReady
		}
		_, err := codingagent.AppendExtensionEntry(sess.Inner(), customType, data, direct)
		return err
	})
}
