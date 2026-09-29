package codingagent

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi 0.87.1 main.ts:943 passes tuiMode: parsed.tuiMode to InteractiveMode, and its constructor (interactive-mode.ts:567-568) selects options.tuiMode ?? settingsManager.getTuiMode() without saving it. The run mode therefore survives any later settings reload, such as the one after project trust is resolved.
func TestInteractiveTuiModeOptionOverridesSettingsForThisRun(t *testing.T) {
	for _, tc := range []struct {
		name, saved, run string
		fullscreen       bool
	}{
		{name: "run fullscreen over saved regular", saved: "regular", run: "fullscreen", fullscreen: true},
		{name: "run regular over saved fullscreen", saved: "fullscreen", run: "regular"},
		{name: "no run mode uses the saved mode", saved: "fullscreen", fullscreen: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agentDir := t.TempDir()
			writeSettingsFixture(t, filepath.Join(agentDir, "settings.json"), `{"tuiMode":"`+tc.saved+`"}`)
			manager := NewSettingsManager(t.TempDir(), agentDir)
			var output bytes.Buffer
			mode := NewInteractiveMode(InteractiveOptions{Settings: manager.Get(), SettingsManager: manager, TuiMode: tc.run, AgentDir: agentDir})
			mode.rendererOut = &output
			handle := mode.createInteractiveTui(t.Context())
			defer handle.cleanup()
			if _, fullscreen := mode.tuiInst.(*tui.TuiAltScreen); fullscreen != tc.fullscreen {
				t.Fatalf("renderer = %T, want fullscreen=%v", mode.tuiInst, tc.fullscreen)
			}
			if got := manager.GetTuiMode(); got != tc.saved {
				t.Fatalf("saved tui mode = %q, want %q", got, tc.saved)
			}
			if mode.opts.Settings.TuiMode != tc.saved {
				t.Fatalf("runtime settings = %q, want saved %q, not the run override", mode.opts.Settings.TuiMode, tc.saved)
			}
			wantMode := tc.run
			if wantMode == "" {
				wantMode = tc.saved
			}
			if mode.opts.TuiMode != wantMode {
				t.Fatalf("captured run mode = %q, want %q", mode.opts.TuiMode, wantMode)
			}
		})
	}
}

// Pi captures the effective mode once in its constructor, even without a CLI override (interactive-mode.ts:567-568). A settings refresh before the renderer is created cannot replace it.
func TestInteractiveTuiModeCapturesDefaultOnce(t *testing.T) {
	for _, initial := range []string{"regular", "fullscreen"} {
		t.Run(initial, func(t *testing.T) {
			cwd, agentDir := t.TempDir(), t.TempDir()
			writeSettingsFixture(t, filepath.Join(agentDir, "settings.json"), `{"tuiMode":"`+initial+`"}`)
			manager := NewSettingsManager(cwd, agentDir)
			mode := NewInteractiveMode(InteractiveOptions{CWD: cwd, AgentDir: agentDir, Settings: manager.Get(), SettingsManager: manager})
			changed := "regular"
			if initial == changed {
				changed = "fullscreen"
			}
			if err := manager.SetTuiMode(changed); err != nil {
				t.Fatal(err)
			}
			mode.opts.Settings = manager.Get()
			var output bytes.Buffer
			mode.rendererOut = &output
			handle := mode.createInteractiveTui(t.Context())
			defer handle.cleanup()
			if (mode.altScreen != nil) != (initial == "fullscreen") {
				t.Fatalf("renderer = %T, want captured mode %s", mode.tuiInst, initial)
			}
		})
	}
}

// Pi keeps options.tuiMode independent of the SettingsManager (interactive-mode.ts:567-568,878,2242). Reloading settings or changing an unrelated setting must not change standalone status layout or the next renderer's mode.
func TestInteractiveTuiModeSurvivesSettingsRefresh(t *testing.T) {
	for _, run := range []string{"regular", "fullscreen"} {
		for _, refresh := range []string{"settings", "reload"} {
			t.Run(run+"/"+refresh, func(t *testing.T) {
				restoreStartupTheme(t)
				saved := "fullscreen"
				if run == saved {
					saved = "regular"
				}
				cwd, agentDir := t.TempDir(), t.TempDir()
				writeSettingsFixture(t, filepath.Join(agentDir, "settings.json"), `{"theme":"dark","clearOnShrink":true,"tuiMode":"`+saved+`"}`)
				manager := NewSettingsManager(cwd, agentDir)
				mode := newSwitchTuiProbeWithOptions(t, InteractiveOptions{CWD: cwd, AgentDir: agentDir, Settings: manager.Get(), SettingsManager: manager, TuiMode: run})
				t.Cleanup(func() { mode.teardownCurrentTui(); mode.stopInteractiveTui(); mode.backgroundTasks.Wait() })
				before := mode.tuiInst
				if refresh == "settings" {
					mode.buildSlashContext(t.Context()).OnSettingApplied("clear-on-shrink", "true")
				} else if err := mode.buildSlashContext(t.Context()).Reload(); err != nil {
					t.Fatal(err)
				}
				if mode.tuiInst != before {
					t.Fatal("settings refresh replaced the renderer")
				}
				mode.tuiInst.SetClearOnShrink(true)
				mode.editor.EmbedWorkingStatus = false
				mode.showStatusIndicator(&tui.StatusIndicator{Kind: "working", Loader: tui.NewLoader("Working")})
				mode.clearStatusIndicator("")
				wantRows := 0
				if run == "regular" {
					wantRows = 2 // Pi IdleStatus.render reserves two rows only on the main screen.
				}
				if got := len(mode.statusContainer.Render(80)); got != wantRows {
					t.Errorf("status rows after %s = %d, want %d for the running %s renderer", refresh, got, wantRows, run)
				}
				if got := manager.GetTuiMode(); got != saved {
					t.Errorf("saved mode = %q, want %q", got, saved)
				}
				if mode.opts.TuiMode != run {
					t.Errorf("run option = %q, want %q", mode.opts.TuiMode, run)
				}
				assertSettingsTuiMode(t, mode, run)
			})
		}
	}
}

// Pi passes this.ui.mode to the settings selector (interactive-mode.ts:4787), not the saved default that a run override or a live switch can supersede.
func assertSettingsTuiMode(t *testing.T, mode *InteractiveMode, want string) {
	t.Helper()
	sc := mode.buildSlashContext(t.Context())
	called := false
	sc.ShowSettingsList = func(items []tui.SettingItem, _ func(string, string) string) {
		called = true
		for _, item := range items {
			if item.ID == "tui-mode" {
				if item.CurrentValue != want {
					t.Errorf("settings TUI mode = %q, want running mode %q", item.CurrentValue, want)
				}
				return
			}
		}
		t.Error("settings omitted TUI mode")
	}
	if err := settingsHandler(sc); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("settings selector not shown")
	}
}

// Pi updates options.tuiMode only after a successful swap (interactive-mode.ts:844-878), including when the project layer masks the saved global mode.
func TestInteractiveTuiModeLiveSwitchOwnsRunMode(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	writeSettingsFixture(t, filepath.Join(agentDir, "settings.json"), `{"tuiMode":"regular"}`)
	writeSettingsFixture(t, filepath.Join(ProjectConfigDir(cwd), "settings.json"), `{"tuiMode":"regular"}`)
	manager := NewSettingsManager(cwd, agentDir)
	mode := newSwitchTuiProbeWithOptions(t, InteractiveOptions{CWD: cwd, AgentDir: agentDir, Settings: manager.Get(), SettingsManager: manager, TuiMode: "fullscreen"})
	t.Cleanup(func() { mode.teardownCurrentTui(); mode.stopInteractiveTui() })
	assertSettingsTuiMode(t, mode, "fullscreen")
	for _, target := range []string{"regular", "fullscreen"} {
		applySettingsTuiMode(t, mode, target, target)
		if mode.opts.TuiMode != target || (mode.altScreen != nil) != (target == "fullscreen") {
			t.Fatalf("live mode = %q renderer = %T, want %s", mode.opts.TuiMode, mode.tuiInst, target)
		}
		assertSettingsTuiMode(t, mode, target)
	}
	mode.tuiInst.OpenOverlay(tui.NewText("overlay"), tui.OverlayOptions{})
	applySettingsTuiMode(t, mode, "regular", "fullscreen")
	if mode.opts.TuiMode != "fullscreen" || mode.altScreen == nil {
		t.Fatal("refused switch changed the running mode")
	}
	assertSettingsTuiMode(t, mode, "fullscreen")
}

func applySettingsTuiMode(t *testing.T, mode *InteractiveMode, target, want string) {
	t.Helper()
	sc := mode.buildSlashContext(t.Context())
	called := false
	sc.ShowSettingsList = func(_ []tui.SettingItem, onChange func(string, string) string) {
		called = true
		if got := onChange("tui-mode", target); got != want {
			t.Errorf("settings change returned %q, want %q", got, want)
		}
	}
	if err := settingsHandler(sc); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("settings selector not shown")
	}
	if got := mode.opts.SettingsManager.GetGlobalSettings().TuiMode; got != want {
		t.Errorf("persisted global mode = %q, want %q", got, want)
	}
}

// chatTexts returns the plain text of each chat text block.
func chatTexts(m *InteractiveMode) []string {
	var texts []string
	for _, child := range m.chatContainer.Children() {
		if text, ok := child.(*tui.Text); ok {
			texts = append(texts, stripANSITest(text.Content))
		}
	}
	return texts
}

// Pi 0.87.1 onTuiModeChange (interactive-mode.ts:4950-4957) switches the renderer before it saves the mode. A refused switch keeps the running mode in the row, reports it with showStatus, and never saves, so the saved default and the settings file are unchanged even when --tui-mode made the running mode differ from the saved one. A successful switch saves the mode and reports "TUI mode: <mode>".
func TestInteractiveTuiModeRefusedSwitchKeepsSavedMode(t *testing.T) {
	for _, tc := range []struct {
		name, global, run, target string
	}{
		{name: "run override differs from the saved mode", global: `{"tuiMode":"regular"}`, run: "fullscreen", target: "regular"},
		{name: "no saved mode", global: `{}`, run: "regular", target: "fullscreen"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd, agentDir := t.TempDir(), t.TempDir()
			globalPath := filepath.Join(agentDir, "settings.json")
			writeSettingsFixture(t, globalPath, tc.global)
			manager := NewSettingsManager(cwd, agentDir)
			mode := newSwitchTuiProbeWithOptions(t, InteractiveOptions{CWD: cwd, AgentDir: agentDir, Settings: manager.Get(), SettingsManager: manager, TuiMode: tc.run})
			t.Cleanup(func() { mode.teardownCurrentTui(); mode.stopInteractiveTui() })
			before, err := os.ReadFile(globalPath)
			if err != nil {
				t.Fatal(err)
			}
			mode.tuiInst.OpenOverlay(tui.NewText("overlay"), tui.OverlayOptions{})
			sc := mode.buildSlashContext(t.Context())
			sc.ShowSettingsList = func(_ []tui.SettingItem, onChange func(string, string) string) {
				if got := onChange("tui-mode", tc.target); got != tc.run {
					t.Errorf("refused change returned %q, want the running mode %q", got, tc.run)
				}
			}
			if err := settingsHandler(sc); err != nil {
				t.Fatal(err)
			}
			if after, err := os.ReadFile(globalPath); err != nil || !bytes.Equal(after, before) {
				t.Errorf("settings file = %s err=%v, want the unchanged %s", after, err, before)
			}
			if mode.opts.TuiMode != tc.run || (mode.altScreen != nil) != (tc.run == "fullscreen") {
				t.Errorf("refused switch changed the running mode to %q (%T)", mode.opts.TuiMode, mode.tuiInst)
			}
			if got, want := chatTexts(mode), []string{"Close active overlays before changing TUI mode"}; !slices.Equal(got, want) {
				t.Errorf("chat = %q, want the status %q", got, want)
			}
		})
	}
}

func TestInteractiveTuiModeSwitchReportsStatus(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	writeSettingsFixture(t, filepath.Join(agentDir, "settings.json"), `{"tuiMode":"regular"}`)
	manager := NewSettingsManager(cwd, agentDir)
	mode := newSwitchTuiProbeWithOptions(t, InteractiveOptions{CWD: cwd, AgentDir: agentDir, Settings: manager.Get(), SettingsManager: manager})
	t.Cleanup(func() { mode.teardownCurrentTui(); mode.stopInteractiveTui() })
	mode.statusContainer.Add(&tui.IdleStatus{})
	applySettingsTuiMode(t, mode, "fullscreen", "fullscreen")
	if got, want := chatTexts(mode), []string{"TUI mode: fullscreen"}; !slices.Equal(got, want) {
		t.Errorf("chat = %q, want the status %q", got, want)
	}
	if got := len(mode.statusContainer.Children()); got != 0 {
		t.Errorf("status container keeps %d children, want it cleared without an active indicator", got)
	}
}
