// Ports packages/coding-agent/src/modes/interactive/components/session-selector.ts.
package codingagent

import (
	"context"
	"slices"
	"sync"
)

// SessionsLoader is session-selector.ts:649 SessionsLoader: the awaited Promise is the blocking call, the AbortSignal is ctx, and onProgress receives partial listings.
type SessionsLoader func(ctx context.Context, onProgress SessionListProgress) ([]SessionInfo, error)

// SessionSelectorOptions is the `options` object of session-selector.ts:764-768. RenameSession is the rename operation (a nil one disables
// renaming); ShowRenameHint defaults to whether renaming is possible; Keybindings defaults to the configured manager. DeleteSession is PiG's
// guard on the delete operation and defaults to deleteSessionFile (session-selector.ts:826).
type SessionSelectorOptions struct {
	RenameSession  func(sessionPath, name string) error
	ShowRenameHint *bool
	Keybindings    *KeybindingsManager
	DeleteSession  func(sessionPath string) sessionDeleteResult
}

// NewSessionSelectorComponent is the constructor of session-selector.ts:757-770. onSelect receives the chosen session path, onCancel runs on the
// cancel key, onExit is the host's exit hook, and requestRender repaints after an asynchronous change. Both loaders start on demand: the current
// scope loads at once, the all scope the first time it is shown. A trailing currentSessionFilePath marks the current session.
func NewSessionSelectorComponent(currentSessionsLoader, allSessionsLoader SessionsLoader, onSelect func(sessionPath string), onCancel, onExit, requestRender func(), options *SessionSelectorOptions, currentSessionFilePath ...string) *SessionSelectorComponent {
	var opts SessionSelectorOptions
	if options != nil {
		opts = *options
	}
	deleteSession := opts.DeleteSession
	if deleteSession == nil {
		deleteSession = deleteSessionFile
	}
	adapt := func(loader SessionsLoader) func(SessionListOptions) ([]SessionInfo, error) {
		return func(o SessionListOptions) ([]SessionInfo, error) { return loader(o.Context, o.OnProgress) }
	}
	currentPath := ""
	if len(currentSessionFilePath) > 0 {
		currentPath = currentSessionFilePath[0]
	}
	s := newSessionSelectorFromListers(adapt(currentSessionsLoader), adapt(allSessionsLoader), opts.RenameSession, deleteSession, currentPath, opts.Keybindings)
	list := s.GetSessionList()
	list.OnSelect, list.OnCancel, list.OnExit = onSelect, onCancel, onExit
	s.requestRender = requestRender
	s.showRenameHint = opts.RenameSession != nil
	if opts.ShowRenameHint != nil {
		s.showRenameHint = *opts.ShowRenameHint
	}
	return s
}

// newSessionSelectorFromListers builds a selector over SessionListOptions listers with no callbacks, for hosts that poll Done and SelectedPath.
func newSessionSelectorFromListers(currentLoader, allLoader func(SessionListOptions) ([]SessionInfo, error), renameSession func(string, string) error, deleteSession func(string) sessionDeleteResult, currentPath string, kb *KeybindingsManager) *SessionSelectorComponent {
	return newSessionSelectorWithLoaders(asyncSessionsLoader(currentLoader), asyncSessionsLoader(allLoader), renameSession, deleteSession, currentPath, kb)
}

// sessionsLoader starts on the UI owner. Its result is the awaited Promise; a loader may publish progress before returning it. Blocking I/O runs through sessionLoad.run, and its progress returns through post.
type sessionsLoader func(*sessionLoad, SessionListProgress) <-chan sessionLoadResult
type sessionLoadResult struct {
	sessions []SessionInfo
	err      error
}
type sessionLoad struct {
	ctx    context.Context
	cancel context.CancelFunc
	result <-chan sessionLoadResult
	owner  *SessionSelectorComponent
	// complete runs on the selector owner after settlement and any active cache update.
	complete func()
}

type sessionSelectorWork struct {
	jobs      sync.WaitGroup
	updates   chan func()
	ready     chan struct{}
	cancelled []*sessionLoad // Owner-only list of refresh continuations awaiting their cancelled load's actual result.
}

func (load *sessionLoad) run(fn func(context.Context) ([]SessionInfo, error)) <-chan sessionLoadResult {
	// A Promise retains one result even when its observer has been cancelled.
	result := make(chan sessionLoadResult, 1)
	load.owner.work.jobs.Go(func() {
		sessions, err := fn(load.ctx)
		result <- sessionLoadResult{sessions, err}
		// The signal coalesces wakeups, not results; each Promise result remains buffered until the owner consumes it.
		// upstream: packages/coding-agent/src/modes/interactive/components/session-selector.ts:loadScope
		select {
		case load.owner.work.ready <- struct{}{}:
		default:
		}
	})
	return result
}

func (load *sessionLoad) post(fn func()) {
	select {
	case load.owner.work.updates <- fn:
	case <-load.ctx.Done(): // upstream: packages/coding-agent/src/modes/interactive/components/session-selector.ts:cancelLoads
	}
}

func asyncSessionsLoader(fn func(SessionListOptions) ([]SessionInfo, error)) sessionsLoader {
	return func(load *sessionLoad, progress SessionListProgress) <-chan sessionLoadResult {
		return load.run(func(ctx context.Context) ([]SessionInfo, error) {
			return fn(SessionListOptions{Context: ctx, OnProgress: func(loaded, total int, partial []SessionInfo) {
				// Listing can reuse its snapshot after the callback returns. Transfer ownership before posting to the input loop.
				partial = slices.Clone(partial)
				load.post(func() { progress(loaded, total, partial) })
			}})
		})
	}
}

func (s *SessionSelectorComponent) loadResult(scope sessionScope) <-chan sessionLoadResult {
	if s == nil {
		return nil
	}
	load := s.scopeLoad(scope)
	if load == nil {
		return nil
	}
	return load.result
}
func (s *SessionSelectorComponent) scopeLoad(scope sessionScope) *sessionLoad {
	if scope == sessionScopeAll {
		return s.allLoad
	}
	return s.currentLoad
}
func (s *SessionSelectorComponent) setScopeLoad(scope sessionScope, load *sessionLoad) {
	if scope == sessionScopeAll {
		s.allLoad = load
	} else {
		s.currentLoad = load
	}
}
func (s *SessionSelectorComponent) setScopeSessions(scope sessionScope, sessions []SessionInfo) {
	if scope == sessionScopeAll {
		s.all = sessions
	} else {
		s.current = sessions
	}
}
func (s *SessionSelectorComponent) scopeSessions(scope sessionScope) []SessionInfo {
	if scope == sessionScopeAll {
		return s.all
	}
	return s.current
}
func (s *SessionSelectorComponent) loadScope(scope sessionScope) {
	if s.scopeLoad(scope) != nil {
		return
	}
	// Only active controllers are cancelled. A completed loader can retain its un-aborted signal without retaining parent callbacks or timers.
	ctx, cancel := context.WithCancel(context.Background())
	load := &sessionLoad{ctx: ctx, cancel: cancel, owner: s}
	s.setScopeLoad(scope, load)
	s.loading = true
	s.loadProgress = nil
	progress := func(loaded, total int, partial []SessionInfo) {
		if s.scopeLoad(scope) != load {
			return
		}
		if partial != nil {
			s.setScopeSessions(scope, slices.Clone(partial))
			if scope == s.scope {
				s.refilterLoadedSessions()
			}
		}
		if scope != s.scope {
			return
		}
		s.loadProgress = &[2]int{loaded, total}
		s.renderRequested()
	}
	loader := s.currentLoader
	if scope == sessionScopeAll {
		loader = s.allLoader
	}
	load.result = loader(load, progress)
}
func (s *SessionSelectorComponent) finishLoad(scope sessionScope, result sessionLoadResult) {
	load := s.scopeLoad(scope)
	if load == nil {
		return
	}
	defer func() {
		s.renderRequested()
		complete := load.complete
		load.complete = nil
		if complete != nil {
			complete()
		}
	}()
	s.setScopeLoad(scope, nil)
	if result.err != nil {
		s.setScopeSessions(scope, nil)
		if scope == s.scope {
			s.loading = false
			s.loadProgress = nil
			s.setStatusMessage("Failed to load sessions: "+result.err.Error(), true, sessionSelectorLoadErrorTimeout)
			s.refilterLoadedSessions()
		}
		return
	}
	sessions := result.sessions
	if sessions == nil {
		sessions = []SessionInfo{}
	}
	s.setScopeSessions(scope, sessions)
	if scope != s.scope {
		return
	}
	s.loading = false
	s.loadProgress = nil
	s.refilterLoadedSessions()
}

// Preserve the touched path across progressive snapshots rather than its previous numeric index.
func (s *SessionSelectorComponent) refilterLoadedSessions() {
	var selectedPath string
	if s.selectionTouched && len(s.filtered) > 0 {
		selectedPath = s.filtered[s.selected].Session.Path
	}
	s.refilter()
	if !s.selectionTouched {
		s.selected = 0
	} else if selectedPath != "" {
		for i, node := range s.filtered {
			if node.Session.Path == selectedPath {
				s.selected = i
				break
			}
		}
	}
}
func (s *SessionSelectorComponent) cancelLoads() {
	for _, scope := range []sessionScope{sessionScopeCurrent, sessionScopeAll} {
		if load := s.scopeLoad(scope); load != nil {
			load.cancel()
			if load.complete != nil {
				s.work.cancelled = append(s.work.cancelled, load)
			}
			s.setScopeLoad(scope, nil)
			s.setScopeSessions(scope, nil)
		}
	}
}
func (s *SessionSelectorComponent) close() {
	s.clearStatusMessage()
	s.cancelLoads()
	s.work.jobs.Wait()
	for _, load := range s.work.cancelled {
		load.complete = nil
	}
	clear(s.work.cancelled)
	s.work.cancelled = nil
}

// Cancelled loadScope promises still settle their caller's finally block; cancellation alone does not settle them. This runs only on the selector owner.
func (s *SessionSelectorComponent) finishCancelledLoads() {
	pending := s.work.cancelled[:0]
	for _, load := range s.work.cancelled {
		select {
		case <-load.result:
			complete := load.complete
			load.complete = nil
			if complete != nil {
				complete()
			}
		default:
			pending = append(pending, load)
		}
	}
	clear(s.work.cancelled[len(pending):])
	s.work.cancelled = pending
}
func (s *SessionSelectorComponent) refreshCurrentScope() *sessionLoad {
	s.cancelLoads()
	s.current = nil
	s.all = nil
	s.loadScope(s.scope)
	return s.scopeLoad(s.scope)
}
func (s *SessionSelectorComponent) toggleScope() {
	if s.scope == sessionScopeCurrent {
		s.scope = sessionScopeAll
	} else {
		s.scope = sessionScopeCurrent
	}
	sessions := s.scopeSessions(s.scope)
	s.loading = s.scopeLoad(s.scope) != nil
	s.loadProgress = nil
	s.refilterLoadedSessions()
	if sessions == nil && !s.loading {
		s.loadScope(s.scope)
	}
	s.renderRequested()
}

// Promise completions queued before an input event run before that input.
func (s *SessionSelectorComponent) drainLoadUpdates() {
	s.finishCancelledLoads()
	for {
		select {
		case <-s.work.ready:
			s.finishCancelledLoads()
		case result := <-s.loadResult(sessionScopeCurrent):
			s.finishLoad(sessionScopeCurrent, result)
		case result := <-s.loadResult(sessionScopeAll):
			s.finishLoad(sessionScopeAll, result)
		case update := <-s.work.updates:
			update()
		default:
			return
		}
	}
}
