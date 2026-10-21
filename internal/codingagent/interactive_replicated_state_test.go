package codingagent

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// A footer reads the live git branch and provider count (footer-data-provider.ts getGitBranch, getAvailableProviderCount), and an extension's footer factory reads them through the state the host replicates. The provider reports a change of either, and only a change: a repeated value tells nobody.
func TestFooterDataProviderReportsABranchOrProviderCountChange(t *testing.T) {
	provider := NewFooterDataProvider()
	var reports atomic.Int64
	stop := provider.ObserveChange(func() { reports.Add(1) })
	expect := func(step string, want int64) {
		t.Helper()
		if got := reports.Load(); got != want {
			t.Fatalf("%s: %d reports, want %d", step, got, want)
		}
	}
	provider.SetProviderCount(0)
	expect("an unchanged provider count", 0)
	provider.SetProviderCount(3)
	expect("a changed provider count", 1)
	provider.SetProviderCount(3)
	expect("a repeated provider count", 1)
	provider.setGitBranch("")
	expect("an unchanged branch", 1)
	provider.setGitBranch("feature")
	expect("a changed branch", 2)
	provider.setGitBranch("feature")
	expect("a repeated branch", 2)
	provider.SetExtensionStatus("key", "text")
	expect("an extension status, which the setStatus call already announces", 2)
	stop()
	provider.SetProviderCount(4)
	expect("a stopped observer", 2)
}

// stateObservingHandle is a Session handle that reports replicated state changes when the test tells it to.
type stateObservingHandle struct {
	InteractiveSessionHandle
	mu        sync.Mutex
	observers map[int]func()
	next      int
}

func (h *stateObservingHandle) ObserveReplicatedState(observe func()) func() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.observers == nil {
		h.observers = map[int]func(){}
	}
	id := h.next
	h.next++
	h.observers[id] = observe
	return func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.observers, id)
	}
}

func (h *stateObservingHandle) changed() {
	h.mu.Lock()
	observers := make([]func(), 0, len(h.observers))
	for _, observe := range h.observers {
		observers = append(observers, observe)
	}
	h.mu.Unlock()
	for _, observe := range observers {
		observe()
	}
}

// hostStateBridge is the bridge interactive mode reports host state changes to.
type hostStateBridge struct {
	captureUIBridge
	reports atomic.Int64
}

func (b *hostStateBridge) HostStateChanged() { b.reports.Add(1) }

var _ hostStateNotifier = (*hostStateBridge)(nil)

// Pi's ctx.model, pi.getSettings() and pi.getSessionName() read the live Session, ctx.ui.theme is the global theme (interactive-mode.ts:2665), and a footer reads the live git branch and provider count: a user's model switch, settings edit or /name, a theme selection, a branch checkout or a login changes them with no extension call. Interactive mode reports each change of the Session's replicated state, of the active theme and of the footer's branch and provider count to the extension host, and stops when its wiring is detached.
func TestInteractiveModeReportsReplicatedStateChangesToTheExtensionHost(t *testing.T) {
	bridge := &hostStateBridge{}
	handle := &stateObservingHandle{}
	m := &InteractiveMode{opts: InteractiveModeOptions{SubprocessUIBridge: bridge, SessionHandle: handle}}
	m.statusLine = NewFooterComponent(nil, "", nil)
	previousTheme := tui.ActiveTheme()
	t.Cleanup(func() { tui.SetThemeInstance(previousTheme) })
	detach := m.wireSubprocessHostCallbacks()

	handle.changed()
	if got := bridge.reports.Load(); got != 1 {
		t.Fatalf("a Session state change reported %d times, want 1", got)
	}
	m.statusLine.SetProviderCount(2)
	if got := bridge.reports.Load(); got != 2 {
		t.Fatalf("a provider count change reported %d times in all, want 2", got)
	}
	m.statusLine.setGitBranch("feature")
	if got := bridge.reports.Load(); got != 3 {
		t.Fatalf("a branch change reported %d times in all, want 3", got)
	}
	tui.SetTheme("light")
	if got := bridge.reports.Load(); got != 4 {
		t.Fatalf("a theme selection reported %d times in all, want 4", got)
	}

	detach()
	handle.changed()
	m.statusLine.SetProviderCount(5)
	tui.SetTheme("dark")
	if got := bridge.reports.Load(); got != 4 {
		t.Fatalf("detached wiring still reported: %d reports in all", got)
	}
}

// A bridge without HostStateChanged, as a test double or a host with no extension runtime, is left alone.
func TestInteractiveModeWithABridgeThatTakesNoStateReportsDoesNotWatch(t *testing.T) {
	bridge := &captureUIBridge{}
	handle := &stateObservingHandle{}
	m := &InteractiveMode{opts: InteractiveModeOptions{SubprocessUIBridge: bridge, SessionHandle: handle}}
	detach := m.wireSubprocessHostCallbacks()
	defer detach()
	if len(handle.observers) != 0 {
		t.Fatalf("the Session was observed on behalf of a bridge that takes no reports: %d observers", len(handle.observers))
	}
}
