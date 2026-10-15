package sdk

import (
	"fmt"
	"maps"
	"slices"
	"sync"
)

// footerSnapshot is the host's footer data as the last state update reported it.
type footerSnapshot struct {
	gitBranch string
	statuses  map[string]string
	providers int
}

// footerSubscribers holds the onBranchChange callbacks of an extension.
type footerSubscribers struct {
	mu        sync.Mutex
	next      int
	callbacks map[int]func()
}

// FooterData is Pi's ReadonlyFooterDataProvider (footer-data-provider.ts:103-163,387): the footer data Pi hands to a ui.setFooter factory as its third argument. A Go footer renderer reads it from the context it was installed with; [Context.SetFooterRenderer] renders again when the host's footer data changes. The values replicate the host's, so reads never call the host.
type FooterData struct{ ext *Extension }

// FooterData returns the footer data provider.
func (c Context) FooterData() FooterData { return FooterData{ext: c.ext} }

// GitBranch returns the current Git branch, and false when there is none (Pi's null).
func (f FooterData) GitBranch() (string, bool) {
	f.ext.mu.RLock()
	defer f.ext.mu.RUnlock()
	return f.ext.footer.gitBranch, f.ext.footer.gitBranch != ""
}

// ExtensionStatuses returns every extension's status text by key; the caller owns the map. The host reports keys in sorted order, so a map loses nothing Pi's insertion-ordered Map keeps.
func (f FooterData) ExtensionStatuses() map[string]string {
	f.ext.mu.RLock()
	defer f.ext.mu.RUnlock()
	return maps.Clone(f.ext.footer.statuses)
}

// AvailableProviderCount returns the number of providers with configured auth.
func (f FooterData) AvailableProviderCount() int {
	f.ext.mu.RLock()
	defer f.ext.mu.RUnlock()
	return f.ext.footer.providers
}

// OnBranchChange registers callback to run when the Git branch changes and returns the function that unsubscribes it. A nil callback registers nothing. A panic in a callback is reported to the user, as the Node runtime reports a failing subscriber, and does not stop the others.
func (f FooterData) OnBranchChange(callback func()) (unsubscribe func()) {
	if callback == nil {
		return func() {}
	}
	subs := &f.ext.footerSubs
	subs.mu.Lock()
	defer subs.mu.Unlock()
	if subs.callbacks == nil {
		subs.callbacks = map[int]func(){}
	}
	id := subs.next
	subs.next++
	subs.callbacks[id] = callback
	return func() {
		subs.mu.Lock()
		delete(subs.callbacks, id)
		subs.mu.Unlock()
	}
}

// applyFooterData installs the footer data of a state update and reports whether it changed and whether the branch did. A state without footer data keeps the last one. The caller holds e.mu.
func (e *Extension) applyFooterData(data *footerDataWire) (changed, branchChanged bool) {
	if data == nil {
		return false, false
	}
	next := footerSnapshot{gitBranch: data.GitBranch, statuses: maps.Clone(data.ExtensionStatuses), providers: data.AvailableProviderCount}
	if next.statuses == nil {
		next.statuses = map[string]string{}
	}
	previous := e.footer
	changed = next.gitBranch != previous.gitBranch || next.providers != previous.providers || !maps.Equal(next.statuses, previous.statuses)
	e.footer = next
	return changed, next.gitBranch != previous.gitBranch
}

// footerDataWire is the footerData object of a state update.
type footerDataWire struct {
	GitBranch              string            `json:"gitBranch"`
	ExtensionStatuses      map[string]string `json:"extensionStatuses"`
	AvailableProviderCount int               `json:"availableProviderCount"`
}

// notifyFooterData tells the footer renderers and, when the branch changed, the branch subscribers about a change of footer data. It runs without e.mu held.
func (e *Extension) notifyFooterData(branchChanged bool) {
	e.refreshFooter()
	if !branchChanged {
		return
	}
	e.footerSubs.mu.Lock()
	ids := make([]int, 0, len(e.footerSubs.callbacks))
	for id := range e.footerSubs.callbacks {
		ids = append(ids, id)
	}
	e.footerSubs.mu.Unlock()
	slices.Sort(ids)
	for _, id := range ids {
		e.footerSubs.mu.Lock()
		callback := e.footerSubs.callbacks[id]
		e.footerSubs.mu.Unlock()
		if callback != nil {
			e.runBranchSubscriber(callback)
		}
	}
}

func (e *Extension) runBranchSubscriber(callback func()) {
	defer func() {
		if recovered := recover(); recovered != nil {
			Context{ext: e}.Notify(fmt.Sprintf("footer branch subscriber failed: %v", recovered), "error")
		}
	}()
	callback()
}
