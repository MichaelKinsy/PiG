package codingagent

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

type themeSelectionCase struct {
	name, global, project string
	initial               *string
	queried               bool
	theme, err, saved     string
}

// Pi 0.87.1 theme-controller.ts:57-81 applies any defined selection as a fixed theme without querying the terminal. theme.ts:790-811 rejects the empty name, and theme-controller.ts:132-140 reports it and falls back to dark. Only an undefined selection, including one a project null leaves, detects and persists the terminal background.
var themeSelectionCases = []themeSelectionCase{
	{name: "saved empty", global: `{"theme":""}`, theme: "dark", err: "Failed to load theme \"\": Theme not found: \nFell back to dark theme.", saved: `""`},
	{name: "initial empty over saved light", global: `{"theme":"light"}`, initial: new(""), theme: "dark", err: "Failed to load theme \"\": Theme not found: \nFell back to dark theme.", saved: `"light"`},
	{name: "project null over saved dark", global: `{"theme":"dark"}`, project: `{"theme":null}`, queried: true, theme: "light", saved: `"light"`},
	// theme-controller.ts:137 interpolates the name into a template string, so quotes, backslashes, and control characters appear verbatim.
	{name: "initial name with quotes and a backslash", global: `{"theme":"light"}`, initial: new(`say "hi" \ ok`), theme: "dark", err: `Failed to load theme "say "hi" \ ok": Theme not found: say "hi" \ ok` + "\nFell back to dark theme.", saved: `"light"`},
	{name: "saved name with a control character", global: `{"theme":"odd\u0001name"}`, theme: "dark", err: "Failed to load theme \"odd\x01name\": Theme not found: odd\x01name\nFell back to dark theme.", saved: `"odd\u0001name"`},
}

// chatErrors returns the text of each error block showError appended, without its "Error: " prefix and color.
func chatErrors(m *InteractiveMode) []string {
	var errs []string
	for _, child := range m.chatContainer.Children() {
		if text, ok := child.(*tui.Text); ok {
			if content, isError := strings.CutPrefix(stripANSITest(text.Content), "Error: "); isError {
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
			t.Setenv("COLORFGBG", "0;15")
			cwd, agentDir := writeThemeLayers(t, tc.global, tc.project)
			manager := NewSettingsManager(cwd, agentDir)
			m := NewInteractiveMode(InteractiveOptions{
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
			m.inputReadCh = make(chan inputChunk, 1)
			m.inputErrCh = make(chan error, 1)
			m.inputReadCh <- inputChunk{data: []byte("\x1b]11;#ffffff\x07")}
			var output bytes.Buffer
			setThemeQueryTestOutput(m, &output)
			t.Cleanup(func() { m.disposeTheme(); m.backgroundTasks.Wait() })
			tui.SetThemeSettingPresence(m.getThemeSelection())
			if err := m.initializeTerminalTheme(ctx, &output); err != nil {
				t.Fatal(err)
			}
			if queried := strings.Contains(output.String(), terminalBackgroundQuery); queried != tc.queried {
				t.Errorf("terminal queried=%v (output %q), want %v", queried, output.String(), tc.queried)
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
			if got := storedTheme(t, filepath.Join(agentDir, "settings.json")); got != tc.saved {
				t.Errorf("saved theme = %s, want %s", got, tc.saved)
			}
		})
	}
}

// Real Pi 0.87.1 InteractiveThemeController produces the expected table for the same settings files, environment, and terminal reply.
func TestInteractiveThemeSelectionPresenceMatchesPi(t *testing.T) {
	t.Setenv("COLORFGBG", "0;15")
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
    async queryTerminalBackgroundColor() { log.push('osc11'); return {r: 255, g: 255, b: 255}; },
    async queryTerminalColorScheme() { log.push('scheme'); return undefined; },
  };
  const sm = SettingsManager.create(cwd, agent);
  const controller = new InteractiveThemeController(ui, {getSettingsManager: () => sm, showError: m => errors.push(m), onChanged() {}, initialThemeSetting: initial});
  await controller.applyFromSettings();
  await sm.flush();
  controller.dispose();
  const saved = JSON.stringify(JSON.parse(readFileSync(join(agent, 'settings.json'), 'utf8')).theme);
  results.push({queried: log.includes('osc11'), theme: T.theme.name, err: errors.join('|'), saved});
}
T.stopThemeWatcher();
console.log(JSON.stringify(results));
`, args...)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi: %v\n%s", err, output)
	}
	var results []struct {
		Queried bool   `json:"queried"`
		Theme   string `json:"theme"`
		Err     string `json:"err"`
		Saved   string `json:"saved"`
	}
	if err := json.Unmarshal(output, &results); err != nil || len(results) != len(themeSelectionCases) {
		t.Fatalf("Pi: %s: %v", output, err)
	}
	for i, tc := range themeSelectionCases {
		if pi := results[i]; pi.Queried != tc.queried || pi.Theme != tc.theme || pi.Err != tc.err || pi.Saved != tc.saved {
			t.Errorf("%s: Pi = %+v, want queried=%v theme=%q err=%q saved=%s", tc.name, pi, tc.queried, tc.theme, tc.err, tc.saved)
		}
	}
}

// Pi 0.87.1 startup-ui.ts:83-107: an explicitly empty theme resolves to itself (theme.ts:597-608), so createStartupTui's initTheme("") and the detected setTheme("") both fall back to dark. The empty string is falsy, so the prompt still queries the terminal.
func TestStartupPromptExplicitEmptyThemeFallsBackToDark(t *testing.T) {
	restoreStartupTheme(t)
	t.Setenv("COLORFGBG", "0;15")
	var settings Settings
	if err := json.Unmarshal([]byte(`{"theme":""}`), &settings); err != nil {
		t.Fatal(err)
	}
	configureStartupTheme(settings, nil)
	if got := tui.ActiveTheme().Name; got != "dark" {
		t.Fatalf("initial theme = %q, want the dark fallback for the empty name", got)
	}
	terminal := &fakeStartupTerminal{replies: [][]byte{
		[]byte("\x1b]11;rgb:ffff/ffff/ffff\x07"),
		[]byte("\x1b[?997;2n"),
		[]byte("h"),
		[]byte("\r"),
	}}
	input := tui.NewExtensionInputComponent("Name", "")
	completed, err := runStartupComponentWith(input, StartupUIOptions{Settings: settings}, false, tui.NewWithOutput(startupBackgroundWriter{terminal}, 80, 24), terminal, map[string]string{"COLORFGBG": "0;15"})
	if err != nil || !completed {
		t.Fatalf("completed=%v err=%v", completed, err)
	}
	if got := input.Text(); got != "h" {
		t.Fatalf("input = %q", got)
	}
	if got := tui.ActiveTheme().Name; got != "dark" {
		t.Fatalf("theme = %q, want dark although the terminal reported light", got)
	}
	if writes := terminal.written(); len(writes) != 2 || writes[0] != terminalColorSchemeQuery || writes[1] != terminalBackgroundQuery {
		t.Fatalf("writes = %q, want both appearance queries", writes)
	}
}

// Pi 0.87.1 startup-ui.ts:86-87 resolves a malformed slash setting to undefined (theme.ts:597-608) and initializes the environment theme. startup-ui.ts:100-101 then skips detection because the setting is a nonempty non-automatic string.
func TestStartupPromptMalformedThemeKeepsEnvironmentTheme(t *testing.T) {
	restoreStartupTheme(t)
	t.Setenv("COLORFGBG", "0;15")
	settings := Settings{Theme: "light/dark/extra"}
	configureStartupTheme(settings, nil)
	if got := tui.ActiveTheme().Name; got != "light" {
		t.Fatalf("initial theme = %q, want the light environment theme", got)
	}
	terminal := &fakeStartupTerminal{}
	selector := tui.NewExtensionSelector("Pick", []string{"a"})
	done := make(chan error, 1)
	go func() {
		_, err := runStartupComponentWith(selector, StartupUIOptions{Settings: settings}, false, tui.NewWithOutput(io.Discard, 80, 24), terminal, map[string]string{"COLORFGBG": "0;15"})
		done <- err
	}()
	terminal.send(t, "\r")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the prompt did not complete")
	}
	if writes := terminal.written(); len(writes) != 0 {
		t.Fatalf("writes = %q; a nonempty non-automatic setting must not query the terminal", writes)
	}
	if got := tui.ActiveTheme().Name; got != "light" {
		t.Fatalf("theme = %q, want the light environment theme", got)
	}
}
