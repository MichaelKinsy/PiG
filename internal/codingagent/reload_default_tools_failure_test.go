package codingagent

import (
	"errors"
	"slices"
	"testing"
)

// pendingToolsHandle records the tool-registry calls a reload makes on a Session.
type pendingToolsHandle struct {
	recordingCompactHandle
	pending   bool
	refreshes int
	discards  int
}

func (h *pendingToolsHandle) ReloadSettings()           { h.pending = true }
func (h *pendingToolsHandle) DiscardAddedDefaultTools() { h.pending, h.discards = false, h.discards+1 }
func (h *pendingToolsHandle) RefreshTools() error {
	h.pending, h.refreshes = false, h.refreshes+1
	return nil
}

// agent-session.ts:3591-3609: a reload that throws before _buildRuntime never activates the tools it found newly added to defaultTools.
// Interactive /reload stages them with ReloadSettings; when the extension host cannot reload, neither replaceExtensionRunner nor the tool refresh runs, so the staged tools must not stay pending for the next unrelated refresh.
func TestFailedExtensionReloadDropsTheStagedDefaultTools(t *testing.T) {
	restoreStartupTheme(t)
	handle := &pendingToolsHandle{}
	m := reloadTestMode(InteractiveModeOptions{
		SubprocessHost:  &orderRecordingHost{err: errors.New("reload failed")},
		SessionHandle:   handle,
		SettingsManager: NewSettingsManager(t.TempDir(), t.TempDir()),
	})
	if err := m.buildSlashContext(t.Context()).Reload(); err != nil {
		t.Fatal(err)
	}
	if handle.pending || handle.refreshes != 0 {
		t.Fatalf("pending = %v, refreshes = %d: a failed extension reload left the staged default tools for a later refresh", handle.pending, handle.refreshes)
	}
}

func TestFailedResourceReloadDropsTheStagedDefaultTools(t *testing.T) {
	restoreStartupTheme(t)
	handle := &pendingToolsHandle{}
	resourceErr := errors.New("invalid resource entry")
	m := reloadTestMode(InteractiveModeOptions{
		SessionHandle:          handle,
		SettingsManager:        NewSettingsManager(t.TempDir(), t.TempDir()),
		ReloadResourceProvider: func() ReloadResourceSnapshot { return ReloadResourceSnapshot{Err: resourceErr} },
	})
	if err := m.buildSlashContext(t.Context()).Reload(); !errors.Is(err, resourceErr) {
		t.Fatalf("reload error = %v, want %v", err, resourceErr)
	}
	if handle.pending || handle.refreshes != 0 {
		t.Fatalf("pending = %v, refreshes = %d: a failed resource reload left the staged default tools for a later refresh", handle.pending, handle.refreshes)
	}
}

func TestSuccessfulReloadStillRefreshesTheStagedDefaultTools(t *testing.T) {
	restoreStartupTheme(t)
	handle := &pendingToolsHandle{}
	m := reloadTestMode(InteractiveModeOptions{
		SubprocessHost:  &orderRecordingHost{},
		SessionHandle:   handle,
		SettingsManager: NewSettingsManager(t.TempDir(), t.TempDir()),
	})
	if err := m.buildSlashContext(t.Context()).Reload(); err != nil {
		t.Fatal(err)
	}
	if handle.pending || handle.refreshes == 0 {
		t.Fatalf("pending = %v, refreshes = %d: a successful reload must rebuild the tool registry", handle.pending, handle.refreshes)
	}
}

type reloadRefreshHandle struct {
	pendingToolsHandle
	reloadRefreshes int
}

func (h *reloadRefreshHandle) RefreshToolsAfterReload() error {
	h.pending, h.reloadRefreshes = false, h.reloadRefreshes+1
	return nil
}

// agent-session.ts:3604-3609: /reload rebuilds the tool registry with includeAllExtensionTools, which re-activates an extension tool disabled during the session; a plain refresh keeps the selection.
func TestReloadRebuildsToolsAsUpstreamReloadDoes(t *testing.T) {
	restoreStartupTheme(t)
	handle := &reloadRefreshHandle{}
	m := reloadTestMode(InteractiveModeOptions{
		SubprocessHost:  &orderRecordingHost{},
		SessionHandle:   handle,
		SettingsManager: NewSettingsManager(t.TempDir(), t.TempDir()),
	})
	if err := m.buildSlashContext(t.Context()).Reload(); err != nil {
		t.Fatal(err)
	}
	if handle.reloadRefreshes != 1 || handle.refreshes != 0 {
		t.Fatalf("reload refreshes = %d, plain refreshes = %d: /reload must rebuild the tools as reload() does", handle.reloadRefreshes, handle.refreshes)
	}
}

// selectionOwningHandle owns the active tool selection as coding.Session does, and records how a mode applies it.
type selectionOwningHandle struct {
	reloadRefreshHandle
	active    []string
	reapplied [][]string
	loadouts  [][]string
}

func (h *selectionOwningHandle) ActiveToolNames() []string { return slices.Clone(h.active) }
func (h *selectionOwningHandle) ReapplyActiveTools(names []string) {
	h.reapplied = append(h.reapplied, slices.Clone(names))
}

func (h *selectionOwningHandle) SetActiveToolsByName(names []string) {
	h.loadouts = append(h.loadouts, slices.Clone(names))
}

// agent-session.ts reload: tools the reloaded extensions register later, such as MCP tools that tool_search loaded, stay pending, and only setActiveToolsByName() drops them when it deactivates a tool. After interactive /reload rebuilds the agent's tools, it applies the Session's selection again, which is not a new loadout: going through SetActiveToolsByName would see the rebuilt tools deactivated and drop the pending ones.
func TestReloadReappliesTheSessionSelectionWithoutANewLoadout(t *testing.T) {
	restoreStartupTheme(t)
	handle := &selectionOwningHandle{active: []string{"read", "mcp__docs__search"}}
	m := reloadTestMode(InteractiveModeOptions{
		SubprocessHost:  &orderRecordingHost{},
		SessionHandle:   handle,
		SettingsManager: NewSettingsManager(t.TempDir(), t.TempDir()),
	})
	if err := m.buildSlashContext(t.Context()).Reload(); err != nil {
		t.Fatal(err)
	}
	if handle.reloadRefreshes != 1 {
		t.Fatalf("reload refreshes = %d, want 1", handle.reloadRefreshes)
	}
	if len(handle.loadouts) != 0 {
		t.Fatalf("SetActiveToolsByName calls = %v, want none: reapplying the selection is not a new loadout", handle.loadouts)
	}
	if want := [][]string{{"read", "mcp__docs__search"}}; !slices.EqualFunc(handle.reapplied, want, slices.Equal) {
		t.Fatalf("ReapplyActiveTools calls = %v, want %v", handle.reapplied, want)
	}
}
