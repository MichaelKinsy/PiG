package sdk

import (
	"context"
	"fmt"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// ExtensionRunner is Pi's `ExtensionRunner` class (core/extensions/runner.ts): the dispatcher and registry over the extensions a [DefaultResourceLoader] loaded. The host owns the runner and the extensions stay in their own processes, as a loader's do; the runner dispatches events to them in extension order with Pi's chaining and error isolation, and answers the registries (tools, commands, flags, shortcuts, handlers) from them.
//
// Pi's runner is built over `Extension` objects that hold the extensions' functions. Here an extension is named by [LoadedExtension], the data [DefaultResourceLoader.GetExtensions] returns, and an extension built in this process (an inline factory) cannot take part: it would run here while the runner runs in the host. Load it through [DefaultResourceLoaderOptions].AdditionalExtensionPaths.
//
// Events and their results cross as Pi's JSON values (a map for an event, the handler chain's result as the JSON it is). The functions of [ExtensionRunner.BindCore], [ExtensionRunner.BindCommandContext] and [ExtensionRunner.SetUIContext] run in this process, called by the host when an extension asks for the action; they may call the runner. Each call blocks until Pi's would settle; the reads are calls on the host's state, so a read after a write sees the write.
type ExtensionRunner struct {
	object         *graphObject
	sessionManager *SessionManager
	modelRegistry  *ModelRuntime

	mu             sync.Mutex
	actions        ExtensionActions
	contextActions ExtensionContextActions
	commandActions *ExtensionCommandContextActions
}

// NewExtensionRunner is `new ExtensionRunner(extensions, runtime, cwd, sessionManager, modelRegistry)`. extensions are the [LoadedExtension] values of a [DefaultResourceLoader.GetExtensions] result, runtime its Runtime. The sessionManager is the log ctx.sessionManager exposes and the modelRegistry the registry ctx.modelRegistry reads and provider registration applies to; Pi's ModelRegistry class is served as [ModelRuntime]. A nil runtime is the runtime the extensions share; a nil sessionManager or modelRegistry is not bound.
func NewExtensionRunner(c Context, extensions []LoadedExtension, runtime *ExtensionRuntime, cwd string, sessionManager *SessionManager, modelRegistry *ModelRuntime) (*ExtensionRunner, error) {
	refs := make([]map[string]string, len(extensions))
	for i, ext := range extensions {
		if ext.loader == "" {
			return nil, fmt.Errorf("extension %s was not read from a DefaultResourceLoader: an extension built in this process cannot join the runner, load it with AdditionalExtensionPaths", ext.Path)
		}
		refs[i] = map[string]string{"loader": ext.loader, "resolvedPath": ext.ResolvedPath}
	}
	wire := map[string]any{"extensions": refs, "cwd": cwd}
	if runtime != nil {
		wire["runtime"] = runtime.Handle()
	}
	if sessionManager != nil {
		wire["sessionManager"] = sessionManager.Handle()
	}
	if modelRegistry != nil {
		wire["modelRegistry"] = modelRegistry.Handle()
	}
	object, err := newGraphObject(c, "ExtensionRunner", "new", wire)
	if err != nil {
		return nil, err
	}
	return &ExtensionRunner{object: object, sessionManager: sessionManager, modelRegistry: modelRegistry}, nil
}

// Handle is the host's name for the runner.
func (r *ExtensionRunner) Handle() string { return r.object.Handle() }

// ExtensionActions is Pi's ExtensionActions: the functions [ExtensionRunner.BindCore] installs, which the extensions' pi.sendMessage and the rest call. A nil function is Pi's undefined: it is not bound, and the runtime's own default applies. A function that returns an error is Pi's thrown error.
type ExtensionActions struct {
	SendMessage      func(message CustomMessage, options *SendMessageOptions) error
	SendUserMessage  func(content any, options *SendUserMessageOptions) error
	AppendEntry      func(customType string, data any) error
	SetSessionName   func(name string) error
	GetSessionName   func() string
	SetLabel         func(entryID string, label *string) error
	GetActiveTools   func() []string
	GetAllTools      func() []ToolInfo
	GetSettings      func() Settings
	SetActiveTools   func(names []string) error
	RefreshTools     func() error
	GetCommands      func() []CommandInfo
	SetModel         func(model map[string]any) (bool, error)
	GetThinkingLevel func() string
	SetThinkingLevel func(level string)
}

// SendUserMessageOptions is the options of Pi's sendUserMessage.
type SendUserMessageOptions struct {
	DeliverAs             string `json:"deliverAs,omitempty"`
	ExpandPromptTemplates *bool  `json:"expandPromptTemplates,omitempty"`
}

// ExtensionContextActions is Pi's ExtensionContextActions: the functions the contexts read. A nil function is Pi's undefined.
type ExtensionContextActions struct {
	GetModel           func() map[string]any
	GetScopedModels    func() []ScopedModel
	IsIdle             func() bool
	IsProjectTrusted   func() bool
	GetSignal          func() context.Context
	Abort              func()
	HasPendingMessages func() bool
	Shutdown           func()
	GetContextUsage    func() *ContextUsage
	// Compact receives the options of the extension that asked (its callbacks when this process asked through a context).
	Compact                func(options *CompactOptions)
	GetSystemPrompt        func() string
	GetSystemPromptOptions func() *SystemPromptOptions
	// ExecuteTool runs the nested call; a call an extension of the runner makes passes no signal or update function (they run in that extension's process).
	ExecuteTool      func(toolCallID, name string, args any, options ExecuteToolOptions) (AgentToolCallOutcome, error)
	GetCallableTools func() []AgentTool
}

// ProviderActions is the providerActions argument of Pi's bindCore: how a provider an extension registers reaches the model registry. A nil function leaves the registry's own.
type ProviderActions struct {
	RegisterProvider   func(name string, config map[string]any) error
	UnregisterProvider func(name string)
}

// CommandNewSessionOptions is the options of Pi's newSession as the command context takes them.
type CommandNewSessionOptions struct {
	ParentSession string
	// Setup prepares the new session's log before it is used, and WithSession runs with the new session's context. They are functions of the process that asked for the replacement: a runner extension's request cannot carry them, and fails.
	Setup       func(sessionManager *SessionManager) error
	WithSession func(ctx *ExtensionCommandContext) error
}

// ForkOptions is the options of Pi's fork: Position is "before" or "at".
type ForkOptions struct {
	Position    string
	WithSession func(ctx *ExtensionCommandContext) error
}

// NavigateTreeOptions is the options of Pi's navigateTree.
type NavigateTreeOptions struct {
	Summarize           bool   `json:"summarize,omitempty"`
	CustomInstructions  string `json:"customInstructions,omitempty"`
	ReplaceInstructions bool   `json:"replaceInstructions,omitempty"`
	Label               string `json:"label,omitempty"`
}

// SwitchSessionOptions is the options of Pi's switchSession.
type SwitchSessionOptions struct {
	WithSession func(ctx *ExtensionCommandContext) error
}

// ExtensionCommandContextActions is Pi's ExtensionCommandContextActions: the functions [ExtensionRunner.BindCommandContext] installs, which the command context calls. A nil function is not bound, and Pi's default applies (resolve at once, or not cancelled).
type ExtensionCommandContextActions struct {
	WaitForIdle   func() error
	NewSession    func(options *CommandNewSessionOptions) (CancelledResult, error)
	Fork          func(entryID string, options *ForkOptions) (CancelledResult, error)
	NavigateTree  func(targetID string, options *NavigateTreeOptions) (CancelledResult, error)
	SwitchSession func(sessionPath string, options *SwitchSessionOptions) (CancelledResult, error)
	Reload        func() error
}

// graphArg decodes argument i of a callback into a T; a missing or null argument is T's zero value.
func graphArg[T any](args []json.RawMessage, i int) (T, error) {
	var value T
	if i >= len(args) || string(args[i]) == "null" {
		return value, nil
	}
	err := json.Unmarshal(args[i], &value)
	return value, err
}

// bindCoreWire names the functions of actions, contextActions and providerActions to the host.
func (r *ExtensionRunner) bindCoreWire(actions ExtensionActions, contextActions ExtensionContextActions, providers *ProviderActions) (wireActions, wireContext, wireProviders map[string]any) {
	c := r.object.context
	named := func(table map[string]any, name string, callback graphCallback) {
		if callback != nil {
			table[name] = c.callbackID(callback)
		}
	}
	wireActions, wireContext, wireProviders = map[string]any{}, map[string]any{}, map[string]any{}
	if f := actions.SendMessage; f != nil {
		named(wireActions, "sendMessage", func(args []json.RawMessage) (any, error) {
			message, err := graphArg[CustomMessage](args, 0)
			if err != nil {
				return nil, err
			}
			options, err := graphArg[*SendMessageOptions](args, 1)
			if err != nil {
				return nil, err
			}
			return nil, f(message, options)
		})
	}
	if f := actions.SendUserMessage; f != nil {
		named(wireActions, "sendUserMessage", func(args []json.RawMessage) (any, error) {
			content, err := graphArg[any](args, 0)
			if err != nil {
				return nil, err
			}
			options, err := graphArg[*SendUserMessageOptions](args, 1)
			if err != nil {
				return nil, err
			}
			return nil, f(content, options)
		})
	}
	if f := actions.AppendEntry; f != nil {
		named(wireActions, "appendEntry", func(args []json.RawMessage) (any, error) {
			customType, err := graphArg[string](args, 0)
			if err != nil {
				return nil, err
			}
			data, err := graphArg[any](args, 1)
			if err != nil {
				return nil, err
			}
			return nil, f(customType, data)
		})
	}
	if f := actions.SetSessionName; f != nil {
		named(wireActions, "setSessionName", func(args []json.RawMessage) (any, error) {
			name, err := graphArg[string](args, 0)
			if err != nil {
				return nil, err
			}
			return nil, f(name)
		})
	}
	if f := actions.GetSessionName; f != nil {
		named(wireActions, "getSessionName", func([]json.RawMessage) (any, error) { return f(), nil })
	}
	if f := actions.SetLabel; f != nil {
		named(wireActions, "setLabel", func(args []json.RawMessage) (any, error) {
			entryID, err := graphArg[string](args, 0)
			if err != nil {
				return nil, err
			}
			label, err := graphArg[*string](args, 1)
			if err != nil {
				return nil, err
			}
			return nil, f(entryID, label)
		})
	}
	if f := actions.GetActiveTools; f != nil {
		named(wireActions, "getActiveTools", func([]json.RawMessage) (any, error) { return f(), nil })
	}
	if f := actions.GetAllTools; f != nil {
		named(wireActions, "getAllTools", func([]json.RawMessage) (any, error) { return f(), nil })
	}
	if f := actions.GetSettings; f != nil {
		named(wireActions, "getSettings", func([]json.RawMessage) (any, error) { return f(), nil })
	}
	if f := actions.SetActiveTools; f != nil {
		named(wireActions, "setActiveTools", func(args []json.RawMessage) (any, error) {
			names, err := graphArg[[]string](args, 0)
			if err != nil {
				return nil, err
			}
			return nil, f(names)
		})
	}
	if f := actions.RefreshTools; f != nil {
		named(wireActions, "refreshTools", func([]json.RawMessage) (any, error) { return nil, f() })
	}
	if f := actions.GetCommands; f != nil {
		named(wireActions, "getCommands", func([]json.RawMessage) (any, error) { return f(), nil })
	}
	if f := actions.SetModel; f != nil {
		named(wireActions, "setModel", func(args []json.RawMessage) (any, error) {
			model, err := graphArg[map[string]any](args, 0)
			if err != nil {
				return nil, err
			}
			return f(model)
		})
	}
	if f := actions.GetThinkingLevel; f != nil {
		named(wireActions, "getThinkingLevel", func([]json.RawMessage) (any, error) { return f(), nil })
	}
	if f := actions.SetThinkingLevel; f != nil {
		named(wireActions, "setThinkingLevel", func(args []json.RawMessage) (any, error) {
			level, err := graphArg[string](args, 0)
			if err != nil {
				return nil, err
			}
			f(level)
			return nil, nil
		})
	}
	if f := contextActions.GetModel; f != nil {
		named(wireContext, "getModel", func([]json.RawMessage) (any, error) { return f(), nil })
	}
	if f := contextActions.GetScopedModels; f != nil {
		named(wireContext, "getScopedModels", func([]json.RawMessage) (any, error) { return f(), nil })
	}
	if f := contextActions.IsIdle; f != nil {
		named(wireContext, "isIdle", func([]json.RawMessage) (any, error) { return f(), nil })
	}
	if f := contextActions.IsProjectTrusted; f != nil {
		named(wireContext, "isProjectTrusted", func([]json.RawMessage) (any, error) { return f(), nil })
	}
	if f := contextActions.GetSignal; f != nil {
		named(wireContext, "getSignal", func([]json.RawMessage) (any, error) { return signalWire(f()), nil })
	}
	if f := contextActions.Abort; f != nil {
		named(wireContext, "abort", func([]json.RawMessage) (any, error) { f(); return nil, nil })
	}
	if f := contextActions.HasPendingMessages; f != nil {
		named(wireContext, "hasPendingMessages", func([]json.RawMessage) (any, error) { return f(), nil })
	}
	if f := contextActions.Shutdown; f != nil {
		named(wireContext, "shutdown", func([]json.RawMessage) (any, error) { f(); return nil, nil })
	}
	if f := contextActions.GetContextUsage; f != nil {
		named(wireContext, "getContextUsage", func([]json.RawMessage) (any, error) { return f(), nil })
	}
	if f := contextActions.Compact; f != nil {
		named(wireContext, "compact", func(args []json.RawMessage) (any, error) {
			var options struct {
				CustomInstructions string `json:"customInstructions"`
			}
			if len(args) > 0 && string(args[0]) != "null" {
				if err := json.Unmarshal(args[0], &options); err != nil {
					return nil, err
				}
			}
			f(&CompactOptions{CustomInstructions: options.CustomInstructions})
			return nil, nil
		})
	}
	if f := contextActions.GetSystemPrompt; f != nil {
		named(wireContext, "getSystemPrompt", func([]json.RawMessage) (any, error) { return f(), nil })
	}
	if f := contextActions.GetSystemPromptOptions; f != nil {
		named(wireContext, "getSystemPromptOptions", func([]json.RawMessage) (any, error) { return f(), nil })
	}
	if f := contextActions.ExecuteTool; f != nil {
		named(wireContext, "executeTool", func(args []json.RawMessage) (any, error) {
			callerID, err := graphArg[string](args, 0)
			if err != nil {
				return nil, err
			}
			name, err := graphArg[string](args, 1)
			if err != nil {
				return nil, err
			}
			params, err := graphArg[any](args, 2)
			if err != nil {
				return nil, err
			}
			return f(callerID, name, params, ExecuteToolOptions{})
		})
	}
	if f := contextActions.GetCallableTools; f != nil {
		named(wireContext, "getCallableTools", func([]json.RawMessage) (any, error) { return f(), nil })
	}
	if providers != nil {
		if f := providers.RegisterProvider; f != nil {
			named(wireProviders, "registerProvider", func(args []json.RawMessage) (any, error) {
				name, err := graphArg[string](args, 0)
				if err != nil {
					return nil, err
				}
				config, err := graphArg[map[string]any](args, 1)
				if err != nil {
					return nil, err
				}
				return nil, f(name, config)
			})
		}
		if f := providers.UnregisterProvider; f != nil {
			named(wireProviders, "unregisterProvider", func(args []json.RawMessage) (any, error) {
				name, err := graphArg[string](args, 0)
				if err != nil {
					return nil, err
				}
				f(name)
				return nil, nil
			})
		}
	}
	return wireActions, wireContext, wireProviders
}

// signalWire is a signal as the host reads it: undefined, or `{aborted}`.
func signalWire(signal context.Context) any {
	if signal == nil {
		return nil
	}
	return map[string]any{"aborted": signal.Err() != nil}
}

// BindCore is `bindCore(actions, contextActions, providerActions?)`: it installs the functions the extensions' actions and contexts call, and applies the provider registrations the extensions queued while loading (to providerActions, or to the model registry the runner was built with). A nil providerActions is Pi's undefined.
func (r *ExtensionRunner) BindCore(actions ExtensionActions, contextActions ExtensionContextActions, providerActions *ProviderActions) error {
	wireActions, wireContext, wireProviders := r.bindCoreWire(actions, contextActions, providerActions)
	args := []any{wireActions, wireContext}
	if providerActions != nil {
		args = append(args, wireProviders)
	}
	if _, err := graphMethodCall[graphDone](r.object, "bindCore", args...); err != nil {
		return err
	}
	r.mu.Lock()
	r.actions, r.contextActions = actions, contextActions
	r.mu.Unlock()
	return nil
}

// BindCommandContext is `bindCommandContext(actions?)`. A nil actions is Pi's undefined: it restores the defaults (waitForIdle and reload resolve, a session replacement is not cancelled).
func (r *ExtensionRunner) BindCommandContext(actions *ExtensionCommandContextActions) error {
	wire := map[string]any{}
	c := r.object.context
	if actions != nil {
		if f := actions.WaitForIdle; f != nil {
			wire["waitForIdle"] = c.callbackID(func([]json.RawMessage) (any, error) { return nil, f() })
		}
		if f := actions.NewSession; f != nil {
			wire["newSession"] = c.callbackID(func(args []json.RawMessage) (any, error) {
				options, err := graphArg[struct {
					ParentSession string `json:"parentSession"`
				}](args, 0)
				if err != nil {
					return nil, err
				}
				return f(&CommandNewSessionOptions{ParentSession: options.ParentSession})
			})
		}
		if f := actions.Fork; f != nil {
			wire["fork"] = c.callbackID(func(args []json.RawMessage) (any, error) {
				entryID, err := graphArg[string](args, 0)
				if err != nil {
					return nil, err
				}
				options, err := graphArg[struct {
					Position string `json:"position"`
				}](args, 1)
				if err != nil {
					return nil, err
				}
				return f(entryID, &ForkOptions{Position: options.Position})
			})
		}
		if f := actions.NavigateTree; f != nil {
			wire["navigateTree"] = c.callbackID(func(args []json.RawMessage) (any, error) {
				targetID, err := graphArg[string](args, 0)
				if err != nil {
					return nil, err
				}
				options, err := graphArg[*NavigateTreeOptions](args, 1)
				if err != nil {
					return nil, err
				}
				return f(targetID, options)
			})
		}
		if f := actions.SwitchSession; f != nil {
			wire["switchSession"] = c.callbackID(func(args []json.RawMessage) (any, error) {
				path, err := graphArg[string](args, 0)
				if err != nil {
					return nil, err
				}
				return f(path, &SwitchSessionOptions{})
			})
		}
		if f := actions.Reload; f != nil {
			wire["reload"] = c.callbackID(func([]json.RawMessage) (any, error) { return nil, f() })
		}
	}
	args := []any{}
	if actions != nil {
		args = append(args, wire)
	}
	if _, err := graphMethodCall[graphDone](r.object, "bindCommandContext", args...); err != nil {
		return err
	}
	r.mu.Lock()
	r.commandActions = actions
	r.mu.Unlock()
	return nil
}

// ExtensionError is Pi's ExtensionError: a handler's failure, as the runner reports it.
type ExtensionError struct {
	ExtensionPath string `json:"extensionPath"`
	Event         string `json:"event"`
	Error         string `json:"error"`
	Stack         string `json:"stack,omitempty"`
}

// OnError is `onError(listener)`: the listener runs, in this process, for each error the runner reports. The returned function removes it.
func (r *ExtensionRunner) OnError(listener func(ExtensionError)) (func(), error) {
	c := r.object.context
	id := c.callbackID(func(args []json.RawMessage) (any, error) {
		report, err := graphArg[ExtensionError](args, 0)
		if err != nil {
			return nil, err
		}
		listener(report)
		return nil, nil
	})
	handle, err := graphMethodCall[string](r.object, "onError", id)
	if err != nil {
		return nil, err
	}
	return func() { _, _ = graphMethodCall[graphDone](r.object, "offError", handle) }, nil
}

// EmitError is `emitError(error)`: it reports error to the runner's listeners.
func (r *ExtensionRunner) EmitError(report ExtensionError) error {
	_, err := graphMethodCall[graphDone](r.object, "emitError", report)
	return err
}
