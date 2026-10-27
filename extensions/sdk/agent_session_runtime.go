package sdk

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Pi's createAgentSessionRuntime and AgentSessionRuntime (core/agent-session-runtime.ts). The runtime owns the current AgentSession and its cwd-bound services; the factory that builds them stays in this process and is called on every session replacement, as Pi's is. The host answers the calls the
// runtime makes on the session it replaces: the session_before_switch, session_before_fork and session_shutdown events through the session's extensions, refreshContext and the session's log.

// CreateAgentSessionRuntimeResult is Pi's CreateAgentSessionRuntimeResult: the session the factory created, its cwd-bound services and the diagnostics collected while setting up.
type CreateAgentSessionRuntimeResult struct {
	Session  *AgentSession
	Services *AgentSessionServices
	// Diagnostics are the non-fatal issues found while creating the services and the session.
	Diagnostics []AgentSessionRuntimeDiagnostic
	// ModelFallbackMessage is Pi's `modelFallbackMessage`; empty is Pi's undefined.
	ModelFallbackMessage string
}

// CreateAgentSessionRuntimeFactoryOptions is the argument of Pi's CreateAgentSessionRuntimeFactory: the target of the session to build. Pi's `projectTrustContext` (a record of the host's UI functions) is not passed: see [SwitchSessionRuntimeOptions].
type CreateAgentSessionRuntimeFactoryOptions struct {
	Cwd               string
	AgentDir          string
	SessionManager    SessionManager
	SessionStartEvent *SessionStartEvent
}

// CreateAgentSessionRuntimeFactory is Pi's CreateAgentSessionRuntimeFactory: it builds the services for the effective cwd, resolves the session options against them and creates the AgentSession over options.SessionManager.
type CreateAgentSessionRuntimeFactory func(options CreateAgentSessionRuntimeFactoryOptions) (*CreateAgentSessionRuntimeResult, error)

// CreateAgentSessionRuntimeOptions is the second argument of Pi's createAgentSessionRuntime.
type CreateAgentSessionRuntimeOptions struct {
	Cwd               string
	AgentDir          string
	SessionManager    SessionManager
	SessionStartEvent *SessionStartEvent
}

// SessionImportFileNotFoundError is Pi's SessionImportFileNotFoundError: ImportFromJsonl named a file that does not exist.
type SessionImportFileNotFoundError struct{ FilePath string }

func (e *SessionImportFileNotFoundError) Error() string { return "File not found: " + e.FilePath }

// MissingSessionCwdError is Pi's MissingSessionCwdError: the working directory a session file stored does not exist.
type MissingSessionCwdError struct {
	SessionFile string
	SessionCwd  string
	FallbackCwd string
}

func (e *MissingSessionCwdError) Error() string {
	file := ""
	if e.SessionFile != "" {
		file = "\nSession file: " + e.SessionFile
	}
	return fmt.Sprintf("Stored session working directory does not exist: %s%s\nCurrent working directory: %s", e.SessionCwd, file, e.FallbackCwd)
}

// withSessionUnsupported is the error of the `withSession` option: its callback takes Pi's ReplacedSessionContext, which AgentSession.createReplacedSessionContext makes, and that member is not served yet (gap C2Sessions in test/parity/sdk-exports.toml).
var errWithSessionUnsupported = errors.New("the withSession option is not supported yet: it takes the ReplacedSessionContext of AgentSession.createReplacedSessionContext, which is a C2Sessions gap in test/parity/sdk-exports.toml; use the setup option, or read AgentSessionRuntime.Session() once the call returned")

// NewSessionRuntimeOptions is the options object of Pi's AgentSessionRuntime.newSession. Pi's `withSession` is not served ([errWithSessionUnsupported]); a function in it fails the call before anything happens.
type NewSessionRuntimeOptions struct {
	ParentSession string
	// Setup runs with the new session's log before the session reads it; the session's context is refreshed after it returns.
	Setup func(SessionManager) error
	// WithSession is Pi's `withSession`: not served yet; a non-nil value fails the call.
	WithSession func()
}

// ForkRuntimeOptions is the options object of Pi's AgentSessionRuntime.fork: Position is "before" (the default) or "at".
type ForkRuntimeOptions struct {
	Position string
	// WithSession is Pi's `withSession`: not served yet; a non-nil value fails the call.
	WithSession func()
}

// ForkResult is the result of Pi's AgentSessionRuntime.fork.
type ForkResult struct {
	Cancelled bool
	// SelectedText is the text of the user message that was forked before; nil for position "at" and when cancelled.
	SelectedText *string
}

// SwitchSessionRuntimeOptions is the options object of Pi's AgentSessionRuntime.switchSession. Pi's `projectTrustContextFactory` builds a record of the host's UI functions, which cannot cross the process boundary: the factory is not served, and it is not a member here.
type SwitchSessionRuntimeOptions struct {
	// CwdOverride is the working directory to use when the stored one is gone.
	CwdOverride string
	// WithSession is Pi's `withSession`: not served yet; a non-nil value fails the call.
	WithSession func()
}

// AgentSessionRuntime is Pi's AgentSessionRuntime: it owns the current AgentSession plus its cwd-bound services. The replacement methods tear the current runtime down first, then create and apply the next one; a failing factory's error is returned and the caller handles it.
type AgentSessionRuntime struct {
	context Context
	create  CreateAgentSessionRuntimeFactory

	mu                      sync.Mutex
	session                 *AgentSession
	services                *AgentSessionServices
	diagnostics             []AgentSessionRuntimeDiagnostic
	modelFallbackMessage    string
	rebindSession           func(*AgentSession) error
	beforeSessionInvalidate func()
}

// CreateAgentSessionRuntime is `createAgentSessionRuntime(createRuntime, options)`: it builds the initial runtime from the factory and keeps the factory for the replacements.
func CreateAgentSessionRuntime(c Context, create CreateAgentSessionRuntimeFactory, options CreateAgentSessionRuntimeOptions) (*AgentSessionRuntime, error) {
	if err := assertSessionCwdExists(options.SessionManager, options.Cwd); err != nil {
		return nil, err
	}
	result, err := create(CreateAgentSessionRuntimeFactoryOptions(options))
	if err != nil {
		return nil, err
	}
	runtime := &AgentSessionRuntime{context: c, create: create}
	runtime.apply(result)
	return runtime, nil
}

func (r *AgentSessionRuntime) apply(result *CreateAgentSessionRuntimeResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.session, r.services, r.diagnostics, r.modelFallbackMessage = result.Session, result.Services, result.Diagnostics, result.ModelFallbackMessage
}

// Session is `runtime.session`: the current session.
func (r *AgentSessionRuntime) Session() *AgentSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.session
}

// Services is `runtime.services`: the current cwd-bound services.
func (r *AgentSessionRuntime) Services() *AgentSessionServices {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.services
}

// Cwd is `runtime.cwd`: the working directory of the current services.
func (r *AgentSessionRuntime) Cwd() string { return r.Services().Cwd }

// Diagnostics is `runtime.diagnostics`.
func (r *AgentSessionRuntime) Diagnostics() []AgentSessionRuntimeDiagnostic {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.diagnostics
}

// ModelFallbackMessage is `runtime.modelFallbackMessage`; empty is Pi's undefined.
func (r *AgentSessionRuntime) ModelFallbackMessage() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.modelFallbackMessage
}

// SetRebindSession is `setRebindSession(rebindSession?)`: the function runs with the new session after every replacement. Nil removes it.
func (r *AgentSessionRuntime) SetRebindSession(rebind func(*AgentSession) error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rebindSession = rebind
}

// SetBeforeSessionInvalidate is `setBeforeSessionInvalidate(callback?)`: the function runs after the current session's session_shutdown handlers finished and before the session is disposed. Nil removes it.
func (r *AgentSessionRuntime) SetBeforeSessionInvalidate(callback func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.beforeSessionInvalidate = callback
}

// emitBeforeReplace is emitBeforeSwitch and emitBeforeFork: `hasHandlers(type)` then `emit(event)`, and whether a handler cancelled.
func (r *AgentSessionRuntime) emitBeforeReplace(event map[string]any) (bool, error) {
	result, emitted, err := r.Session().emitExtensionEvent(event)
	if err != nil || !emitted {
		return false, err
	}
	var answer struct {
		Cancel bool `json:"cancel"`
	}
	if len(result) != 0 && string(result) != "null" {
		if err := json.Unmarshal(result, &answer); err != nil {
			return false, err
		}
	}
	return answer.Cancel, nil
}

func (r *AgentSessionRuntime) emitBeforeSwitch(reason, targetSessionFile string) (bool, error) {
	event := map[string]any{"type": "session_before_switch", "reason": reason}
	if targetSessionFile != "" {
		event["targetSessionFile"] = targetSessionFile
	}
	return r.emitBeforeReplace(event)
}

// teardownCurrent settles the active response so its aborted turn is persisted to the outgoing session, emits session_shutdown, runs the before-invalidate callback and disposes the session.
func (r *AgentSessionRuntime) teardownCurrent(reason, targetSessionFile string) error {
	session := r.Session()
	if err := session.Abort(); err != nil {
		return err
	}
	event := map[string]any{"type": "session_shutdown", "reason": reason}
	if targetSessionFile != "" {
		event["targetSessionFile"] = targetSessionFile
	}
	if _, _, err := session.emitExtensionEvent(event); err != nil {
		return err
	}
	r.mu.Lock()
	callback := r.beforeSessionInvalidate
	r.mu.Unlock()
	if callback != nil {
		callback()
	}
	session.Dispose()
	return nil
}

func (r *AgentSessionRuntime) finishSessionReplacement() error {
	r.mu.Lock()
	rebind, session := r.rebindSession, r.session
	r.mu.Unlock()
	if rebind != nil {
		return rebind(session)
	}
	return nil
}

// replace creates the next runtime with the factory and applies it.
func (r *AgentSessionRuntime) replace(cwd string, manager SessionManager, reason, previousSessionFile string) error {
	result, err := r.create(CreateAgentSessionRuntimeFactoryOptions{
		Cwd: cwd, AgentDir: r.Services().AgentDir, SessionManager: manager,
		SessionStartEvent: &SessionStartEvent{Reason: reason, PreviousSessionFile: previousSessionFile},
	})
	if err != nil {
		return err
	}
	r.apply(result)
	return nil
}

func sessionFileOf(manager SessionManager) (string, error) {
	file, err := manager.GetSessionFile()
	if err != nil || file == nil {
		return "", err
	}
	return *file, nil
}

// assertSessionCwdExists is Pi's assertSessionCwdExists: a persisted session whose stored working directory is gone fails with a MissingSessionCwdError.
func assertSessionCwdExists(manager SessionManager, fallbackCwd string) error {
	file, err := sessionFileOf(manager)
	if err != nil || file == "" {
		return err
	}
	cwd, err := manager.GetCwd()
	if err != nil || cwd == "" {
		return err
	}
	if _, statErr := os.Stat(cwd); statErr == nil {
		return nil
	}
	return &MissingSessionCwdError{SessionFile: file, SessionCwd: cwd, FallbackCwd: fallbackCwd}
}

// SwitchSession is `switchSession(sessionPath, options?)`.
func (r *AgentSessionRuntime) SwitchSession(sessionPath string, options *SwitchSessionRuntimeOptions) (CancelledResult, error) {
	if options != nil && options.WithSession != nil {
		return CancelledResult{}, errWithSessionUnsupported
	}
	cancelled, err := r.emitBeforeSwitch("resume", sessionPath)
	if err != nil || cancelled {
		return CancelledResult{Cancelled: cancelled}, err
	}
	previous, err := currentSessionFile(r)
	if err != nil {
		return CancelledResult{}, err
	}
	var cwdOverride *string
	if options != nil && options.CwdOverride != "" {
		cwdOverride = &options.CwdOverride
	}
	manager, err := SessionManagerOpen(r.context, sessionPath, nil, cwdOverride)
	if err != nil {
		return CancelledResult{}, err
	}
	if err := assertSessionCwdExists(manager, r.Cwd()); err != nil {
		return CancelledResult{}, err
	}
	target, err := sessionFileOf(manager)
	if err != nil {
		return CancelledResult{}, err
	}
	if err := r.teardownCurrent("resume", target); err != nil {
		return CancelledResult{}, err
	}
	cwd, err := manager.GetCwd()
	if err != nil {
		return CancelledResult{}, err
	}
	if err := r.replace(cwd, manager, "resume", previous); err != nil {
		return CancelledResult{}, err
	}
	return CancelledResult{}, r.finishSessionReplacement()
}

// currentSessionFile is `session.sessionFile`: the file of the log the current session appends to, empty for an in-memory session.
func currentSessionFile(r *AgentSessionRuntime) (string, error) {
	manager, err := r.Session().SessionManager()
	if err != nil {
		return "", err
	}
	return sessionFileOf(manager)
}

// NewSession is `newSession(options?)`.
func (r *AgentSessionRuntime) NewSession(options *NewSessionRuntimeOptions) (CancelledResult, error) {
	if options != nil && options.WithSession != nil {
		return CancelledResult{}, errWithSessionUnsupported
	}
	cancelled, err := r.emitBeforeSwitch("new", "")
	if err != nil || cancelled {
		return CancelledResult{Cancelled: cancelled}, err
	}
	previous, err := currentSessionFile(r)
	if err != nil {
		return CancelledResult{}, err
	}
	current, err := r.Session().SessionManager()
	if err != nil {
		return CancelledResult{}, err
	}
	sessionDir, err := current.GetSessionDir()
	if err != nil {
		return CancelledResult{}, err
	}
	persisted, err := current.IsPersisted()
	if err != nil {
		return CancelledResult{}, err
	}
	var manager SessionManager
	if persisted {
		manager, err = SessionManagerCreate(r.context, r.Cwd(), &sessionDir, nil)
	} else {
		manager, err = SessionManagerInMemory(r.context, r.Cwd(), nil, nil)
	}
	if err != nil {
		return CancelledResult{}, err
	}
	if options != nil && options.ParentSession != "" {
		if _, err := manager.NewSession(&NewSessionOptions{ParentSession: &options.ParentSession}); err != nil {
			return CancelledResult{}, err
		}
	}
	target, err := sessionFileOf(manager)
	if err != nil {
		return CancelledResult{}, err
	}
	if err := r.teardownCurrent("new", target); err != nil {
		return CancelledResult{}, err
	}
	if err := r.replace(r.Cwd(), manager, "new", previous); err != nil {
		return CancelledResult{}, err
	}
	if options != nil && options.Setup != nil {
		next, err := r.Session().SessionManager()
		if err != nil {
			return CancelledResult{}, err
		}
		if err := options.Setup(next); err != nil {
			return CancelledResult{}, err
		}
		if err := r.Session().RefreshContext(); err != nil {
			return CancelledResult{}, err
		}
	}
	return CancelledResult{}, r.finishSessionReplacement()
}

// userMessageText is extractUserMessageText: a string, or the text parts of a content array joined.
func userMessageText(content any) string {
	if text, ok := content.(string); ok {
		return text
	}
	var out strings.Builder
	if parts, ok := content.([]any); ok {
		for _, part := range parts {
			if block, ok := part.(map[string]any); ok && block["type"] == "text" {
				if text, ok := block["text"].(string); ok {
					out.WriteString(text)
				}
			}
		}
	}
	return out.String()
}

// Fork is `fork(entryId, options?)`.
func (r *AgentSessionRuntime) Fork(entryID string, options *ForkRuntimeOptions) (ForkResult, error) {
	if options != nil && options.WithSession != nil {
		return ForkResult{}, errWithSessionUnsupported
	}
	position := "before"
	if options != nil && options.Position != "" {
		position = options.Position
	}
	cancelled, err := r.emitBeforeReplace(map[string]any{"type": "session_before_fork", "entryId": entryID, "position": position})
	if err != nil || cancelled {
		return ForkResult{Cancelled: cancelled}, err
	}
	current, err := r.Session().SessionManager()
	if err != nil {
		return ForkResult{}, err
	}
	var targetLeafID *string
	var selectedText *string
	entry, err := current.GetEntry(entryID)
	if err != nil {
		return ForkResult{}, err
	}
	if entry == nil {
		return ForkResult{}, errors.New("Invalid entry ID for forking")
	}
	if position == "at" {
		id, _ := entry["id"].(string)
		targetLeafID = &id
	} else {
		message, _ := entry["message"].(map[string]any)
		if entry["type"] != "message" || message["role"] != "user" {
			return ForkResult{}, errors.New("Invalid entry ID for forking")
		}
		if parent, ok := entry["parentId"].(string); ok {
			targetLeafID = &parent
		}
		text := userMessageText(message["content"])
		selectedText = &text
	}
	previous, err := sessionFileOf(current)
	if err != nil {
		return ForkResult{}, err
	}
	finish := func(manager SessionManager, cwd string) (ForkResult, error) {
		if err := r.replace(cwd, manager, "fork", previous); err != nil {
			return ForkResult{}, err
		}
		return ForkResult{SelectedText: selectedText}, r.finishSessionReplacement()
	}
	persisted, err := current.IsPersisted()
	if err != nil {
		return ForkResult{}, err
	}
	if persisted {
		if previous == "" {
			return ForkResult{}, errors.New("Persisted session is missing a session file")
		}
		sessionDir, err := current.GetSessionDir()
		if err != nil {
			return ForkResult{}, err
		}
		if targetLeafID == nil {
			manager, err := SessionManagerCreate(r.context, r.Cwd(), &sessionDir, nil)
			if err != nil {
				return ForkResult{}, err
			}
			if _, err := manager.NewSession(&NewSessionOptions{ParentSession: &previous}); err != nil {
				return ForkResult{}, err
			}
			target, err := sessionFileOf(manager)
			if err != nil {
				return ForkResult{}, err
			}
			if err := r.teardownCurrent("fork", target); err != nil {
				return ForkResult{}, err
			}
			return finish(manager, r.Cwd())
		}
		if _, err := os.Stat(previous); err != nil {
			return ForkResult{}, errors.New("This session has not been saved yet. Send a message before cloning or forking it.")
		}
		manager, err := SessionManagerOpen(r.context, previous, &sessionDir, nil)
		if err != nil {
			return ForkResult{}, err
		}
		forked, err := manager.CreateBranchedSession(*targetLeafID)
		if err != nil {
			return ForkResult{}, err
		}
		if forked == nil {
			return ForkResult{}, errors.New("Failed to create forked session")
		}
		target, err := sessionFileOf(manager)
		if err != nil {
			return ForkResult{}, err
		}
		if err := r.teardownCurrent("fork", target); err != nil {
			return ForkResult{}, err
		}
		cwd, err := manager.GetCwd()
		if err != nil {
			return ForkResult{}, err
		}
		return finish(manager, cwd)
	}
	target, err := sessionFileOf(current)
	if err != nil {
		return ForkResult{}, err
	}
	if err := r.teardownCurrent("fork", target); err != nil {
		return ForkResult{}, err
	}
	if targetLeafID == nil {
		var parent *string
		if previous != "" {
			parent = &previous
		}
		if _, err := current.NewSession(&NewSessionOptions{ParentSession: parent}); err != nil {
			return ForkResult{}, err
		}
	} else if _, err := current.CreateBranchedSession(*targetLeafID); err != nil {
		return ForkResult{}, err
	}
	return finish(current, r.Cwd())
}

// resolveImportPath is resolvePath(input): `~` is the home directory and a relative path is relative to the process's working directory.
func resolveImportPath(input string) (string, error) {
	path := strings.TrimSpace(input)
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	return filepath.Abs(path)
}

// ImportFromJsonl is `importFromJsonl(inputPath, cwdOverride?)`: it copies the session file into the current session directory (unless it is already there) and switches to it. The file's absence is a [SessionImportFileNotFoundError].
func (r *AgentSessionRuntime) ImportFromJsonl(inputPath string, cwdOverride *string) (CancelledResult, error) {
	resolved, err := resolveImportPath(inputPath)
	if err != nil {
		return CancelledResult{}, err
	}
	if _, err := os.Stat(resolved); err != nil {
		return CancelledResult{}, &SessionImportFileNotFoundError{FilePath: resolved}
	}
	current, err := r.Session().SessionManager()
	if err != nil {
		return CancelledResult{}, err
	}
	sessionDir, err := current.GetSessionDir()
	if err != nil {
		return CancelledResult{}, err
	}
	if _, err := os.Stat(sessionDir); err != nil {
		if err := os.MkdirAll(sessionDir, 0o777); err != nil {
			return CancelledResult{}, err
		}
	}
	destination := filepath.Join(sessionDir, filepath.Base(resolved))
	absDestination, _ := filepath.Abs(destination)
	alreadyStored := absDestination == resolved
	if !alreadyStored {
		ext := filepath.Ext(destination)
		name := strings.TrimSuffix(filepath.Base(destination), ext)
		for suffix := 1; ; suffix++ {
			if _, err := os.Stat(destination); err != nil {
				break
			}
			destination = filepath.Join(sessionDir, fmt.Sprintf("%s-%d%s", name, suffix, ext))
		}
	}
	cancelled, err := r.emitBeforeSwitch("resume", destination)
	if err != nil || cancelled {
		return CancelledResult{Cancelled: cancelled}, err
	}
	previous, err := currentSessionFile(r)
	if err != nil {
		return CancelledResult{}, err
	}
	if !alreadyStored {
		if err := copyFileExclusive(resolved, destination); err != nil {
			return CancelledResult{}, err
		}
	}
	manager, err := SessionManagerOpen(r.context, destination, &sessionDir, cwdOverride)
	if err != nil {
		return CancelledResult{}, err
	}
	if err := assertSessionCwdExists(manager, r.Cwd()); err != nil {
		return CancelledResult{}, err
	}
	target, err := sessionFileOf(manager)
	if err != nil {
		return CancelledResult{}, err
	}
	if err := r.teardownCurrent("resume", target); err != nil {
		return CancelledResult{}, err
	}
	cwd, err := manager.GetCwd()
	if err != nil {
		return CancelledResult{}, err
	}
	if err := r.replace(cwd, manager, "resume", previous); err != nil {
		return CancelledResult{}, err
	}
	return CancelledResult{}, r.finishSessionReplacement()
}

// copyFileExclusive is copyFileSync(source, destination, COPYFILE_EXCL).
func copyFileExclusive(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// Dispose is `dispose()`: it emits session_shutdown with the reason "quit" to the current session's extensions, runs the before-invalidate callback and disposes the session.
func (r *AgentSessionRuntime) Dispose() error {
	session := r.Session()
	if _, _, err := session.emitExtensionEvent(map[string]any{"type": "session_shutdown", "reason": "quit"}); err != nil {
		return err
	}
	r.mu.Lock()
	callback := r.beforeSessionInvalidate
	r.mu.Unlock()
	if callback != nil {
		callback()
	}
	session.Dispose()
	return nil
}
