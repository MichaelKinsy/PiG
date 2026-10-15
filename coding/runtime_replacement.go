package coding

// Ports packages/coding-agent/src/core/agent-session-runtime.ts.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
)

// CreateAgentSessionRuntimeOptions selects the destination log and cwd-bound factory inputs.
type CreateAgentSessionRuntimeOptions struct {
	CWD               string
	AgentDir          string
	SessionManager    *SessionManager
	SessionStartEvent *extension.SessionStartEvent
	// ProjectTrustContext is the trust context of CWD for a replacement that the interactive mode drives (agent-session-runtime.ts:40, 219); its UI serves the project trust prompt. The factory falls back to a headless context without it.
	ProjectTrustContext *extension.ProjectTrustContext
}

// CreateAgentSessionRuntimeResult is agent-session-runtime.ts:23 `interface CreateAgentSessionRuntimeResult extends CreateAgentSessionResult`: the members of CreateAgentSessionResult (Session, ExtensionsResult, ModelFallbackMessage) and the Services and Diagnostics of the construction, flat as in 0.4.1. A factory carries the created result's members over as Pi's does ({ ...created, services, diagnostics }, main.ts:839-860).
type CreateAgentSessionRuntimeResult struct {
	Session  *Session
	Services *AgentSessionServices
	// ExtensionsResult is what the Session's resource loader loaded (sdk.ts CreateAgentSessionResult.extensionsResult).
	ExtensionsResult LoadExtensionsResult
	// Diagnostics are the startup diagnostics of the construction. Runtime.Diagnostics returns them until a replacement applies its own.
	Diagnostics []AgentSessionRuntimeDiagnostic
	// ModelFallbackMessage reports a saved model that could not be restored, or that no model is available. Runtime.ModelFallbackMessage returns it until a replacement applies its own.
	ModelFallbackMessage string
	// Dispose requests retirement of factory-owned resources after logical Session disposal. It must not wait for callbacks that replacement has not invoked yet.
	Dispose func(reason string)
}

// CreateAgentSessionRuntimeFactory rebuilds destination-CWD Services, resources, tools and extension instances, then constructs a Session over the supplied log. It returns only when construction is complete.
type CreateAgentSessionRuntimeFactory func(context.Context, CreateAgentSessionRuntimeOptions) (CreateAgentSessionRuntimeResult, error)

type runtimeInstance struct {
	CreateAgentSessionRuntimeResult
	retireOnce sync.Once
}

func (instance *runtimeInstance) retireResources(reason string) {
	instance.retireOnce.Do(func() {
		// pig additive (D19): the factory owns subprocess resources; logical Session disposal does not wait for a callback's leased transport to retire physically.
		if instance.Dispose != nil {
			instance.Dispose(reason)
		}
	})
}

type runtimeReplacement struct {
	current       atomic.Pointer[runtimeInstance]
	closed        atomic.Bool
	quitEmitted   atomic.Bool
	createRuntime CreateAgentSessionRuntimeFactory
	rebindSession func(context.Context, *Session) error
	// reload is the mode's session.reload() for the current Session (agent-session.ts:3575-3625); nil until the mode installs it.
	reload func(context.Context, *Session) error
	// drain settles mode-owned work of the outgoing Session after cancellable before hooks approved the replacement and before the Session aborts and shuts down.
	drain func(context.Context) error
}

// NewAgentSessionRuntime is upstream's `new AgentSessionRuntime(session, services, createRuntime, diagnostics, modelFallbackMessage)` (agent-session-runtime.ts:81-93):
// a Runtime that owns the given Session and its Services and retains createRuntime for later replacements. The Session must be the one constructed over services.
// ctx bounds the Runtime's own cancellation signal, as a Go call bounds it; upstream's constructor has no such input.
func NewAgentSessionRuntime(ctx context.Context, session *Session, services *AgentSessionServices, createRuntime CreateAgentSessionRuntimeFactory, diagnostics []AgentSessionRuntimeDiagnostic, modelFallbackMessage string) (*Runtime, error) {
	if session == nil || services == nil || createRuntime == nil {
		return nil, errors.New("coding: runtime Session, Services and factory are required")
	}
	abortCtx, abort := context.WithCancel(ctx)
	runtime := &Runtime{
		replacement: runtimeReplacement{createRuntime: createRuntime},
		extCtx:      &icodingagent.ExtensionContext{CWD: services.CWD(), AbortSignal: abortCtx, AbortFunc: abort},
	}
	result := CreateAgentSessionRuntimeResult{Session: session, Services: services, ExtensionsResult: session.ResourceLoader().GetExtensions(), Diagnostics: diagnostics, ModelFallbackMessage: modelFallbackMessage}
	var event *extension.SessionStartEvent
	if session.sessionStartEvent.Type != "" {
		// The Session keeps the start event it was constructed with. Copy it: applyRuntime resets the Session's field before it reads event.
		kept := session.sessionStartEvent
		event = &kept
	}
	if err := runtime.applyRuntime(result, event); err != nil {
		abort()
		return nil, err
	}
	return runtime, nil
}

// CreateAgentSessionRuntime constructs the first Session and retains the same factory for later replacements. The caller binds extensions after subscribing to Session events.
// upstream: agent-session-runtime.ts:426-438 (createAgentSessionRuntime: assert the cwd, run the factory, construct the AgentSessionRuntime over its result).
func CreateAgentSessionRuntime(ctx context.Context, factory CreateAgentSessionRuntimeFactory, options CreateAgentSessionRuntimeOptions) (*Runtime, error) {
	if factory == nil || options.SessionManager == nil {
		return nil, errors.New("coding: runtime factory and SessionManager are required")
	}
	if err := icodingagent.AssertSessionCwdExists(options.SessionManager, options.CWD); err != nil {
		return nil, err
	}
	result, err := factory(ctx, options)
	if err != nil {
		return nil, err
	}
	if result.Session == nil || result.Services == nil {
		return nil, errors.New("coding: runtime factory must return a Session and its Services")
	}
	result.Session.sessionStartEvent = extension.SessionStartEvent{Type: "session_start", Reason: "startup"}
	if options.SessionStartEvent != nil {
		result.Session.sessionStartEvent = *options.SessionStartEvent
	}
	runtime, err := NewAgentSessionRuntime(ctx, result.Session, result.Services, factory, result.Diagnostics, result.ModelFallbackMessage)
	if err != nil {
		return nil, err
	}
	// The Runtime holds the factory's result: its Dispose, and the ExtensionsResult the factory created rather than one rebuilt from the Session's loader.
	current := runtime.replacement.current.Load()
	current.Dispose = result.Dispose
	current.ExtensionsResult = result.ExtensionsResult
	return runtime, nil
}

// Session returns the current factory-owned Session, or nil before a low-level Runtime has constructed one.
func (rt *Runtime) Session() *Session {
	if current := rt.replacement.current.Load(); current != nil {
		return current.Session
	}
	return nil
}

// CWD returns the current Session's Services directory.
func (rt *Runtime) CWD() string { return rt.Services().CWD() }

// ExtensionCommandActions returns session's command-context actions with new, fork and switch replaced by the Runtime's replacement path, as Pi's print, RPC and interactive modes bind newSession, fork and switchSession to the runtime host. The other actions stay bound to session.
func (rt *Runtime) ExtensionCommandActions(session *Session) extension.CommandActions {
	bound := session.ExtensionCommandActions()
	bound.NewSessionContext = rt.NewSession
	bound.NewSession = func(options *extension.NewSessionOptions) (extension.CancelledResult, error) {
		return rt.NewSession(context.Background(), options)
	}
	bound.ForkContext = func(ctx context.Context, entryID string, options *extension.ForkOptions) (extension.CancelledResult, error) {
		result, err := rt.Fork(ctx, entryID, options)
		return extension.CancelledResult{Cancelled: result.Cancelled}, err
	}
	bound.Fork = func(entryID string, options *extension.ForkOptions) (extension.CancelledResult, error) {
		return bound.ForkContext(context.Background(), entryID, options)
	}
	bound.SwitchSessionContext = func(ctx context.Context, path string, options *extension.SwitchSessionOptions) (extension.CancelledResult, error) {
		return rt.SwitchSession(ctx, path, switchOptionsFromExtension(options))
	}
	bound.SwitchSession = func(path string, options *extension.SwitchSessionOptions) (extension.CancelledResult, error) {
		return rt.SwitchSession(context.Background(), path, switchOptionsFromExtension(options))
	}
	// print-mode.ts:97-99 and rpc-mode.ts:341-343 bind reload to session.reload(); this Session's mode runs it.
	bound.ReloadContext = func(ctx context.Context) error { return rt.reloadSession(ctx, session) }
	bound.Reload = func() error { return bound.ReloadContext(context.Background()) }
	return bound
}

// SetReload installs the mode's reload of the current Session: the work of upstream's session.reload() that depends on the resources and extension host the mode owns. Configure it before extensions run.
func (rt *Runtime) SetReload(reload func(context.Context, *Session) error) {
	rt.replacement.reload = reload
}

// reloadSession runs the mode's reload for session. Pi's reload returns a Promise, so the caller's call lane advances after its synchronous start.
func (rt *Runtime) reloadSession(ctx context.Context, session *Session) error {
	extension.CallInitiated(ctx)
	if rt.replacement.reload == nil {
		return errors.New("coding: Runtime has no reload")
	}
	return rt.replacement.reload(ctx, session)
}

// EmitQuitShutdown emits session_shutdown with reason quit for the current Session, once. A mode that must emit it before it tears its terminal down calls it; Close then does not repeat the event.
func (rt *Runtime) EmitQuitShutdown() {
	if !rt.replacement.quitEmitted.CompareAndSwap(false, true) {
		return
	}
	if session := rt.Session(); session != nil {
		session.EmitSessionShutdown("quit")
	}
}

// EmitQuitShutdownContext is EmitQuitShutdown with the context that dispatches the event to extensions, for a mode that awaits its handlers' acknowledgement.
func (rt *Runtime) EmitQuitShutdownContext(ctx context.Context) {
	if !rt.replacement.quitEmitted.CompareAndSwap(false, true) {
		return
	}
	session := rt.Session()
	if session == nil {
		return
	}
	if runner := session.currentRunner(); runner != nil && runner.HasHandlers(icodingagent.EventSessionShutdown) {
		_, _ = runner.Emit(ctx, extension.SessionShutdownEvent{Type: icodingagent.EventSessionShutdown, Reason: "quit"})
	}
}

// SetBeforeSessionReplacement installs the mode's awaited drain. It runs once a replacement is approved and before the outgoing Session aborts, so a mode can release queued input or join its own work. Configure it before starting replacement operations.
func (rt *Runtime) SetBeforeSessionReplacement(drain func(context.Context) error) {
	rt.replacement.drain = drain
}

// SetRebindSession installs the awaited host rebind callback. Configure it before starting replacement operations.
func (rt *Runtime) SetRebindSession(rebind func(context.Context, *Session) error) {
	rt.replacement.rebindSession = rebind
}

func (rt *Runtime) applyRuntime(result CreateAgentSessionRuntimeResult, event *extension.SessionStartEvent) error {
	if result.Session == nil || result.Services == nil || result.Session.services != result.Services {
		return errors.New("coding: runtime factory must return a Session and its owning Services")
	}
	result.Session.sessionStartEvent = extension.SessionStartEvent{Type: "session_start", Reason: "startup"}
	if event != nil {
		result.Session.sessionStartEvent = *event
	}
	runner := result.Session.currentRunner()
	if runner == nil {
		runner = inproc.NewRunner(nil, result.Services.CWD())
		result.Session.ReplaceRunner(runner)
	}
	result.Session.bindExtensionCore(runner)
	rt.replacement.current.Store(&runtimeInstance{CreateAgentSessionRuntimeResult: result})
	return nil
}

func (rt *Runtime) beforeSwitch(ctx context.Context, reason, target string) (extension.CancelledResult, error) {
	if rt.Session() == nil || rt.replacement.createRuntime == nil {
		return extension.CancelledResult{}, errors.New("coding: Runtime has no replacement factory")
	}
	// Pi's newSession and switchSession return a Promise, so the caller's call lane advances after this synchronous start.
	extension.CallInitiated(ctx)
	runner := rt.Session().currentRunner()
	if runner == nil {
		return extension.CancelledResult{}, nil
	}
	result, err := runner.Emit(ctx, extension.SessionBeforeSwitchEvent{Type: "session_before_switch", Reason: reason, TargetSessionFile: target})
	return extension.CancelledResult{Cancelled: beforeResultCancelled(result)}, err
}

// beforeResultCancelled reads the cancel flag of a session_before_switch or session_before_fork result. In-process handlers return the typed result. Subprocess handlers return their JSON.
func beforeResultCancelled(result any) bool {
	switch value := result.(type) {
	case nil:
		return false
	case *extension.SessionBeforeSwitchResult:
		return value != nil && value.Cancel
	case extension.SessionBeforeSwitchResult:
		return value.Cancel
	case *extension.SessionBeforeForkResult:
		return value != nil && value.Cancel
	case extension.SessionBeforeForkResult:
		return value.Cancel
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return false
	}
	var decoded struct {
		Cancel bool `json:"cancel"`
	}
	return json.Unmarshal(encoded, &decoded) == nil && decoded.Cancel
}

// It disposes the outgoing Session as Pi's session.dispose() does, which leaves the Session able to run a prompt, so a replacement that then fails keeps the disposed Session as the Runtime's, as Pi does. The caller closes it once the replacement applied.
func (rt *Runtime) teardownCurrent(ctx context.Context, reason, target string) error {
	current := rt.replacement.current.Load()
	session := current.Session
	if drain := rt.replacement.drain; drain != nil {
		if err := drain(ctx); err != nil {
			return err
		}
	}
	if err := session.Abort(ctx); err != nil {
		return err
	}
	if runner := session.currentRunner(); runner != nil {
		if _, err := runner.Emit(ctx, extension.SessionShutdownEvent{Type: "session_shutdown", Reason: reason, TargetSessionFile: target}); err != nil {
			return err
		}
	}
	if rt.beforeSessionInvalidate != nil {
		rt.beforeSessionInvalidate()
	}
	if runner := session.currentRunner(); runner != nil {
		runner.Invalidate("")
	}
	session.Dispose()
	current.retireResources(reason)
	return nil
}

func (rt *Runtime) replaceRuntime(ctx context.Context, manager *SessionManager, reason string, trustContext *extension.ProjectTrustContext) error {
	outgoing := rt.Session()
	previous := outgoing.Path()
	if err := rt.teardownCurrent(ctx, reason, manager.Path()); err != nil {
		return err
	}
	if err := rt.createReplacement(ctx, manager, reason, previous, trustContext); err != nil {
		return err
	}
	return outgoing.closeSuperseded(rt.Session())
}

func (rt *Runtime) createReplacement(ctx context.Context, manager *SessionManager, reason, previous string, trustContext *extension.ProjectTrustContext) error {
	agentDir := rt.Services().AgentDir()
	event := &extension.SessionStartEvent{Type: "session_start", Reason: reason, PreviousSessionFile: previous}
	result, err := rt.replacement.createRuntime(ctx, CreateAgentSessionRuntimeOptions{CWD: manager.GetCwd(), AgentDir: agentDir, SessionManager: manager, SessionStartEvent: event, ProjectTrustContext: trustContext})
	if err != nil {
		return err
	}
	return rt.applyRuntime(result, event)
}

func (rt *Runtime) finishSessionReplacement(ctx context.Context, withSession func(*extension.ReplacedSessionContext) error) error {
	if rebind := rt.replacement.rebindSession; rebind != nil {
		if err := rebind(ctx, rt.Session()); err != nil {
			return err
		}
	}
	if withSession != nil {
		return withSession(rt.Session().CreateReplacedSessionContext(ctx))
	}
	return nil
}

// NewSession tears down the outgoing Session and creates a fresh Session through the retained factory. Cancellation precedes all replacement work.
func (rt *Runtime) NewSession(ctx context.Context, options *extension.NewSessionOptions) (extension.CancelledResult, error) {
	before, err := rt.beforeSwitch(ctx, "new", "")
	if err != nil || before.Cancelled {
		return before, err
	}
	parent := ""
	if options != nil {
		parent = options.ParentSession
	}
	manager, err := rt.freshSessionManager(parent)
	if err != nil {
		return extension.CancelledResult{}, err
	}
	if err := rt.replaceRuntime(ctx, manager, "new", nil); err != nil {
		return extension.CancelledResult{}, err
	}
	if options != nil {
		if options.Setup != nil {
			if err := options.Setup(rt.Session().SessionManager()); err != nil {
				return extension.CancelledResult{}, err
			}
			rt.Session().RefreshContext()
		}
		return extension.CancelledResult{}, rt.finishSessionReplacement(ctx, options.WithSession)
	}
	return extension.CancelledResult{}, rt.finishSessionReplacement(ctx, nil)
}

func (rt *Runtime) freshSessionManager(parent string) (*SessionManager, error) {
	if !rt.Session().inner.IsPersisted() {
		manager, err := NewInMemorySessionManager(rt.CWD())
		if err == nil && parent != "" {
			_, err = manager.NewSession(&icodingagent.NewSessionOptions{ParentSession: parent})
		}
		return manager, err
	}
	id, err := icodingagent.GenerateSessionID()
	if err != nil {
		return nil, err
	}
	return newSessionManagerForDir(rt.Services(), rt.Session().inner.GetSessionDir()).Create(id, parent)
}

// SwitchSessionOptions is the options object of AgentSessionRuntime.switchSession (agent-session-runtime.ts:196-201).
type SwitchSessionOptions struct {
	// CwdOverride replaces the Session's stored working directory when that directory no longer exists.
	CwdOverride string
	// WithSession runs against the replaced Session's context once the replacement settles.
	WithSession func(*extension.ReplacedSessionContext) error
	// ProjectTrustContextFactory returns the project trust context of the destination cwd; its UI serves the trust prompt.
	ProjectTrustContextFactory func(cwd string) extension.ProjectTrustContext
}

// switchOptionsFromExtension carries the options an extension's switchSession call supplies; the extension API has no cwd override or trust factory.
func switchOptionsFromExtension(options *extension.SwitchSessionOptions) *SwitchSessionOptions {
	if options == nil {
		return nil
	}
	return &SwitchSessionOptions{WithSession: options.WithSession}
}

// processCWD is the directory SessionManager.open falls back to when the file has no header and no override (session-manager.ts open: `?? process.cwd()`); fallback stands in when the process directory is unreadable.
func processCWD(fallback string) string {
	if dir, err := nodepath.Cwd(); err == nil {
		return dir
	}
	return fallback
}

// SwitchSession loads and validates the destination before disposing the outgoing Session, then rebuilds its cwd-bound runtime (agent-session-runtime.ts:196-222).
func (rt *Runtime) SwitchSession(ctx context.Context, path string, options ...*SwitchSessionOptions) (extension.CancelledResult, error) {
	var opts SwitchSessionOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}
	cwdOverride, projectTrustContext := opts.CwdOverride, opts.ProjectTrustContextFactory
	before, err := rt.beforeSwitch(ctx, "resume", path)
	if err != nil || before.Cancelled {
		return before, err
	}
	manager, err := rt.openSwitchTarget(path, cwdOverride)
	if err != nil {
		return extension.CancelledResult{}, err
	}
	var trustContext *extension.ProjectTrustContext
	if projectTrustContext != nil {
		created := projectTrustContext(manager.GetCwd())
		trustContext = &created
	}
	if err := rt.replaceRuntime(ctx, manager, "resume", trustContext); err != nil {
		return extension.CancelledResult{}, err
	}
	return extension.CancelledResult{}, rt.finishSessionReplacement(ctx, opts.WithSession)
}

// openSwitchTarget loads the destination of a switch and checks that its cwd exists (SessionManager.open, assertSessionCwdExists).
func (rt *Runtime) openSwitchTarget(path, cwdOverride string) (*icodingagent.Session, error) {
	var override []string
	if cwdOverride != "" {
		override = append(override, cwdOverride)
	}
	manager, err := icodingagent.NewSessionManagerWithDir(processCWD(rt.CWD()), filepath.Dir(path)).Open(path, override...)
	if err != nil {
		return nil, err
	}
	if err := icodingagent.AssertSessionCwdExists(manager, rt.CWD()); err != nil {
		return nil, err
	}
	return manager, nil
}

// ValidateSwitchSession reports the error that switching to path would return from loading it, without running any handler or replacing anything.
// Pi's switchSession raises it before it waits for any replacement ahead of it. The loaded Session is discarded.
func (rt *Runtime) ValidateSwitchSession(path string) error {
	_, err := rt.openSwitchTarget(path, "")
	return err
}

// RuntimeForkResult retains selectedText presence: it is absent for position at and for a cancelled fork.
type RuntimeForkResult struct {
	Cancelled    bool    `json:"cancelled"`
	SelectedText *string `json:"selectedText,omitempty"`
}

// Fork creates a new Session containing the selected active branch. The default position is before a user message; at retains the selected entry itself.
type forkSessionKey struct{}

// WithForkSession makes a fork started with the returned context read its entry, and the Session file it branches, from session instead of the Runtime's current Session. An RPC input batch that requests a fork while an earlier replacement of the batch is still pending forks the Session that was current when the fork was requested, as Pi's does.
func WithForkSession(ctx context.Context, session *Session) context.Context {
	return context.WithValue(ctx, forkSessionKey{}, session)
}

func forkSessionOf(ctx context.Context) *Session {
	session, _ := ctx.Value(forkSessionKey{}).(*Session)
	return session
}

// forkTarget is the check Pi's fork makes against the current Session once the before handlers returned: the entry exists, and for position "before" it is a user message. It returns the leaf to branch at and the selected text.
func (s *Session) forkTarget(entryID, position string) (leaf, selected *string, err error) {
	entry, ok := s.inner.GetEntry(entryID)
	if !ok {
		return nil, nil, errors.New("Invalid entry ID for forking")
	}
	entryLeaf := entry.Base().ID
	leaf = &entryLeaf
	if position != "at" {
		message, ok := icodingagent.AsMessage(entry)
		if !ok || message.Message.User == nil {
			return nil, nil, errors.New("Invalid entry ID for forking")
		}
		leaf = entry.Base().ParentID
		selected = new(extractUserMessageText(message.Message.User.Content))
	}
	return leaf, selected, nil
}

// ValidateFork reports the error that forking at entryID would return from this Session without running any handler or replacing anything: an entry that does not exist or is not a user message for position "before", or a file-backed Session that has not been saved. A caller that dispatches a fork while another replacement is still in flight uses it to answer a request that Pi rejects before that replacement applies.
func (s *Session) ValidateFork(entryID, position string) error {
	if position == "" {
		position = "before"
	}
	if _, _, err := s.forkTarget(entryID, position); err != nil {
		return err
	}
	if s.inner.IsPersisted() {
		if _, err := os.Stat(s.inner.Path()); errors.Is(err, os.ErrNotExist) {
			return errors.New("This session has not been saved yet. Send a message before cloning or forking it.")
		}
	}
	return nil
}

func (rt *Runtime) Fork(ctx context.Context, entryID string, options *extension.ForkOptions) (RuntimeForkResult, error) {
	if rt.Session() == nil || rt.replacement.createRuntime == nil {
		return RuntimeForkResult{}, errors.New("coding: Runtime has no replacement factory")
	}
	// Pi's fork returns a Promise, so the caller's call lane advances after this synchronous start.
	extension.CallInitiated(ctx)
	position := "before"
	var withSession func(*extension.ReplacedSessionContext) error
	if options != nil {
		withSession = options.WithSession
		if options.Position != "" {
			position = options.Position
		}
	}
	if runner := rt.Session().currentRunner(); runner != nil {
		value, err := runner.Emit(ctx, extension.SessionBeforeForkEvent{Type: "session_before_fork", EntryID: entryID, Position: position})
		if err != nil {
			return RuntimeForkResult{}, err
		}
		cancelled := beforeResultCancelled(value)
		if cancelled {
			return RuntimeForkResult{Cancelled: true}, nil
		}
	}
	// Pi's fork reads the entry from the Session that was current when it was requested, also when an earlier replacement of the same batch applied meanwhile.
	forkFrom := rt.Session()
	if requested := forkSessionOf(ctx); requested != nil {
		forkFrom = requested
	}
	leaf, selected, err := forkFrom.forkTarget(entryID, position)
	if err != nil {
		return RuntimeForkResult{}, err
	}
	if source := forkFrom.inner; !source.IsPersisted() {
		outgoing := rt.Session()
		previous := source.Path()
		if err := rt.teardownCurrent(ctx, "fork", previous); err != nil {
			return RuntimeForkResult{}, err
		}
		if leaf == nil {
			if _, err := source.NewSession(&icodingagent.NewSessionOptions{ParentSession: previous}); err != nil {
				return RuntimeForkResult{}, err
			}
		} else if _, err := source.CreateBranchedSession(*leaf); err != nil {
			return RuntimeForkResult{}, err
		}
		if err := rt.createReplacement(ctx, source, "fork", previous, nil); err != nil {
			return RuntimeForkResult{}, err
		}
		// The replacement is already applied, so the hook runs even when the superseded Session's close fails.
		closeErr := outgoing.closeSuperseded(rt.Session())
		return RuntimeForkResult{SelectedText: selected}, errors.Join(closeErr, rt.finishSessionReplacement(ctx, withSession))
	}
	var manager *SessionManager
	if leaf == nil {
		manager, err = rt.freshSessionManager(forkFrom.Path())
	} else {
		source := forkFrom.inner
		storage := newSessionManagerForDir(rt.Services(), source.GetSessionDir())
		if source.IsPersisted() {
			if _, err := os.Stat(source.Path()); errors.Is(err, os.ErrNotExist) {
				return RuntimeForkResult{}, errors.New("This session has not been saved yet. Send a message before cloning or forking it.")
			}
			source, err = storage.Load(source.Path())
			if err != nil {
				return RuntimeForkResult{}, err
			}
		}
		manager, err = storage.Clone(source, *leaf)
	}
	if err != nil {
		return RuntimeForkResult{}, err
	}
	if err := rt.replaceRuntime(ctx, manager, "fork", nil); err != nil {
		return RuntimeForkResult{}, err
	}
	return RuntimeForkResult{SelectedText: selected}, rt.finishSessionReplacement(ctx, withSession)
}

// SessionImportFileNotFoundError identifies a missing import input before the active Session changes.
type SessionImportFileNotFoundError = icodingagent.SessionImportFileNotFoundError

// ImportFromJsonl copies an external log without overwriting an existing same-name Session, then replaces the active runtime.
// A non-empty cwdOverride replaces a stored working directory that no longer exists.
func (rt *Runtime) ImportFromJsonl(ctx context.Context, input string, cwdOverride ...string) (extension.CancelledResult, error) {
	path, err := icodingagent.ResolvePath(input, "")
	if err != nil {
		return extension.CancelledResult{}, err
	}
	if _, err := os.Stat(path); err != nil {
		return extension.CancelledResult{}, &SessionImportFileNotFoundError{FilePath: path}
	}
	dir := rt.Session().inner.GetSessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return extension.CancelledResult{}, err
	}
	destination := filepath.Join(dir, filepath.Base(path))
	alreadyStored := destination == path
	if !alreadyStored {
		ext := filepath.Ext(destination)
		name := strings.TrimSuffix(filepath.Base(destination), ext)
		for suffix := 1; ; suffix++ {
			if _, err := os.Stat(destination); errors.Is(err, os.ErrNotExist) {
				break
			} else if err != nil {
				return extension.CancelledResult{}, err
			}
			destination = filepath.Join(dir, fmt.Sprintf("%s-%d%s", name, suffix, ext))
		}
	}
	before, err := rt.beforeSwitch(ctx, "resume", destination)
	if err != nil || before.Cancelled {
		return before, err
	}
	if !alreadyStored {
		if err := copyFileExclusive(path, destination); err != nil {
			return extension.CancelledResult{}, err
		}
	}
	manager, err := icodingagent.NewSessionManagerWithDir(processCWD(rt.CWD()), dir).Open(destination, cwdOverride...)
	if err != nil {
		return extension.CancelledResult{}, err
	}
	if err := icodingagent.AssertSessionCwdExists(manager, rt.CWD()); err != nil {
		return extension.CancelledResult{}, err
	}
	if err := rt.replaceRuntime(ctx, manager, "resume", nil); err != nil {
		return extension.CancelledResult{}, err
	}
	return extension.CancelledResult{}, rt.finishSessionReplacement(ctx, nil)
}

// SessionManagerFor returns the log a Session started with opts wraps, as NewSession selects it: the supplied manager, the opened resume path, or a new persisted or in-memory log. CreateAgentSessionRuntime needs the log before the factory runs.
func SessionManagerFor(svcs *AgentSessionServices, opts SessionStartOptions) (*SessionManager, error) {
	if opts.SessionManager != nil {
		return opts.SessionManager, nil
	}
	storage := newSessionManagerForDir(svcs, opts.SessionDir)
	if opts.ResumePath != "" {
		var override []string
		if opts.CWDOverride != nil {
			override = []string{*opts.CWDOverride}
		}
		manager, err := storage.Open(opts.ResumePath, override...)
		if err != nil {
			return nil, fmt.Errorf("coding.SessionManagerFor: resume %s: %w", opts.ResumePath, err)
		}
		return manager, nil
	}
	id := opts.SessionID
	if id == "" {
		var err error
		if id, err = icodingagent.GenerateSessionID(); err != nil {
			return nil, fmt.Errorf("coding.SessionManagerFor: gen session id: %w", err)
		}
	}
	if opts.NoSession {
		return icodingagent.NewSession(id, svcs.CWD()), nil
	}
	return storage.Create(id, "")
}
