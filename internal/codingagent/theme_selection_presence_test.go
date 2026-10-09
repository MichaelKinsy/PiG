package codingagent

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

type themeSelectionCase struct {
	name, global, project string
	initial               *string
	notify                bool
	theme, err            string
}

// upstream 0.99.1 theme-controller.ts applyFromSettings and theme.ts setTheme: every selection applies without persisting the terminal's appearance, an empty or unknown name is reported and falls back to the system theme, and the system theme and theme pairs follow the terminal's appearance.
var themeSelectionCases = []themeSelectionCase{
	{name: "saved empty", global: `{"theme":""}`, theme: "system", err: "Failed to load theme \"\": Theme not found: \nFell back to the system theme."},
	{name: "initial empty over saved light", global: `{"theme":"light"}`, initial: new(""), theme: "system", err: "Failed to load theme \"\": Theme not found: \nFell back to the system theme."},
	{name: "project null over saved dark", global: `{"theme":"dark"}`, project: `{"theme":null}`, notify: true, theme: "system"},
	{name: "saved fixed dark", global: `{"theme":"dark"}`, theme: "dark"},
	{name: "saved pair", global: `{"theme":"light/dark"}`, notify: true, theme: "dark"},
	{name: "saved malformed slash setting", global: `{"theme":"light/dark/extra"}`, notify: true, theme: "system"},
	{name: "initial name with quotes and a backslash", global: `{"theme":"light"}`, initial: new(`say "hi" \ ok`), theme: "system", err: `Failed to load theme "say "hi" \ ok": Theme not found: say "hi" \ ok` + "\nFell back to the system theme."},
	{name: "saved name with a control character", global: `{"theme":"odd\u0001name"}`, theme: "system", err: "Failed to load theme \"odd\x01name\": Theme not found: odd\x01name\nFell back to the system theme."},
}

// chatErrors returns the text of each error block showError appended, without its "Error: " prefix and color.
// chatTextContent is the string a chat text child shows. A ThemedText builds its string when it renders (themed-text.ts).
func chatTextContent(child tui.Component) (string, bool) {
	switch text := child.(type) {
	case *tui.Text:
		return text.Content, true
	case *tui.ThemedText:
		text.Render(1000)
		return text.Content, true
	}
	return "", false
}

// lastStatusContent is the string of the mode's coalescing status line.
func lastStatusContent(m *InteractiveMode) string {
	content, _ := chatTextContent(m.lastStatusText)
	return content
}

func chatErrors(m *InteractiveMode) []string {
	var errs []string
	for _, child := range m.chatContainer.Children() {
		if content, ok := chatTextContent(child); ok {
			if content, isError := strings.CutPrefix(stripANSITest(content), "Error: "); isError {
				errs = append(errs, content)
			}
		}
	}
	return errs
}

func TestInteractiveThemeSelectionPresence(t *testing.T) {
	for _, tc := range themeSelectionCases {
		t.Run(tc.name, func(t *testing.T) {
			restoreStartupTheme(t)
			t.Setenv("COLORFGBG", "15;0")
			cwd, agentDir := writeThemeLayers(t, tc.global, tc.project)
			manager := NewSettingsManager(cwd, agentDir)
			m := NewInteractiveMode(nil, InteractiveModeOptions{
				CWD: cwd, AgentDir: agentDir,
				Settings: manager.Get(), SettingsManager: manager, InitialThemeSetting: tc.initial,
			})
			ctx := t.Context()
			m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
			m.editor = tui.NewEditor()
			m.chatContainer = tui.NewContainer()
			m.keybindings = DefaultKeybindingsManager()
			m.installRenderDispatcher()
			m.backgroundCtx = ctx
			var output bytes.Buffer
			m.themeState.output = &output
			m.tuiInst = tui.NewWithOutput(&output, 100, 30)
			// The replacement renderer schedules its renders on the owner loop, as the production renderer does.
			m.installRenderDispatcher()
			t.Cleanup(func() { m.disposeTheme(); m.backgroundTasks.Wait() })
			m.initTheme()
			m.applyThemeFromSettings(ctx)
			if notified := strings.Contains(output.String(), "\x1b[?2031h"); notified != tc.notify {
				t.Errorf("color-scheme notifications enabled=%v (output %q), want %v", notified, output.String(), tc.notify)
			}
			if got := tui.ActiveTheme().Name; got != tc.theme {
				t.Errorf("theme = %q, want %q", got, tc.theme)
			}
			var want []string
			if tc.err != "" {
				want = []string{tc.err}
			}
			if got := chatErrors(m); !slices.Equal(got, want) {
				t.Errorf("errors = %q, want %q", got, want)
			}
		})
	}
}

// Real upstream 0.99.1 InteractiveThemeController produces the expected table for the same settings files, environment, and terminal answer.
func TestInteractiveThemeSelectionPresenceMatchesPi(t *testing.T) {
	t.Setenv("COLORFGBG", "15;0")
	var args []string
	for _, tc := range themeSelectionCases {
		cwd, agentDir := writeThemeLayers(t, tc.global, tc.project)
		initial := "null"
		if tc.initial != nil {
			encoded, err := json.Marshal(*tc.initial)
			if err != nil {
				t.Fatal(err)
			}
			initial = string(encoded)
		}
		args = append(args, agentDir, cwd, initial)
	}
	cmd := piDirectoryNode(t, `
const T = await load('modes/interactive/theme/theme');
const {InteractiveThemeController} = await load('modes/interactive/theme/theme-controller');
const cases = process.argv.slice(2), results = [];
for (let i = 0; i < cases.length; i += 3) {
  const [agent, cwd] = cases.slice(i, i + 2);
  const initial = JSON.parse(cases[i + 2]) ?? undefined;
  const log = [], errors = [];
  const ui = {
    invalidate() {}, requestRender() {},
    setTerminalColorSchemeNotifications(enabled) { log.push('notify:' + enabled); },
    onTerminalColorSchemeChange() { return () => {}; },
    async queryTerminalColors() { return {}; },
  };
  const sm = SettingsManager.create(cwd, agent);
  const controller = new InteractiveThemeController(ui, {getSettingsManager: () => sm, showError: m => errors.push(m), onChanged() {}, initialThemeSetting: initial});
  controller.applyFromSettings();
  await controller.waitForTerminalColors();
  controller.dispose();
  results.push({notify: log.includes('notify:true'), theme: T.theme.name, err: errors.join('|')});
}
T.stopThemeWatcher();
console.log(JSON.stringify(results));
`, args...)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi: %v\n%s", err, output)
	}
	var results []struct {
		Notify bool   `json:"notify"`
		Theme  string `json:"theme"`
		Err    string `json:"err"`
	}
	if err := json.Unmarshal(output, &results); err != nil || len(results) != len(themeSelectionCases) {
		t.Fatalf("Pi: %s: %v", output, err)
	}
	for i, tc := range themeSelectionCases {
		if pi := results[i]; pi.Notify != tc.notify || pi.Theme != tc.theme || pi.Err != tc.err {
			t.Errorf("%s: Pi = %+v, want notify=%v theme=%q err=%q", tc.name, pi, tc.notify, tc.theme, tc.err)
		}
	}
}

// upstream 0.99.1 startup-ui.ts createStartupTui and theme.ts resolveThemeSetting: an explicitly empty theme resolves to itself, which setTheme rejects, so the prompt starts on the system theme and stays there once the terminal's colors arrive.
func TestStartupPromptExplicitEmptyThemeFallsBackToSystem(t *testing.T) {
	restoreStartupTheme(t)
	var settings Settings
	if err := json.Unmarshal([]byte(`{"theme":""}`), &settings); err != nil {
		t.Fatal(err)
	}
	configureStartupTheme(settings, nil)
	if got := tui.ActiveTheme().Name; got != tui.SystemThemeName {
		t.Fatalf("initial theme = %q, want the system fallback for the empty name", got)
	}
	terminal := &fakeStartupTerminal{replies: append(whiteTerminalReplies(), []byte("h"), []byte("\r"))}
	input := tui.NewExtensionInputComponent("Name", "", nil, nil)
	completed, err := runStartupComponentWith(input, StartupUIOptions{Settings: settings}, false, tui.NewWithOutput(startupQueryWriter{terminal}, 80, 24), terminal)
	if err != nil || !completed {
		t.Fatalf("completed=%v err=%v", completed, err)
	}
	if got := input.Text(); got != "h" {
		t.Fatalf("input = %q", got)
	}
	if got := tui.ActiveTheme().Name; got != tui.SystemThemeName {
		t.Fatalf("theme = %q, want system", got)
	}
}

// upstream 0.99.1 theme.ts resolveThemeSetting returns undefined for a malformed slash setting, so startup uses the system theme.
func TestStartupPromptMalformedThemeUsesSystemTheme(t *testing.T) {
	restoreStartupTheme(t)
	settings := Settings{Theme: "light/dark/extra"}
	configureStartupTheme(settings, nil)
	if got := tui.ActiveTheme().Name; got != tui.SystemThemeName {
		t.Fatalf("initial theme = %q, want the system theme", got)
	}
	terminal := &fakeStartupTerminal{replies: append(whiteTerminalReplies(), []byte("\r"))}
	selector := tui.NewExtensionSelectorComponent("Pick", []string{"a"}, nil, nil)
	done := make(chan error, 1)
	go func() {
		_, err := runStartupComponentWith(selector, StartupUIOptions{Settings: settings}, false, tui.NewWithOutput(startupQueryWriter{terminal}, 80, 24), terminal)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the prompt did not complete")
	}
	if got := tui.ActiveTheme().Name; got != tui.SystemThemeName {
		t.Fatalf("theme = %q, want the system theme", got)
	}
}
