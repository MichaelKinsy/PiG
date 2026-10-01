package coding

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Runtime constructs Sessions and, when created with CreateAgentSessionRuntime, owns replacement through the retained cwd-bound factory.
type Runtime struct {
	replacement runtimeReplacement
	services    *Services
	extCtx      *icodingagent.ExtensionContext

	// beforeSessionInvalidate runs synchronously before the extension runner is
	// invalidated. Mirrors upstream session-replacement teardown ordering for
	// hosts that need to detach UI state before old extension contexts go stale.
	beforeSessionInvalidate func()

	// newRunner is the extension runner (inproc.Runner).
	// Holds tools, commands, and event handlers registered by extensions.
	newRunner *inproc.Runner
	newExts   []extension.Extension
}

// RuntimeOptions configures Runtime construction.
type RuntimeOptions struct {
	// Services is the dependency container produced by NewServices.
	// Required.
	Services *Services

	// NewExtensions are the extensions (`coding/extension`) to pre-load.
	// Their tools layer into every Session created by this Runtime.
	NewExtensions []extension.Extension

	// ExtensionRuntime is the registration state the extension host that loaded NewExtensions already holds (MCP servers,
	// providers, virtual models). The Session's runner shares it, so a registration by an out-of-process extension reaches
	// the runner's `mcp_servers_change` dispatch and `getMcpServers`. Nil gives the runner its own state.
	ExtensionRuntime *extension.ExtensionRuntime

	// AbortContext is the parent context for the runtime's own
	// cancellation signals. If nil, context.Background() is used.
	AbortContext context.Context
}

// SessionStartOptions configures a single Session within a Runtime.
type SessionStartOptions struct {
	// SessionManager selects the actual log to wrap, ahead of file loading or creation.
	SessionManager *SessionManager

	// ScopedModels supplies the read-only cycling scope, including optional per-model thinking levels.
	ScopedModels []extension.ScopedModel

	// Model is the LLM the agent calls. Required.
	Model *ai.Model

	// ThinkingLevel overrides restored and configured preferences before model clamping. Empty uses the Session or settings preference.
	ThinkingLevel ai.ThinkingLevel

	// SystemPrompt is the system prompt.
	SystemPrompt string

	// SystemPromptSections carries the caller-built structured prompt.
	SystemPromptSections ai.OrderedSections

	// SystemPromptResources is the resource-loader state behind SystemPromptSections.
	SystemPromptResources *SystemPromptResources

	// ResourceLoader supplies the Session's resources. See SessionOptions.ResourceLoader.
	ResourceLoader ResourceLoader

	// AllowedTools restricts which tools may execute.
	AllowedTools map[string]struct{}

	// ActiveBuiltinTools, when non-nil, restricts which built-in coding tools
	// are active (extension/extra tools are unaffected, and a name that is
	// not a built-in activates the extension tool of that name). nil means all
	// built-in tools. Mirrors upstream defaultActiveToolNames (sdk.ts:244):
	// the CLI default is [read, bash, edit, write], so grep/find/ls are
	// registered but inactive unless requested via --tools.
	ActiveBuiltinTools map[string]struct{}

	// ExcludedTools is a denylist of tool names removed from the final tool
	// set after allow/active filtering. Gates built-in and extension/caller
	// tools alike. Mirrors upstream excludedToolNames (sdk.ts:246).
	ExcludedTools map[string]struct{}

	// NoTools mirrors upstream SDK noTools behavior when no explicit allowlist
	// is provided.
	//   - "all": expose no tools
	//   - "builtin": omit built-in coding tools but keep extension/extra tools
	NoTools string

	// SkipBuiltinTools, when true, omits the built-in coding tools while still
	// allowing extension and extra tools.
	SkipBuiltinTools bool

	// BeforeToolCall hooks run before each tool call and may block.
	BeforeToolCall []agent.BeforeToolCallHook

	// ExtraTools are tools to add ON TOP of extension tools.
	ExtraTools []agent.AgentTool

	// SkipExtensionTools, when true, omits extension tools.
	SkipExtensionTools bool

	// ResumePath, when non-empty, loads an existing JSONL.
	ResumePath string
	// CWDOverride selects the effective cwd when opening a Session.
	CWDOverride *string

	// SessionDir overrides the on-disk session directory for new sessions
	// and session lookups such as Resume/Continue.
	SessionDir string

	// SessionID specifies an exact session ID for new sessions.
	// Mirrors upstream --session-id (v0.76.0).
	SessionID string

	// NoSession disables session persistence (ephemeral mode).
	// Mirrors upstream --no-session (args.ts:92).
	NoSession bool
}

// NewRuntime constructs a Runtime.
func NewRuntime(opts RuntimeOptions) (*Runtime, error) {
	if opts.Services == nil {
		return nil, fmt.Errorf("coding: NewRuntime: Services is required")
	}
	abortParent := opts.AbortContext
	if abortParent == nil {
		abortParent = context.Background()
	}
	abortCtx, abortFn := context.WithCancel(abortParent)

	extCtx := &icodingagent.ExtensionContext{
		CWD:         opts.Services.CWD(),
		AbortSignal: abortCtx,
		AbortFunc:   abortFn,
	}

	newRunner := inproc.NewRunner(opts.NewExtensions, opts.Services.CWD(), opts.ExtensionRuntime)
	newRunner.BindCore(extension.ExtensionActions{}, extension.ContextActions{
		ModelRegistry:    opts.Services.Registry(),
		IsProjectTrusted: opts.Services.SettingsManager().IsProjectTrusted,
	}, nil)

	return &Runtime{
		services:  opts.Services,
		extCtx:    extCtx,
		newRunner: newRunner,
		newExts:   opts.NewExtensions,
	}, nil
}

// Services returns the underlying dependency container.
func (rt *Runtime) Services() *Services {
	if current := rt.replacement.current.Load(); current != nil {
		return current.Services
	}
	return rt.services
}

// ExtensionContext returns the shared extension context.
func (rt *Runtime) ExtensionContext() *icodingagent.ExtensionContext { return rt.extCtx }

// NewExtensionRunner returns the extension runner.
func (rt *Runtime) NewExtensionRunner() *inproc.Runner {
	if session := rt.Session(); session != nil {
		return session.currentRunner()
	}
	return rt.newRunner
}

// SetBeforeSessionInvalidate installs a synchronous teardown hook that runs
// before the runner is invalidated.
func (rt *Runtime) SetBeforeSessionInvalidate(fn func()) { rt.beforeSessionInvalidate = fn }

// Close releases Runtime-level resources. Services supplied by the caller remain caller-owned and must be closed separately.
func (rt *Runtime) Close() error {
	if current := rt.replacement.current.Load(); current != nil {
		if !rt.replacement.closed.CompareAndSwap(false, true) {
			return nil
		}
		rt.EmitQuitShutdown()
		if rt.beforeSessionInvalidate != nil {
			rt.beforeSessionInvalidate()
		}
		if runner := current.Session.currentRunner(); runner != nil {
			runner.Invalidate("")
		}
		err := current.Session.Close()
		current.retireResources("quit")
		if rt.extCtx != nil && rt.extCtx.AbortFunc != nil {
			rt.extCtx.AbortFunc()
		}
		return err
	}
	if rt.extCtx != nil && rt.extCtx.AbortFunc != nil {
		rt.extCtx.AbortFunc()
	}
	if rt.beforeSessionInvalidate != nil {
		rt.beforeSessionInvalidate()
	}
	if rt.newRunner != nil {
		rt.newRunner.Invalidate("runtime closed")
	}
	return nil
}

// New creates a fresh Session.
func (rt *Runtime) New(opts SessionStartOptions) (*Session, error) {
	return rt.startSession(opts)
}

// Resume loads an existing Session by id.
func (rt *Runtime) Resume(id string, opts SessionStartOptions) (*Session, error) {
	sm := newSessionManagerForDir(rt.Services(), opts.SessionDir)
	path := sm.FindByID(id)
	if path == "" {
		return nil, fmt.Errorf("coding: Resume: session id %q not found in %s", id, sm.SessionDir())
	}
	opts.ResumePath = path
	return rt.startSession(opts)
}

// Open loads an existing Session by path.
func (rt *Runtime) Open(path string, opts SessionStartOptions) (*Session, error) {
	if path == "" {
		return nil, fmt.Errorf("coding: Open: path is empty")
	}
	opts.ResumePath = path
	return rt.startSession(opts)
}

// Continue resumes the most-recent Session.
func (rt *Runtime) Continue(opts SessionStartOptions) (*Session, error) {
	sm := newSessionManagerForDir(rt.Services(), opts.SessionDir)
	path := sm.FindMostRecent()
	if path == "" {
		return nil, fmt.Errorf("coding: Continue: no prior session in %s", sm.SessionDir())
	}
	opts.ResumePath = path
	return rt.startSession(opts)
}

// ListSessions returns SessionInfo summaries.
func (rt *Runtime) ListSessions() ([]SessionInfo, error) {
	sm := newSessionManagerForDir(rt.Services(), "")
	return sm.ListSessions()
}

// startSession is the common construction path.
func (rt *Runtime) startSession(opts SessionStartOptions) (*Session, error) {
	return rt.startSessionWithFactory(opts, NewSession)
}

func (rt *Runtime) startSessionWithFactory(opts SessionStartOptions, create func(*Services, SessionOptions) (*Session, error)) (*Session, error) {
	var resumed *icodingagent.Session
	if opts.SessionManager == nil && opts.ResumePath != "" {
		var override []string
		if opts.CWDOverride != nil {
			override = []string{*opts.CWDOverride}
		}
		target, err := newSessionManagerForDir(rt.Services(), opts.SessionDir).Open(opts.ResumePath, override...)
		if err != nil {
			return nil, err
		}
		if err := icodingagent.AssertSessionCwdExists(target, rt.Services().CWD()); err != nil {
			return nil, err
		}
		resumed = target
	}
	return create(rt.Services(), SessionOptions{
		resumed:               resumed,
		SessionManager:        opts.SessionManager,
		ScopedModels:          opts.ScopedModels,
		Model:                 opts.Model,
		ThinkingLevel:         opts.ThinkingLevel,
		SystemPrompt:          opts.SystemPrompt,
		SystemPromptSections:  opts.SystemPromptSections,
		SystemPromptResources: opts.SystemPromptResources,
		ResourceLoader:        opts.ResourceLoader,
		Tools:                 opts.ExtraTools,
		NoTools:               opts.NoTools,
		skipExtensionTools:    opts.SkipExtensionTools,
		AllowedTools:          opts.AllowedTools,
		ActiveBuiltinTools:    opts.ActiveBuiltinTools,
		ExcludedTools:         opts.ExcludedTools,
		SkipBuiltinTools:      opts.SkipBuiltinTools,
		BeforeToolCall:        opts.BeforeToolCall,
		Runner:                rt.NewExtensionRunner(),
		ResumePath:            opts.ResumePath,
		CWDOverride:           opts.CWDOverride,
		SessionDir:            opts.SessionDir,
		SessionID:             opts.SessionID,
		NoSession:             opts.NoSession,
		Transport:             ai.Transport(rt.Services().Settings().Transport),
		runnerShared:          true,
	})
}
