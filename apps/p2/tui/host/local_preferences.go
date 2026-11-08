package host

// Trusted launcher: reads the visitor's own pi files and p2 prefs, validates them and hands the TUI data only (p2-tui-port.md section 4.2). pi's
// files are only ever read. Paths, env and launcher-only keys (external editor, mouse policy, download folder) never leave this process.
//
// pi: apps/p2/src/hosts/tui-local-preferences.ts

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	pitui "github.com/MichaelKinsy/PiG/tui"
	"github.com/fsnotify/fsnotify"
)

const maxThemeBytes = 1024 * 1024

// MousePolicy is tui-modes.ts MousePolicy: which mouse reporting the visitor's terminal gets.
type MousePolicy string

// The three MousePolicy values.
const (
	MouseOff MousePolicy = "off"
	MouseOn  MousePolicy = "on"
	MouseAll MousePolicy = "all"
)

// LocalPreferenceSources are where the preferences are read from.
type LocalPreferenceSources struct {
	// AgentDir is pi's agent directory (`PI_CODING_AGENT_DIR` or `~/.pi/agent`).
	AgentDir string
	// ConfigDir is p2's launcher directory (`P2_TUI_CONFIG_DIR` or `~/.pi/p2-tui`), holding `prefs.json`.
	ConfigDir string
	// Theme is `--theme`: this run's theme setting.
	Theme string
	// NoThemes is `--no-themes`: skip custom theme files.
	NoThemes bool
}

// LocalPreferenceOptions are the `--theme` and `--no-themes` options.
type LocalPreferenceOptions struct {
	Theme    string
	NoThemes bool
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// expandHome is the `~` and `~/` expansion the TypeScript applies to configured directories.
func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		return filepath.Join(homeDir(), path[1:])
	}
	return path
}

// ConfigDirectory is tui-config.ts configDirectory: `P2_TUI_CONFIG_DIR` or `~/.pi/p2-tui`.
func ConfigDirectory() string {
	if configured := os.Getenv("P2_TUI_CONFIG_DIR"); configured != "" {
		return configured
	}
	return filepath.Join(homeDir(), ".pi", "p2-tui")
}

// NewLocalPreferenceSources is localPreferenceSources over the process environment.
func NewLocalPreferenceSources(options LocalPreferenceOptions) LocalPreferenceSources {
	agentDir := filepath.Join(homeDir(), ".pi", "agent")
	if configured := os.Getenv("PI_CODING_AGENT_DIR"); configured != "" {
		agentDir = expandHome(configured)
	}
	return LocalPreferenceSources{AgentDir: agentDir, ConfigDir: ConfigDirectory(), Theme: options.Theme, NoThemes: options.NoThemes}
}

// readBounded is a bounded read of one regular file through its descriptor; ok is false if absent.
func readBounded(path string, limit int) (text []byte, ok bool, err error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, errors.New("not a regular file")
	}
	tooLarge := fmt.Errorf("larger than %d KiB", limit/1024)
	if info.Size() > int64(limit) {
		return nil, false, tooLarge
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > limit {
		return nil, false, tooLarge
	}
	return []byte(strings.TrimPrefix(string(data), "\ufeff")), true, nil
}

// readJSON reads and parses one file; ok is false when it is absent or unreadable (a problem is reported for the latter).
func readJSON(path, label string, problems *[]string) (value any, raw []byte, ok bool) {
	data, found, err := readBounded(path, prefs.MaxPreferenceFile)
	if err == nil && found {
		var parsed any
		if jsonErr := json.Unmarshal(data, &parsed); jsonErr != nil {
			*problems = append(*problems, label+": invalid JSON")
			return nil, nil, false
		}
		return parsed, data, true
	}
	if err != nil {
		*problems = append(*problems, label+": "+err.Error())
	}
	return nil, nil, false
}

func record(value any) map[string]any {
	if object, ok := value.(map[string]any); ok {
		return object
	}
	return map[string]any{}
}

// objectPrototypeKeys are the names `name in BUILTIN_THEMES` is true for besides the built-in themes: Object.prototype's members.
var objectPrototypeKeys = []string{
	"constructor", "toString", "toLocaleString", "valueOf", "hasOwnProperty", "isPrototypeOf", "propertyIsEnumerable", "__proto__",
	"__defineGetter__", "__defineSetter__", "__lookupGetter__", "__lookupSetter__",
}

func isBuiltinThemeName(name string) bool {
	_, builtin := theme.BuiltinTheme(name)
	return builtin || slices.Contains(objectPrototypeKeys, name)
}

// jsSlice is `text.slice(0, n)` over UTF-16 code units.
func jsSlice(text string, n int) string {
	units := utf16.Encode([]rune(text))
	if len(units) <= n {
		return text
	}
	return string(utf16.Decode(units[:n]))
}

func customThemes(directory string, problems *[]string) map[string]theme.ThemeJSON {
	themes := map[string]theme.ThemeJSON{}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return themes
	}
	var files []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			files = append(files, entry.Name())
		}
	}
	slices.Sort(files)
	total := 0
	for _, file := range files {
		label := "themes/" + jsSlice(file, 64)
		if len(themes) >= prefs.MaxCustomThemes {
			*problems = append(*problems, fmt.Sprintf("themes: more than %d files; the rest are skipped", prefs.MaxCustomThemes))
			break
		}
		_, raw, ok := readJSON(filepath.Join(directory, file), label, problems)
		if !ok {
			continue
		}
		parsed, err := theme.ValidateThemeJSON(label, raw)
		if err == nil {
			encoded, _ := json.Marshal(parsed)
			size := len(utf16.Encode([]rune(string(encoded))))
			switch {
			case total+size > maxThemeBytes:
				err = errors.New("custom themes exceed 1 MiB in total")
			// pi: built-in names (and the reserved system name) win over custom files.
			case isBuiltinThemeName(parsed.Name) || parsed.Name == theme.SystemThemeName || !prefs.IsThemeSetting(parsed.Name):
				err = fmt.Errorf("theme name \"%s\" is reserved or invalid", jsSlice(parsed.Name, 64))
			default:
				total += size
				themes[parsed.Name] = parsed
			}
		}
		if err != nil {
			first, _, _ := strings.Cut(err.Error(), "\n")
			*problems = append(*problems, label+": "+first)
		}
	}
	return themes
}

// LoadLocalPreferences is loadLocalPreferences: pi files first, then p2 prefs; only whitelisted keys are kept (section 4.2 flow step 1).
func LoadLocalPreferences(sources LocalPreferenceSources) prefs.TuiLocalPreferences {
	problems := []string{}
	keybindings := pitui.KeybindingsConfig{}
	piBindingsDoc, _, _ := readJSON(filepath.Join(sources.AgentDir, "keybindings.json"), "keybindings.json", &problems)
	piBindings, invalid := prefs.SanitizeKeybindings(piBindingsDoc)
	for _, id := range invalid {
		problems = append(problems, "keybindings.json: invalid binding "+id)
	}
	for id, keys := range piBindings {
		keybindings[id] = keys
	}
	piSettingsDoc, _, _ := readJSON(filepath.Join(sources.AgentDir, "settings.json"), "settings.json", &problems)
	piSettings := record(piSettingsDoc)
	prefsDoc, _, _ := readJSON(filepath.Join(sources.ConfigDir, "prefs.json"), "prefs.json", &problems)
	prefsFile := record(prefsDoc)
	prefBindings, invalid := prefs.SanitizeKeybindings(prefsFile["keybindings"])
	for _, id := range invalid {
		problems = append(problems, "prefs.json: invalid binding "+id)
	}
	for id, keys := range prefBindings {
		keybindings[id] = keys
	}
	var layers []prefs.TuiSettings
	for _, layer := range []struct {
		label string
		raw   map[string]any
	}{{"settings.json", piSettings}, {"prefs.json", record(prefsFile["settings"])}} {
		valid, invalidKeys := prefs.SanitizeSettings(layer.raw)
		// pi's settings.json holds much more than display keys; only report bad values of keys p2 uses.
		for _, key := range invalidKeys {
			problems = append(problems, layer.label+": invalid "+key)
		}
		layers = append(layers, valid)
	}
	themeSetting := ""
	for _, candidate := range []any{sources.Theme, prefsFile["theme"], piSettings["theme"]} {
		if name, ok := candidate.(string); ok && prefs.IsThemeSetting(name) {
			themeSetting = name
			break
		}
	}
	themes := map[string]theme.ThemeJSON{}
	if !sources.NoThemes {
		themes = customThemes(filepath.Join(sources.AgentDir, "themes"), &problems)
	}
	if len(problems) > 50 {
		problems = problems[:50]
	}
	return prefs.TuiLocalPreferences{Keybindings: keybindings, Theme: themeSetting, Settings: prefs.MergeSettings(layers...), Themes: themes, Problems: problems}
}

// WatchLocalPreferences watches the preference files and theme directory; onChange is called with changed, re-validated preferences. initial is the
// preferences already loaded. The returned function stops watching.
func WatchLocalPreferences(sources LocalPreferenceSources, onChange func(prefs.TuiLocalPreferences), initial prefs.TuiLocalPreferences) (stop func()) {
	encode := func(p prefs.TuiLocalPreferences) string {
		encoded, _ := json.Marshal(p)
		return string(encoded)
	}
	var mu sync.Mutex
	current := encode(initial)
	var watcher *fsnotify.Watcher
	var timer *time.Timer
	closed := false
	var schedule func()
	arm := func() {
		if watcher != nil {
			_ = watcher.Close()
		}
		next, err := fsnotify.NewWatcher()
		if err != nil {
			watcher = nil
			return
		}
		watcher = next
		directories := map[string]bool{}
		// A missing directory (a first prefs.json save creates ~/.pi/p2-tui) is noticed through its parent.
		for _, directory := range []string{sources.AgentDir, filepath.Join(sources.AgentDir, "themes"), sources.ConfigDir} {
			if _, statErr := os.Stat(directory); statErr == nil {
				directories[directory] = true
			} else {
				directories[filepath.Dir(directory)] = true
			}
		}
		for directory := range directories {
			// Unwatchable (gone, permissions): re-armed on the next change.
			_ = next.Add(directory)
		}
		go func() {
			for {
				select {
				case _, ok := <-next.Events:
					if !ok {
						return
					}
					schedule()
				case _, ok := <-next.Errors:
					if !ok {
						return
					}
				}
			}
		}()
	}
	schedule = func() {
		mu.Lock()
		defer mu.Unlock()
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(100*time.Millisecond, func() {
			mu.Lock()
			if closed {
				mu.Unlock()
				return
			}
			arm()
			loaded := LoadLocalPreferences(sources)
			text := encode(loaded)
			if text == current {
				mu.Unlock()
				return
			}
			current = text
			mu.Unlock()
			onChange(loaded)
		})
	}
	mu.Lock()
	arm()
	mu.Unlock()
	return func() {
		mu.Lock()
		defer mu.Unlock()
		closed = true
		if timer != nil {
			timer.Stop()
		}
		if watcher != nil {
			_ = watcher.Close()
		}
	}
}

// Capabilities are the launcher-known terminal facts for the TUI (section 3.2 `capabilities`).
type Capabilities struct {
	TrueColor   bool
	Platform    string
	WindowsKeys bool
}

// LauncherCapabilities is launcherCapabilities. env reads an environment variable; platform is Node's process.platform ("darwin", "linux", "win32").
func LauncherCapabilities(getenv func(string) string, platform string) Capabilities {
	return Capabilities{
		TrueColor: pitui.GetCapabilities().TrueColor,
		Platform:  platform,
		// coding-agent useWindowsKeybindings: Windows and WSL terminals.
		WindowsKeys: platform == "win32" || (platform == "linux" && (getenv("WSL_DISTRO_NAME") != "" || getenv("WSL_INTEROP") != "")),
	}
}

// DefaultMouse is defaultMouse. pi's fullscreen captures the mouse by default: all-motion reporting, except button-motion inside multiplexers, where
// every pointer movement would lag (pi-tui tui-alt-screen.ts makes the same choice from the same variables). lookup reports whether a variable is set.
func DefaultMouse(lookup func(string) (string, bool)) MousePolicy {
	term, _ := lookup("TERM")
	term = strings.ToLower(term)
	_, tmux := lookup("TMUX")
	_, zellij := lookup("ZELLIJ")
	_, sty := lookup("STY")
	if tmux || zellij || sty || strings.HasPrefix(term, "tmux") || strings.HasPrefix(term, "screen") {
		return MouseOn
	}
	return MouseAll
}

// LauncherPrefs are the launcher-only preferences, never posted to the TUI's plugins.
type LauncherPrefs struct {
	Mouse     MousePolicy
	Downloads string
	Editor    string
}

// LauncherPreferences is launcherPreferences: the mouse policy, the download folder and the external editor command (p2 prefs, then pi's
// `externalEditor`, then $VISUAL/$EDITOR, then pi's nano/notepad default).
func LauncherPreferences(sources LocalPreferenceSources, lookup func(string) (string, bool), platform string) LauncherPrefs {
	var ignored []string
	prefsDoc, _, _ := readJSON(filepath.Join(sources.ConfigDir, "prefs.json"), "prefs.json", &ignored)
	piDoc, _, _ := readJSON(filepath.Join(sources.AgentDir, "settings.json"), "settings.json", &ignored)
	prefsFile, pi := record(prefsDoc), record(piDoc)
	text := func(value any) string {
		s, _ := value.(string)
		return strings.TrimSpace(s)
	}
	mouse := MousePolicy("")
	if raw, _ := prefsFile["mouse"].(string); raw == string(MouseOn) || raw == string(MouseAll) || raw == string(MouseOff) {
		mouse = MousePolicy(raw)
	} else {
		mouse = DefaultMouse(lookup)
	}
	downloads := filepath.Join(homeDir(), "Downloads", "p2")
	if configured := text(prefsFile["downloads"]); configured != "" {
		downloads = expandHome(configured)
	}
	visual, _ := lookup("VISUAL")
	editorVar, _ := lookup("EDITOR")
	editor := text(prefsFile["externalEditor"])
	for _, candidate := range []string{text(pi["externalEditor"]), strings.TrimSpace(visual), strings.TrimSpace(editorVar)} {
		if editor == "" {
			editor = candidate
		}
	}
	if editor == "" {
		editor = "nano"
		if platform == "win32" {
			editor = "notepad"
		}
	}
	return LauncherPrefs{Mouse: mouse, Downloads: downloads, Editor: editor}
}
