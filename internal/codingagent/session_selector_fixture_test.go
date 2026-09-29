package codingagent

// Resolved fixture promises need no producer goroutine. The initial drain corresponds to the upstream fixture's flushPromises after construction.
func resolvedSessionLoader(fn func() ([]SessionInfo, error)) sessionsLoader {
	return func(_ *sessionLoad, _ SessionListProgress) <-chan sessionLoadResult {
		result := make(chan sessionLoadResult, 1)
		sessions, err := fn()
		result <- sessionLoadResult{sessions, err}
		return result
	}
}
func newLoadedSessionSelector(currentLoader, allLoader func() ([]SessionInfo, error), renameSession func(string, string) error, deleteSession func(string) error, currentPath string, kb *KeybindingsManager) *sessionSelector {
	var deleter func(string) sessionDeleteResult
	if deleteSession != nil {
		deleter = unlinkDeleter(deleteSession)
	}
	s := newSessionSelectorWithLoaders(resolvedSessionLoader(currentLoader), resolvedSessionLoader(allLoader), renameSession, deleter, currentPath, kb)
	s.drainLoadUpdates()
	return s
}

// unlinkDeleter adapts a fixture deletion to deleteSessionFile's result for its unlink fallback.
func unlinkDeleter(remove func(string) error) func(string) sessionDeleteResult {
	return func(path string) sessionDeleteResult {
		if err := remove(path); err != nil {
			return sessionDeleteResult{method: sessionDeleteUnlink, error: err.Error()}
		}
		return sessionDeleteResult{ok: true, method: sessionDeleteUnlink}
	}
}
