package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestNodeContextExposesRunMode pins ctx.mode for node extensions.
//
// The host has always sent the run mode in the ready payload (host.go SetMode,
// ReadyPayload.Mode), but the node runtime applied only cwd, model, and state,
// so ctx.mode was undefined for every pi extension. Upstream declares it on
// ExtensionContext (core/extensions/types.ts), and extensions branch on it:
// pi-atelier returns from session_start unless it is "tui", so it loaded,
// registered its command, and then did nothing at all.
func TestNodeContextExposesRunMode(t *testing.T) {
	shortSockDir(t)

	var mu sync.Mutex
	var notifications []string

	fakeUI := newTestUIContext()
	fakeUI.onNotify = func(msg, _ string) {
		mu.Lock()
		notifications = append(notifications, msg)
		mu.Unlock()
	}

	h := newTestHost(t)
	h.SetMode("tui")
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fakeUI)
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ext, err := h.Load(ctx, ExtConfig{
		Name:    "ctx-mode",
		Source:  filepath.Join("testdata", "ctx-mode.mjs"),
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := ext.Commands["report_mode"].Handler(context.Background(), ""); err != nil {
		t.Fatal(err)
	}

	var got string
	pollUntil(t, 10*time.Second, "extension never reported ctx.mode", func() bool {
		mu.Lock()
		defer mu.Unlock()
		if len(notifications) == 0 {
			return false
		}
		got = notifications[len(notifications)-1]
		return true
	})

	if got != "mode=tui" {
		t.Errorf("ctx.mode reported %q, want %q: extensions that branch on the run mode silently do nothing", got, "mode=tui")
	}
}

// TestNodeContextExposesProjectTrust pins ctx.isProjectTrusted() for node
// extensions.
//
// Upstream declares it on ExtensionContext (core/extensions/types.ts:332) and
// resolves it live through the settings manager, since trust can be granted
// mid-session. pig had the project_trust event but not the accessor, so an
// extension calling it threw: pi-atelier died at startup with
// "initializationContext.isProjectTrusted is not a function".
func TestNodeContextExposesProjectTrust(t *testing.T) {
	for _, trusted := range []bool{true, false} {
		t.Run(map[bool]string{true: "trusted", false: "untrusted"}[trusted], func(t *testing.T) {
			shortSockDir(t)

			var mu sync.Mutex
			var notifications []string

			fakeUI := newTestUIContext()
			fakeUI.onNotify = func(msg, _ string) {
				mu.Lock()
				notifications = append(notifications, msg)
				mu.Unlock()
			}

			h := newTestHost(t)
			bridge := NewUIBridge(func() {})
			bridge.SetUIContext(fakeUI)
			bridge.SetHostAction("isProjectTrusted", func() bool { return trusted })
			h.SetUIBridge(bridge)
			defer h.Shutdown("test done")

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			ext, err := h.Load(ctx, ExtConfig{
				Name:    "ctx-trust",
				Source:  filepath.Join("testdata", "ctx-trust.mjs"),
				Enabled: true,
			})
			if err != nil {
				t.Fatal(err)
			}

			if err := ext.Commands["report_trust"].Handler(context.Background(), ""); err != nil {
				t.Fatal(err)
			}

			want := fmt.Sprintf("trusted=%t", trusted)
			var got string
			pollUntil(t, 10*time.Second, "extension never reported project trust", func() bool {
				mu.Lock()
				defer mu.Unlock()
				if len(notifications) == 0 {
					return false
				}
				got = notifications[len(notifications)-1]
				return true
			})
			if got != want {
				t.Errorf("ctx.isProjectTrusted() reported %q, want %q", got, want)
			}
		})
	}
}

// TestCustomOverlayExposesTerminalGeometry pins tui.terminal for ui.custom
// components.
//
// Upstream TUI exposes `terminal` (tui.ts:333) and Terminal declares
// columns/rows getters (terminal.ts:79-80). pig's overlay shim offered only
// width/height, so a component reading tui.terminal.rows threw inside its own
// render: pi-atelier failed with "Cannot read properties of undefined (reading
// 'rows')" for ui.custom and 'columns' for its sidebar.
func TestCustomOverlayExposesTerminalGeometry(t *testing.T) {
	shortSockDir(t)

	var mu sync.Mutex
	var notifications []string

	fakeUI := newTestUIContext()
	fakeUI.onNotify = func(msg, _ string) {
		mu.Lock()
		notifications = append(notifications, msg)
		mu.Unlock()
	}

	h := newTestHost(t)
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fakeUI)
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ext, err := h.Load(ctx, ExtConfig{
		Name:    "overlay-terminal",
		Source:  filepath.Join("testdata", "overlay-terminal.mjs"),
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	go func() { _ = ext.Commands["report_geometry"].Handler(context.Background(), "") }()

	var got string
	pollUntil(t, 15*time.Second, "overlay never reported terminal geometry", func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, n := range notifications {
			if strings.HasPrefix(n, "cols=") {
				got = n
				return true
			}
		}
		return false
	})

	if strings.Contains(got, "undefined") || strings.Contains(got, "NaN") {
		t.Errorf("tui.terminal geometry reported %q: components sizing from it throw", got)
	}
}

// TestFooterFactoryReceivesProvider pins the third argument of a ui.setFooter
// factory as upstream's ReadonlyFooterDataProvider (footer-data-provider.ts:387):
// getGitBranch, getExtensionStatuses, getAvailableProviderCount, onBranchChange.
//
// pig passed the raw state object instead, so pi-atelier's footer died with
// "footerData.onBranchChange is not a function" and the extension crash-looped.
func TestFooterFactoryReceivesProvider(t *testing.T) {
	shortSockDir(t)

	var mu sync.Mutex
	var notifications []string

	fakeUI := newTestUIContext()
	fakeUI.onNotify = func(msg, _ string) {
		mu.Lock()
		notifications = append(notifications, msg)
		mu.Unlock()
	}

	h := newTestHost(t)
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fakeUI)
	bridge.SetHostAction("getGitBranch", func() string { return "feat/pypiserver" })
	bridge.SetHostAction("getExtensionStatuses", func() map[string]string {
		return map[string]string{"atelier": "ready", "ask": "idle"}
	})
	bridge.SetHostAction("getAvailableProviderCount", func() int { return 3 })
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ext, err := h.Load(ctx, ExtConfig{
		Name:    "footer-provider",
		Source:  filepath.Join("testdata", "footer-provider.mjs"),
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	go func() { _ = ext.Commands["show_footer"].Handler(context.Background(), "") }()

	var got string
	pollUntil(t, 15*time.Second, "footer never reported its provider surface", func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, n := range notifications {
			if strings.HasPrefix(n, "branch=") {
				got = n
				return true
			}
		}
		return false
	})

	want := "branch=feat/pypiserver statuses=2 providers=3 unsub=function"
	if got != want {
		t.Errorf("footer observed %q, want %q", got, want)
	}

	mu.Lock()
	notifications = nil
	mu.Unlock()
	go func() { _ = ext.Commands["show_status_footer"].Handler(context.Background(), "") }()
	pollUntil(t, 15*time.Second, "footer did not observe the status set immediately before it", func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, notification := range notifications {
			if strings.HasPrefix(notification, "local=") {
				got = notification
				return true
			}
		}
		return false
	})
	if got != "local=fresh" {
		t.Errorf("footer observed immediate status %q, want local=fresh", got)
	}
}

// TestFooterFactoryRefreshesAfterStatusChange pins that a later ui.setStatus
// call reaches a footer factory that composes its lines from
// footerData.getExtensionStatuses(), matching upstream's setExtensionStatus
// (interactive-mode.ts:2203-2205): it updates the footer data provider and
// calls this.ui.requestRender() synchronously in the same in-process
// runtime, so the next render always sees the new value.
//
// Pig's footer factory runs in a separate Node subprocess and only
// re-renders on a pushed state_update (runtime.mjs applyState ->
// renderSpecialSurface). Before UIBridge.OnStateChanged was wired to
// Host.BroadcastStateUpdate, ui.setStatus never triggered that push, so a
// footer already installed at session_start (as in the parity fixture
// parity/scenarios/extensions-runtime/testdata/ext/footer-status.mjs) could
// go stale forever after the first render: most visibly across /reload
// (scenario 15-footer-status-reload-composition), where the reloaded
// extension's own setStatus/setFooter calls are the only state transition
// and nothing else happens to trigger an incidental re-render.
func TestFooterFactoryRefreshesAfterStatusChange(t *testing.T) {
	shortSockDir(t)

	var mu sync.Mutex
	statuses := map[string]string{}

	fakeUI := newTestUIContext()
	fakeUI.onStatus = func(key, text string) {
		mu.Lock()
		statuses[key] = text
		mu.Unlock()
	}

	h := newTestHost(t)
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fakeUI)
	bridge.SetHostAction("getExtensionStatuses", func() map[string]string {
		mu.Lock()
		defer mu.Unlock()
		return maps.Clone(statuses)
	})
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ext, err := h.Load(ctx, ExtConfig{
		Name:    "footer-status-live",
		Source:  filepath.Join("testdata", "footer-status-live.mjs"),
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(ext.Handlers["session_start"]) == 0 {
		t.Fatal("missing session_start handler")
	}
	if _, err := ext.Handlers["session_start"][0](map[string]any{"type": "session_start"}); err != nil {
		t.Fatalf("dispatch session_start: %v", err)
	}

	pollUntil(t, 15*time.Second, "footer never reflected the initial status", func() bool {
		return slices.Contains(fakeUI.uiSnapshot().footerLines, "one")
	})

	if err := ext.Commands["bump-status"].Handler(ctx, ""); err != nil {
		t.Fatalf("bump-status: %v", err)
	}

	pollUntil(t, 15*time.Second, "footer never reflected a status change after session_start: "+
		"ui.setStatus must broadcast a state_update so the subprocess footer factory re-renders", func() bool {
		return slices.Contains(fakeUI.uiSnapshot().footerLines, "two")
	})
}

// TestSessionLogReplicatesIncrementally pins that an extension sees the whole
// session log and the correct branch when the host sends only what is new.
//
// The push carries entriesAppended plus a total, not the full history, so the
// runtime folds appends into a local log and derives the branch by walking
// parent links from the leaf, as upstream does in process. A fold or walk bug
// would silently hand extensions a truncated or wrong-path conversation.
//
// The fixture entries deliberately branch: e2 and e1 are both children of e0,
// so a leaf of e2 must yield [e0 e2] and never the append order.
func TestSessionLogReplicatesIncrementally(t *testing.T) {
	shortSockDir(t)

	entry := func(id, parent string) json.RawMessage {
		if parent == "" {
			return json.RawMessage(fmt.Sprintf(`{"type":"message","id":%q}`, id))
		}
		return json.RawMessage(fmt.Sprintf(`{"type":"message","id":%q,"parentId":%q}`, id, parent))
	}

	var mu sync.Mutex
	log := []json.RawMessage{entry("e0", "")}
	leaf := "e0"

	var notifyMu sync.Mutex
	var notifications []string

	fakeUI := newTestUIContext()
	fakeUI.onNotify = func(msg, _ string) {
		notifyMu.Lock()
		notifications = append(notifications, msg)
		notifyMu.Unlock()
	}

	h := newTestHost(t)
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fakeUI)
	bridge.SetHostAction("getSessionID", func() string { return "sess-repl" })
	bridge.SetHostAction("getLeafID", func() string {
		mu.Lock()
		defer mu.Unlock()
		return leaf
	})
	bridge.SetHostAction("getEntriesPage", func(cursor, _ int) ([]json.RawMessage, int, bool, string) {
		mu.Lock()
		defer mu.Unlock()
		if cursor < 0 || cursor > len(log) {
			cursor = 0
		}
		return log[cursor:], len(log), false, leaf
	})
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ext, err := h.Load(ctx, ExtConfig{
		Name:    "session-replication",
		Source:  filepath.Join("testdata", "session-replication.mjs"),
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	report := func(want string) {
		t.Helper()
		notifyMu.Lock()
		notifications = nil
		notifyMu.Unlock()
		go func() { _ = ext.Commands["report_session"].Handler(context.Background(), "") }()
		var got string
		pollUntil(t, 15*time.Second, "extension never reported its session view", func() bool {
			notifyMu.Lock()
			defer notifyMu.Unlock()
			for _, n := range notifications {
				if strings.HasPrefix(n, "entries=") {
					got = n
					return true
				}
			}
			return false
		})
		if got != want {
			t.Errorf("extension saw %q, want %q", got, want)
		}
	}

	report("entries=[e0] branch=[e0]")

	// Two appends land, branching from the same parent.
	mu.Lock()
	log = append(log, entry("e1", "e0"), entry("e2", "e0"))
	leaf = "e2"
	mu.Unlock()

	// The whole log must be visible even though only e1 and e2 were sent, and
	// the branch must follow parent links rather than append order.
	report("entries=[e0,e1,e2] branch=[e0,e2]")

	// Replacing the Session with a shorter log resets the runtime mirror and host cursor together.
	mu.Lock()
	log = []json.RawMessage{entry("replacement", "")}
	leaf = "replacement"
	mu.Unlock()
	report("entries=[replacement] branch=[replacement]")
}

// getEditorText, getToolsExpanded, and getAllThemes are synchronous in the
// extension API, so the Node runtime cannot make a host call for them. It used
// to return "", false, and [] unconditionally, which is worse than an absent
// method: an extension reading the editor concluded it was empty and never saw
// an error. The values now travel with the state snapshot, which the host
// pushes immediately before dispatching a command, so a handler reads current
// data.
func TestNodeRuntimeReportsRealUIStateNotStubs(t *testing.T) {
	shortSockDir(t)

	var notifyMu sync.Mutex
	var notifications []string

	fakeUI := newTestUIContext()
	fakeUI.onNotify = func(msg, _ string) {
		notifyMu.Lock()
		notifications = append(notifications, msg)
		notifyMu.Unlock()
	}
	fakeUI.allThemes = []extension.ThemeMeta{
		{Name: "dark", Path: "/tmp/dark.json"},
		{Name: "solarized", Path: "/tmp/solarized.json"},
	}
	fakeUI.setUIState("draft prompt", true)

	h := newTestHost(t)
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fakeUI)
	bridge.SetHostAction("getSystemPromptOptions", func() extension.BuildSystemPromptOptions {
		return extension.BuildSystemPromptOptions{Cwd: "/probe/cwd"}
	})
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ext, err := h.Load(ctx, ExtConfig{
		Name:    "ui-state-replication",
		Source:  filepath.Join("testdata", "ui-state-replication.mjs"),
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	report := func(want string) {
		t.Helper()
		notifyMu.Lock()
		notifications = nil
		notifyMu.Unlock()
		go func() { _ = ext.Commands["report_ui"].Handler(context.Background(), "") }()
		var got string
		pollUntil(t, 15*time.Second, "extension never reported ui state", func() bool {
			notifyMu.Lock()
			defer notifyMu.Unlock()
			for _, n := range notifications {
				if strings.HasPrefix(n, "text=") {
					got = n
					return true
				}
			}
			return false
		})
		if got != want {
			t.Errorf("extension saw %q, want %q", got, want)
		}
	}

	report(`text="draft prompt" expanded=true themes=[dark,solarized] named=solarized spo_cwd=/probe/cwd`)

	// A later dispatch must observe the edit, not the value captured at load.
	fakeUI.setUIState("", false)
	report(`text="" expanded=false themes=[dark,solarized] named=solarized spo_cwd=/probe/cwd`)
}

// setSessionName, setLabel, and ui.setWorkingVisible are upstream extension
// API members (types.ts:1319, :1325, :154). The host implemented all three and
// the Go, Rust, and Python SDKs called them, but the Node runtime never
// defined them, so a TypeScript extension calling any of them threw
// TypeError instead of reaching the host.
func TestNodeRuntimeExposesSessionAndWorkingVisibleAPI(t *testing.T) {
	shortSockDir(t)

	var mu sync.Mutex
	var sessionName, labelEntry, labelValue string
	var workingVisible = true
	var notified bool

	fakeUI := newTestUIContext()
	fakeUI.onNotify = func(string, string) {
		mu.Lock()
		notified = true
		mu.Unlock()
	}
	fakeUI.onSetWorkingVisible = func(v bool) {
		mu.Lock()
		workingVisible = v
		mu.Unlock()
	}

	h := newTestHost(t)
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fakeUI)
	bridge.SetHostAction("setSessionName", func(n string) error {
		mu.Lock()
		sessionName = n
		mu.Unlock()
		return nil
	})
	bridge.SetHostAction("setLabel", func(entryID, label string) error {
		mu.Lock()
		labelEntry, labelValue = entryID, label
		mu.Unlock()
		return nil
	})
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ext, err := h.Load(ctx, ExtConfig{
		Name:    "api-surface",
		Source:  filepath.Join("testdata", "api-surface.mjs"),
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := ext.Commands["exercise_api"].Handler(context.Background(), ""); err != nil {
		t.Fatalf("command failed, which is how a missing api member surfaces: %v", err)
	}

	pollUntil(t, 15*time.Second, "extension never completed its api calls", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return notified && sessionName != "" && labelEntry != "" && !workingVisible
	})

	mu.Lock()
	defer mu.Unlock()
	if sessionName != "renamed-by-extension" {
		t.Errorf("setSessionName delivered %q", sessionName)
	}
	if labelEntry != "entry-7" || labelValue != "bookmark" {
		t.Errorf("setLabel delivered (%q, %q)", labelEntry, labelValue)
	}
	if workingVisible {
		t.Error("ui.setWorkingVisible never reached the host")
	}
}
