package coding

// Ports packages/coding-agent/src/core/agent-session-runtime.ts.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// CreateAgentSessionRuntimeOptions selects the destination log and cwd-bound factory inputs.
type CreateAgentSessionRuntimeOptions struct {
	CWD               string
	AgentDir          string
	SessionManager    *SessionManager
	SessionStartEvent *extension.SessionStartEvent
	// ProjectTrustUI is the UI a project trust prompt for CWD uses. Pi's projectTrustContext (main.ts createRuntime) carries it for a replacement that the interactive mode drives; the factory falls back to a headless context without it.
	ProjectTrustUI extension.UIContext
}

// CreateAgentSessionRuntimeResult contains the constructed Session and its Services.
type CreateAgentSessionRuntimeResult struct {
	Session  *Session
	Services *Services
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
	// drain settles mode-owned work of the outgoing Session after cancellable before hooks approved the replacement and before the Session aborts and shuts down.
	drain func(context.Context) error
}

// CreateAgentSessionRuntime constructs the first Session and retains the same factory for later replacements. The caller binds extensions after subscribing to Session events.
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
	abortCtx, abort := context.WithCancel(ctx)
	runtime := &Runtime{
		replacement: runtimeReplacement{createRuntime: factory},
		extCtx:      &icodingagent.ExtensionContext{CWD: options.CWD, AbortSignal: abortCtx, AbortFunc: abort},
	}
	if err := runtime.applyRuntime(result, options.SessionStartEvent); err != nil {
		return nil, err
	}
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
		return rt.SwitchSession(ctx, path, options)
	}
	bound.SwitchSession = func(path string, options *extension.SwitchSessionOptions) (extension.CancelledResult, error) {
		return rt.SwitchSession(context.Background(), path, options)
	}
	return bound
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
	err := session.Close()
	current.retireResources(reason)
	return err
}

func (rt *Runtime) replaceRuntime(ctx context.Context, manager *SessionManager, reason string, trustUI extension.UIContext) error {
	previous := rt.Session().Path()
	if err := rt.teardownCurrent(ctx, reason, manager.Path()); err != nil {
		return err
	}
	return rt.createReplacement(ctx, manager, reason, previous, trustUI)
}

func (rt *Runtime) createReplacement(ctx context.Context, manager *SessionManager, reason, previous string, trustUI extension.UIContext) error {
	agentDir := rt.Services().AgentDir()
	event := &extension.SessionStartEvent{Type: "session_start", Reason: reason, PreviousSessionFile: previous}
	result, err := rt.replacement.createRuntime(ctx, CreateAgentSessionRuntimeOptions{CWD: manager.GetCwd(), AgentDir: agentDir, SessionManager: manager, SessionStartEvent: event, ProjectTrustUI: trustUI})
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
			err = manager.NewSession(parent)
		}
		return manager, err
	}
	id, err := icodingagent.GenerateSessionID()
	if err != nil {
		return nil, err
	}
	return newSessionManagerForDir(rt.Services(), rt.Session().inner.GetSessionDir()).Create(id, parent)
}

// SwitchSession loads and validates the destination before disposing the outgoing Session, then rebuilds its cwd-bound runtime.
func (rt *Runtime) SwitchSession(ctx context.Context, path string, options ...*extension.SwitchSessionOptions) (extension.CancelledResult, error) {
	return rt.SwitchSessionWithCWD(ctx, path, "", options...)
}

// SwitchSessionWithCWD is SwitchSession with a working directory that replaces the Session's stored one when that directory no longer exists, as Pi's switchSession cwdOverride does.
func (rt *Runtime) SwitchSessionWithCWD(ctx context.Context, path, cwdOverride string, options ...*extension.SwitchSessionOptions) (extension.CancelledResult, error) {
	return rt.SwitchSessionWithProjectTrust(ctx, path, cwdOverride, nil, options...)
}

// SwitchSessionWithProjectTrust is SwitchSessionWithCWD with Pi's projectTrustContextFactory (agent-session-runtime.ts:196-222): projectTrustUI receives the destination cwd and returns the UI the replacement's project trust prompt uses. A nil factory keeps the headless trust context.
func (rt *Runtime) SwitchSessionWithProjectTrust(ctx context.Context, path, cwdOverride string, projectTrustUI func(cwd string) extension.UIContext, options ...*extension.SwitchSessionOptions) (extension.CancelledResult, error) {
	before, err := rt.beforeSwitch(ctx, "resume", path)
	if err != nil || before.Cancelled {
		return before, err
	}
	var override []string
	if cwdOverride != "" {
		override = append(override, cwdOverride)
	}
	manager, err := icodingagent.NewSessionManagerWithDir(rt.CWD(), filepath.Dir(path)).Open(path, override...)
	if err != nil {
		return extension.CancelledResult{}, err
	}
	if err := icodingagent.AssertSessionCwdExists(manager, rt.CWD()); err != nil {
		return extension.CancelledResult{}, err
	}
	var trustUI extension.UIContext
	if projectTrustUI != nil {
		trustUI = projectTrustUI(manager.GetCwd())
	}
	if err := rt.replaceRuntime(ctx, manager, "resume", trustUI); err != nil {
		return extension.CancelledResult{}, err
	}
	var withSession func(*extension.ReplacedSessionContext) error
	if len(options) > 0 && options[0] != nil {
		withSession = options[0].WithSession
	}
	return extension.CancelledResult{}, rt.finishSessionReplacement(ctx, withSession)
}

// RuntimeForkResult retains selectedText presence: it is absent for position at and for a cancelled fork.
type RuntimeForkResult struct {
	Cancelled    bool    `json:"cancelled"`
	SelectedText *string `json:"selectedText,omitempty"`
}

// Fork creates a new Session containing the selected active branch. The default position is before a user message; at retains the selected entry itself.
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
	entry, ok := rt.Session().inner.EntryByID(entryID)
	if !ok {
		return RuntimeForkResult{}, errors.New("Invalid entry ID for forking")
	}
	leaf := &entry.Base.ID
	var selected *string
	if position != "at" {
		message, ok := entry.AsMessage()
		if !ok || message.Message.User == nil {
			return RuntimeForkResult{}, errors.New("Invalid entry ID for forking")
		}
		leaf = entry.Base.ParentID
		selected = new(extractUserMessageText(message.Message.User.Content))
	}
	if source := rt.Session().inner; !source.IsPersisted() {
		previous := source.Path()
		if err := rt.teardownCurrent(ctx, "fork", previous); err != nil {
			return RuntimeForkResult{}, err
		}
		if leaf == nil {
			if err := source.NewSession(previous); err != nil {
				return RuntimeForkResult{}, err
			}
		} else if _, err := source.CreateBranchedSession(*leaf); err != nil {
			return RuntimeForkResult{}, err
		}
		if err := rt.createReplacement(ctx, source, "fork", previous, nil); err != nil {
			return RuntimeForkResult{}, err
		}
		return RuntimeForkResult{SelectedText: selected}, rt.finishSessionReplacement(ctx, withSession)
	}
	var manager *SessionManager
	var err error
	if leaf == nil {
		manager, err = rt.freshSessionManager(rt.Session().Path())
	} else {
		source := rt.Session().inner
		storage := newSessionManagerForDir(rt.Services(), source.GetSessionDir())
		if source.IsPersisted() {
			if _, err := os.Stat(source.Path()); errors.Is(err, os.ErrNotExist) {
				return RuntimeForkResult{}, errors.New("This session has not been saved yet. Wait for the first assistant response before cloning or forking it.")
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
		if err := copyRuntimeImport(path, destination); err != nil {
			return extension.CancelledResult{}, err
		}
	}
	manager, err := icodingagent.NewSessionManagerWithDir(rt.CWD(), dir).Open(destination, cwdOverride...)
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

func copyRuntimeImport(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	modeErr := output.Chmod(info.Mode().Perm())
	err = errors.Join(copyErr, modeErr, output.Close())
	if err != nil {
		if cleanupErr := os.Remove(destination); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
			return errors.Join(err, cleanupErr)
		}
	}
	return err
}

// SessionManagerFor returns the log a Session started with opts wraps, as NewSession selects it: the supplied manager, the opened resume path, or a new persisted or in-memory log. CreateAgentSessionRuntime needs the log before the factory runs.
func SessionManagerFor(svcs *Services, opts SessionStartOptions) (*SessionManager, error) {
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
