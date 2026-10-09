package codingagent

import (
	"maps"
	"slices"
	"sync"
)

// FooterDataProvider is the data a footer reads about its surroundings (footer-data-provider.ts FooterDataProvider): the working directory and
// the git branch of the repository bound to it, the statuses extensions set, the number of available providers, and the branch-change
// subscribers. A footer built with NewFooterComponentForSession shares one provider with the host that feeds it.
type FooterDataProvider struct {
	mu                sync.RWMutex
	cwd               string
	gitBranch         string    // cached; resolved from the initial repository binding
	gitPaths          *gitPaths // repository binding captured before extension startup
	extensionStatuses map[string]string
	providerCount     int

	// branchChangeMu guards the OnBranchChange subscribers (footer-data-provider.ts branchChangeCallbacks).
	branchChangeMu     sync.Mutex
	branchChangeHooks  map[int]func()
	branchChangeNextID int
	// disposed and stopWatcher are footer-data-provider.ts `disposed` and its git watchers; Dispose sets and runs them.
	disposed    bool
	stopWatcher func()
}

// NewFooterDataProvider creates a provider bound to no repository, with no statuses and no providers.
func NewFooterDataProvider() *FooterDataProvider {
	return &FooterDataProvider{extensionStatuses: make(map[string]string), branchChangeHooks: make(map[int]func())}
}

// SetCwd binds the provider to the repository present at cwd, resolving its branch.
func (d *FooterDataProvider) SetCwd(cwd string) {
	d.mu.Lock()
	d.cwd = cwd
	d.gitPaths = nil
	d.gitBranch = ""
	if paths, ok := findGitPaths(cwd); cwd != "" && ok {
		d.gitPaths = &paths
		d.gitBranch = resolveGitBranchFromPaths(paths)
	}
	d.mu.Unlock()
}

// GetGitBranch returns the cached git branch, empty outside a repo (getGitBranch).
func (d *FooterDataProvider) GetGitBranch() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.gitBranch
}

// GetAvailableProviderCount returns the number of authenticated, reachable providers (getAvailableProviderCount).
func (d *FooterDataProvider) GetAvailableProviderCount() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.providerCount
}

// SetProviderCount records the number of authenticated, reachable providers (setAvailableProviderCount).
func (d *FooterDataProvider) SetProviderCount(n int) {
	d.mu.Lock()
	d.providerCount = n
	d.mu.Unlock()
}

// GetExtensionStatuses returns a snapshot of all extension statuses (getExtensionStatuses).
func (d *FooterDataProvider) GetExtensionStatuses() map[string]string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return maps.Clone(d.extensionStatuses)
}

// SetExtensionStatus sets, or with empty text clears, a keyed status entry (setExtensionStatus, clearExtensionStatuses).
func (d *FooterDataProvider) SetExtensionStatus(key, text string) {
	d.mu.Lock()
	if text == "" {
		delete(d.extensionStatuses, key)
	} else {
		d.extensionStatuses[key] = text
	}
	d.mu.Unlock()
}

// OnBranchChange subscribes to git-branch updates and returns the unsubscribe function (onBranchChange).
func (d *FooterDataProvider) OnBranchChange(fn func()) func() {
	d.branchChangeMu.Lock()
	id := d.branchChangeNextID
	d.branchChangeNextID++
	d.branchChangeHooks[id] = fn
	d.branchChangeMu.Unlock()
	return func() {
		d.branchChangeMu.Lock()
		delete(d.branchChangeHooks, id)
		d.branchChangeMu.Unlock()
	}
}

// notifyBranchChange runs the OnBranchChange subscribers in subscription order, like upstream's branchChangeCallbacks.
func (d *FooterDataProvider) notifyBranchChange() {
	d.branchChangeMu.Lock()
	ids := slices.Sorted(maps.Keys(d.branchChangeHooks))
	hooks := make([]func(), len(ids))
	for i, id := range ids {
		hooks[i] = d.branchChangeHooks[id]
	}
	d.branchChangeMu.Unlock()
	for _, fn := range hooks {
		fn()
	}
}

// setGitBranch records a branch the watcher resolved.
func (d *FooterDataProvider) setGitBranch(branch string) {
	d.mu.Lock()
	d.gitBranch = branch
	d.mu.Unlock()
}

// boundGitPaths returns the repository binding, or nil outside a repository.
func (d *FooterDataProvider) boundGitPaths() *gitPaths {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.gitPaths
}

// snapshot reads what one footer render needs under a single lock.
func (d *FooterDataProvider) snapshot() (cwd, gitBranch string, providerCount int, extensionStatuses map[string]string) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.cwd, d.gitBranch, d.providerCount, maps.Clone(d.extensionStatuses)
}

// Dispose is footer-data-provider.ts dispose (:187), which FooterComponent.dispose (footer.ts:93) leaves the cleanup to: it stops the git
// branch watcher and drops the OnBranchChange subscribers.
func (d *FooterDataProvider) Dispose() {
	d.branchChangeMu.Lock()
	d.disposed = true
	stop := d.stopWatcher
	d.stopWatcher = nil
	clear(d.branchChangeHooks)
	d.branchChangeMu.Unlock()
	if stop != nil {
		stop()
	}
}

// setWatcherStop registers the function that stops the git branch watcher, or runs it at once when Dispose already ran.
func (d *FooterDataProvider) setWatcherStop(stop func()) {
	d.branchChangeMu.Lock()
	disposed := d.disposed
	if !disposed {
		d.stopWatcher = stop
	}
	d.branchChangeMu.Unlock()
	if disposed {
		stop()
	}
}

func (d *FooterDataProvider) isDisposed() bool {
	d.branchChangeMu.Lock()
	defer d.branchChangeMu.Unlock()
	return d.disposed
}
