package codingagent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

type themeLoadFailureCase struct {
	name string
	// content is <agent directory>/themes/proto.json as written; mutate changes the built-in dark theme named "proto" instead. directory puts a directory there.
	content   string
	mutate    func(document map[string]any)
	directory bool
	// theme is the active theme after startup; reason is what follows `Failed to load theme "proto": `, or "" when the theme loads.
	theme, reason string
}

func themeColors(mutate func(colors map[string]any)) func(map[string]any) {
	return func(document map[string]any) { mutate(document["colors"].(map[string]any)) }
}

// upstream theme-controller.ts:178-186 applyThemeName prints `Failed to load theme "<name>": ${result.error}`, and theme.ts setTheme (703-724) takes that error from loadThemeJson (487-507):
// the file <agent directory>/themes/<name>.json is read whenever it exists, so a file that fails to load repeats the loader's reason, labelled with the name, and a valid file loads by its file name.
// Only a name without such a file is "Theme not found". TestThemeLoadFailureReasonMatchesPi runs the real controller over the same files.
var themeLoadFailureCases = []themeLoadFailureCase{
	{name: "valid file with another name", mutate: func(document map[string]any) { document["name"] = "other" }, theme: "other"},
	{name: "directory in place of the file", directory: true, theme: "system", reason: "EISDIR: illegal operation on a directory, read"},
	{name: "invalid JSON", content: `{"name": "proto",`, theme: "system", reason: "Failed to parse theme proto: SyntaxError: Expected double-quoted property name in JSON at position 17 (line 1 column 18)"},
	{name: "not an object", content: `[1,2]`, theme: "system", reason: "Invalid theme \"proto\":\n\n\nOther errors:\n  - /: must be object"},
	{name: "no colors", content: `{"name":"proto"}`, theme: "system", reason: "Invalid theme \"proto\":\n\n\nOther errors:\n  - /: must have required properties colors"},
	{name: "missing required color", mutate: themeColors(func(colors map[string]any) { delete(colors, "accent") }), theme: "system", reason: "Invalid theme \"proto\":\n\nMissing required color tokens:\n  - accent\n\nPlease add these colors to your theme's \"colors\" object.\nSee the built-in themes (dark.json, light.json) for reference values."},
	{name: "color of the wrong type", mutate: themeColors(func(colors map[string]any) { colors["accent"] = true }), theme: "system", reason: "Invalid theme \"proto\":\n\n\nOther errors:\n  - /colors/accent: must be string\n  - /colors/accent: must be integer\n  - /colors/accent: must match a schema in anyOf"},
	{name: "unknown variable", mutate: themeColors(func(colors map[string]any) { colors["accent"] = "nosuchvar" }), theme: "system", reason: "Variable reference not found: nosuchvar"},
	{name: "circular variable", mutate: func(document map[string]any) {
		vars := document["vars"].(map[string]any)
		vars["loopa"], vars["loopb"] = "loopb", "loopa"
		document["colors"].(map[string]any)["accent"] = "loopa"
	}, theme: "system", reason: "Circular variable reference detected: loopa"},
	{name: "name with a slash", mutate: func(document map[string]any) { document["name"] = "a/b" }, theme: "system", reason: `Invalid theme name "a/b": theme names cannot contain "/" because it is reserved for automatic light/dark theme settings.`},
}

// writeThemeLoadFailure writes settings.json selecting "proto" and the case's themes/proto.json, and returns the working and agent directories.
func writeThemeLoadFailure(t *testing.T, tc themeLoadFailureCase) (cwd, agentDir string) {
	t.Helper()
	cwd, agentDir = writeThemeLayers(t, `{"theme":"proto"}`, "")
	themesDir := filepath.Join(agentDir, "themes")
	path := filepath.Join(themesDir, "proto.json")
	if err := os.MkdirAll(themesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := []byte(tc.content)
	switch {
	case tc.directory:
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		return cwd, agentDir
	case tc.mutate != nil:
		data, err := os.ReadFile(filepath.Join("..", "..", "tui", "theme_dark.json"))
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatal(err)
		}
		document["name"] = "proto"
		tc.mutate(document)
		if content, err = json.Marshal(document); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return cwd, agentDir
}

// Startup with settings.json naming a theme whose file in the agent directory fails to load: the error line carries the loader's reason, as Pi's does.
func TestStartupThemeLoadFailureRepeatsTheLoadersReason(t *testing.T) {
	for _, tc := range themeLoadFailureCases {
		t.Run(tc.name, func(t *testing.T) {
			restoreStartupTheme(t)
			t.Setenv("COLORFGBG", "15;0")
			cwd, agentDir := writeThemeLoadFailure(t, tc)
			manager := NewSettingsManager(cwd, agentDir)
			m := NewInteractiveMode(nil, InteractiveModeOptions{
				CWD: cwd, AgentDir: agentDir,
				Settings: manager.Get(), SettingsManager: manager,
				// The resource loader resolves the agent directory's themes directory (package-manager.ts user resources).
				ThemePaths: []string{filepath.Join(agentDir, "themes")},
			})
			ctx := t.Context()
			var output bytes.Buffer
			m.themeState.output = &output
			m.tuiInst = tui.NewWithOutput(&output, 100, 30)
			m.editor = tui.NewEditor()
			m.chatContainer = tui.NewContainer()
			m.keybindings = DefaultKeybindingsManager()
			m.backgroundCtx = ctx
			m.installRenderDispatcher()
			t.Cleanup(func() { m.disposeTheme(); m.backgroundTasks.Wait() })
			m.loadThemes()
			m.initTheme()
			m.applyThemeFromSettings(ctx)
			if got := tui.ActiveTheme().Name; got != tc.theme {
				t.Errorf("theme = %q, want %q", got, tc.theme)
			}
			var want []string
			if tc.reason != "" {
				want = []string{"Failed to load theme \"proto\": " + tc.reason + "\nFell back to the system theme."}
			}
			if got := chatErrors(m); !slices.Equal(got, want) {
				t.Errorf("errors = %q\nwant     %q", got, want)
			}
		})
	}
}

// An extension's ctx.ui.setTheme answers with the same reason (interactive-mode.ts extension setTheme returns the controller's result): ExtUIContext.SetTheme is the one host
// function behind every SDK's setTheme, so the reason is host data for all of them.
func TestExtensionSetThemeRepeatsTheLoadersReason(t *testing.T) {
	for _, tc := range themeLoadFailureCases {
		t.Run(tc.name, func(t *testing.T) {
			restoreStartupTheme(t)
			_, agentDir := writeThemeLoadFailure(t, tc)
			m := &InteractiveMode{opts: InteractiveModeOptions{AgentDir: agentDir, ThemePaths: []string{filepath.Join(agentDir, "themes")}}}
			m.loadThemes()
			got := (&ExtUIContext{m: m}).SetTheme(extension.ThemeName("proto"))
			if want := (extension.SetThemeResult{Success: tc.reason == "", Error: tc.reason}); got != want {
				t.Errorf("SetTheme = %+v\nwant       %+v", got, want)
			}
			if active := tui.ActiveTheme().Name; active != tc.theme {
				t.Errorf("theme = %q, want %q", active, tc.theme)
			}
		})
	}
}

// The real upstream InteractiveThemeController, with main.ts's theme validator and no registered theme (a file that fails is not registered; the valid one is registered as "other"), prints the table's reasons for the same files.
func TestThemeLoadFailureReasonMatchesPi(t *testing.T) {
	t.Setenv("COLORFGBG", "15;0")
	var args []string
	for _, tc := range themeLoadFailureCases {
		cwd, agentDir := writeThemeLoadFailure(t, tc)
		args = append(args, agentDir, cwd)
	}
	cmd := piDirectoryNode(t, `
const T = await load('modes/interactive/theme/theme');
const {validateThemeJson} = await load('modes/interactive/theme/theme-schema');
const {InteractiveThemeController} = await load('modes/interactive/theme/theme-controller');
T.setThemeJsonValidator(validateThemeJson);
const cases = process.argv.slice(2), results = [];
for (let i = 0; i < cases.length; i += 2) {
  const [agent, cwd] = cases.slice(i, i + 2);
  process.env.PI_CODING_AGENT_DIR = agent;
  T.setRegisteredThemes([]);
  const errors = [];
  const ui = {
    invalidate() {}, requestRender() {},
    setTerminalColorSchemeNotifications() {},
    onTerminalColorSchemeChange() { return () => {}; },
    async queryTerminalColors() { return {}; },
  };
  const sm = SettingsManager.create(cwd, agent);
  const controller = new InteractiveThemeController(ui, {getSettingsManager: () => sm, showError: m => errors.push(m), onChanged() {}});
  controller.applyFromSettings();
  await controller.waitForTerminalColors();
  controller.dispose();
  results.push({theme: T.theme.name, err: errors.join('|')});
}
T.stopThemeWatcher();
console.log(JSON.stringify(results));
`, args...)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi: %v\n%s", err, output)
	}
	var results []struct {
		Theme string `json:"theme"`
		Err   string `json:"err"`
	}
	if err := json.Unmarshal(output, &results); err != nil || len(results) != len(themeLoadFailureCases) {
		t.Fatalf("Pi: %s: %v", output, err)
	}
	for i, tc := range themeLoadFailureCases {
		want := ""
		if tc.reason != "" {
			want = "Failed to load theme \"proto\": " + tc.reason + "\nFell back to the system theme."
		}
		if pi := results[i]; pi.Theme != tc.theme || pi.Err != want {
			t.Errorf("%s: Pi = %+v, want theme=%q err=%q", tc.name, pi, tc.theme, want)
		}
	}
}
