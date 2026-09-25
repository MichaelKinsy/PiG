package tui

// Mirrors upstream packages/tui/src/keybindings.ts.
//
// This is the component-level keybinding system used by Editor, SelectList,
// and TreeSelector. It is separate from the app-level KeybindingsManager in
// internal/codingagent/keybindings.go which handles actions like
// app.model.cycle, app.tree.toggle, etc.

import (
	"os"
	"runtime"
	"slices"
	"strings"
)

// TUIKeybinding is a named action for TUI components.
type TUIKeybinding = string

// TUI keybinding action constants: mirrors upstream Keybindings interface.
const (
	// Editor navigation and editing
	KBEditorCursorUp          TUIKeybinding = "tui.editor.cursorUp"
	KBEditorCursorDown        TUIKeybinding = "tui.editor.cursorDown"
	KBEditorHistoryPrevious   TUIKeybinding = "tui.editor.historyPrevious"
	KBEditorHistoryNext       TUIKeybinding = "tui.editor.historyNext"
	KBEditorCursorLeft        TUIKeybinding = "tui.editor.cursorLeft"
	KBEditorCursorRight       TUIKeybinding = "tui.editor.cursorRight"
	KBEditorCursorWordLeft    TUIKeybinding = "tui.editor.cursorWordLeft"
	KBEditorCursorWordRight   TUIKeybinding = "tui.editor.cursorWordRight"
	KBEditorCursorLineStart   TUIKeybinding = "tui.editor.cursorLineStart"
	KBEditorCursorLineEnd     TUIKeybinding = "tui.editor.cursorLineEnd"
	KBEditorJumpForward       TUIKeybinding = "tui.editor.jumpForward"
	KBEditorJumpBackward      TUIKeybinding = "tui.editor.jumpBackward"
	KBEditorPageUp            TUIKeybinding = "tui.editor.pageUp"
	KBEditorPageDown          TUIKeybinding = "tui.editor.pageDown"
	KBEditorDeleteCharBack    TUIKeybinding = "tui.editor.deleteCharBackward"
	KBEditorDeleteCharForward TUIKeybinding = "tui.editor.deleteCharForward"
	KBEditorDeleteWordBack    TUIKeybinding = "tui.editor.deleteWordBackward"
	KBEditorDeleteWordForward TUIKeybinding = "tui.editor.deleteWordForward"
	KBEditorDeleteToLineStart TUIKeybinding = "tui.editor.deleteToLineStart"
	KBEditorDeleteToLineEnd   TUIKeybinding = "tui.editor.deleteToLineEnd"
	KBEditorYank              TUIKeybinding = "tui.editor.yank"
	KBEditorYankPop           TUIKeybinding = "tui.editor.yankPop"
	KBEditorUndo              TUIKeybinding = "tui.editor.undo"

	// Generic input actions
	KBInputNewLine TUIKeybinding = "tui.input.newLine"
	KBInputSubmit  TUIKeybinding = "tui.input.submit"
	KBInputTab     TUIKeybinding = "tui.input.tab"
	KBInputCopy    TUIKeybinding = "tui.input.copy"

	// Generic selection actions
	KBSelectUp       TUIKeybinding = "tui.select.up"
	KBSelectDown     TUIKeybinding = "tui.select.down"
	KBSelectPageUp   TUIKeybinding = "tui.select.pageUp"
	KBSelectPageDown TUIKeybinding = "tui.select.pageDown"
	KBSelectConfirm  TUIKeybinding = "tui.select.confirm"
	KBSelectCancel   TUIKeybinding = "tui.select.cancel"

	// Alt-screen (fullscreen) viewport actions
	KBAltScreenPageUp         TUIKeybinding = "tui.altScreen.pageUp"
	KBAltScreenPageDown       TUIKeybinding = "tui.altScreen.pageDown"
	KBAltScreenPreviousPrompt TUIKeybinding = "tui.altScreen.previousPrompt"
	KBAltScreenNextPrompt     TUIKeybinding = "tui.altScreen.nextPrompt"
	KBAltScreenTop            TUIKeybinding = "tui.altScreen.top"
	KBAltScreenBottom         TUIKeybinding = "tui.altScreen.bottom"
)

// TUIKeybindingDef defines a keybinding's default keys and description.
type TUIKeybindingDef struct {
	DefaultKeys []string
	Description string
}

// KeybindingPlatform selects a column of per-platform default keys. Upstream
// branches its default table on process.platform and on
// useWindowsKeybindings() (coding-agent core/keybindings.ts); these four values
// name every combination the table distinguishes. The values match the
// columns of the behavior-input inventory.
type KeybindingPlatform string

const (
	KeybindingPlatformDarwin   KeybindingPlatform = "darwin"
	KeybindingPlatformLinux    KeybindingPlatform = "linux"
	KeybindingPlatformLinuxWSL KeybindingPlatform = "linuxWsl"
	KeybindingPlatformWin32    KeybindingPlatform = "win32"
)

// KeybindingPlatforms lists every platform column in inventory order.
var KeybindingPlatforms = []KeybindingPlatform{
	KeybindingPlatformDarwin, KeybindingPlatformLinux, KeybindingPlatformLinuxWSL, KeybindingPlatformWin32,
}

// UseWindowsKeybindings reports whether Windows default keys apply: native
// Windows, or Linux under WSL. WT_SESSION alone does not count, because
// Windows Terminal also hosts SSH sessions to other machines. Mirrors
// upstream useWindowsKeybindings(platform, env) in core/keybindings.ts; goos
// uses Go's runtime.GOOS names.
func UseWindowsKeybindings(goos string, getenv func(string) string) bool {
	return goos == "windows" || (goos == "linux" && (getenv("WSL_DISTRO_NAME") != "" || getenv("WSL_INTEROP") != ""))
}

// KeybindingPlatformFor maps a runtime.GOOS value and an environment to the
// default-key column. Platforms upstream does not name (freebsd, etc.) take
// the Linux column, as process.platform comparisons fall through there.
func KeybindingPlatformFor(goos string, getenv func(string) string) KeybindingPlatform {
	switch {
	case goos == "darwin":
		return KeybindingPlatformDarwin
	case goos == "windows":
		return KeybindingPlatformWin32
	case UseWindowsKeybindings(goos, getenv):
		return KeybindingPlatformLinuxWSL
	default:
		return KeybindingPlatformLinux
	}
}

// UsesWindowsKeybindings reports whether the platform takes Windows defaults.
func (p KeybindingPlatform) UsesWindowsKeybindings() bool {
	return p == KeybindingPlatformWin32 || p == KeybindingPlatformLinuxWSL
}

// hostKeybindingPlatform is detected once, as upstream evaluates
// useWindowsKeybindings() once when the keybindings module loads.
var hostKeybindingPlatform = KeybindingPlatformFor(runtime.GOOS, os.Getenv)

// HostKeybindingPlatform returns the platform column for this process.
func HostKeybindingPlatform() KeybindingPlatform {
	return hostKeybindingPlatform
}

// PlatformKeys holds default keys that differ by platform. Other applies
// unless a narrower field is non-nil. Windows covers every platform that uses
// Windows keybindings (win32 and WSL); Win32, LinuxWSL, and Darwin override it
// for one platform. A non-nil empty slice means "no default key" on that
// platform, as upstream's `[]`.
type PlatformKeys struct {
	Other    []string
	Darwin   []string
	Windows  []string
	Win32    []string
	LinuxWSL []string
}

// For returns the default keys for platform.
func (k PlatformKeys) For(platform KeybindingPlatform) []string {
	switch {
	case platform == KeybindingPlatformDarwin && k.Darwin != nil:
		return k.Darwin
	case platform == KeybindingPlatformWin32 && k.Win32 != nil:
		return k.Win32
	case platform == KeybindingPlatformLinuxWSL && k.LinuxWSL != nil:
		return k.LinuxWSL
	case platform.UsesWindowsKeybindings() && k.Windows != nil:
		return k.Windows
	default:
		return k.Other
	}
}

// TUIKeybindingDefinitionsFor returns the tui.* definition table for a
// platform: upstream TUI_KEYBINDINGS (packages/tui/src/keybindings.ts) with
// the per-platform tui.* defaults that coding-agent's KEYBINDINGS applies on
// top (core/keybindings.ts). Every call returns a fresh map.
func TUIKeybindingDefinitionsFor(platform KeybindingPlatform) map[string]TUIKeybindingDef {
	return map[string]TUIKeybindingDef{
		KBEditorCursorUp:          {DefaultKeys: []string{"up"}, Description: "Move cursor up"},
		KBEditorCursorDown:        {DefaultKeys: []string{"down"}, Description: "Move cursor down"},
		KBEditorHistoryPrevious:   {DefaultKeys: []string{}, Description: "Select previous prompt history entry"},
		KBEditorHistoryNext:       {DefaultKeys: []string{}, Description: "Select next prompt history entry"},
		KBEditorCursorLeft:        {DefaultKeys: []string{"left", "ctrl+b"}, Description: "Move cursor left"},
		KBEditorCursorRight:       {DefaultKeys: []string{"right", "ctrl+f"}, Description: "Move cursor right"},
		KBEditorCursorWordLeft:    {DefaultKeys: []string{"alt+left", "ctrl+left", "alt+b"}, Description: "Move cursor word left"},
		KBEditorCursorWordRight:   {DefaultKeys: []string{"alt+right", "ctrl+right", "alt+f"}, Description: "Move cursor word right"},
		KBEditorCursorLineStart:   {DefaultKeys: []string{"home", "ctrl+home", "ctrl+a"}, Description: "Move to line start"},
		KBEditorCursorLineEnd:     {DefaultKeys: []string{"end", "ctrl+end", "ctrl+e"}, Description: "Move to line end"},
		KBEditorJumpForward:       {DefaultKeys: []string{"ctrl+]"}, Description: "Jump forward to character"},
		KBEditorJumpBackward:      {DefaultKeys: []string{"ctrl+alt+]"}, Description: "Jump backward to character"},
		KBEditorPageUp:            {DefaultKeys: []string{"pageUp", "ctrl+pageUp"}, Description: "Page up"},
		KBEditorPageDown:          {DefaultKeys: []string{"pageDown", "ctrl+pageDown"}, Description: "Page down"},
		KBEditorDeleteCharBack:    {DefaultKeys: []string{"backspace"}, Description: "Delete character backward"},
		KBEditorDeleteCharForward: {DefaultKeys: []string{"delete", "ctrl+d"}, Description: "Delete character forward"},
		KBEditorDeleteWordBack:    {DefaultKeys: []string{"ctrl+w", "alt+backspace"}, Description: "Delete word backward"},
		KBEditorDeleteWordForward: {DefaultKeys: []string{"alt+d", "alt+delete"}, Description: "Delete word forward"},
		KBEditorDeleteToLineStart: {DefaultKeys: []string{"ctrl+u"}, Description: "Delete to line start"},
		KBEditorDeleteToLineEnd:   {DefaultKeys: []string{"ctrl+k"}, Description: "Delete to line end"},
		KBEditorYank:              {DefaultKeys: []string{"ctrl+y"}, Description: "Yank"},
		KBEditorYankPop:           {DefaultKeys: []string{"alt+y"}, Description: "Yank pop"},
		// Native Windows undoes on ctrl+z; WSL leaves ctrl+z to job control
		// and takes alt+z. Mirrors coding-agent KEYBINDINGS["tui.editor.undo"].
		KBEditorUndo: {DefaultKeys: PlatformKeys{
			Other:    []string{"ctrl+-"},
			Win32:    []string{"ctrl+z"},
			LinuxWSL: []string{"alt+z"},
		}.For(platform), Description: "Undo"},
		KBInputNewLine:            {DefaultKeys: []string{"shift+enter", "ctrl+j"}, Description: "Insert newline"},
		KBInputSubmit:             {DefaultKeys: []string{"enter"}, Description: "Submit input"},
		KBInputTab:                {DefaultKeys: []string{"tab"}, Description: "Tab / autocomplete"},
		KBInputCopy:               {DefaultKeys: []string{"ctrl+c"}, Description: "Copy selection"},
		KBSelectUp:                {DefaultKeys: []string{"up"}, Description: "Move selection up"},
		KBSelectDown:              {DefaultKeys: []string{"down"}, Description: "Move selection down"},
		KBSelectPageUp:            {DefaultKeys: []string{"pageUp"}, Description: "Selection page up"},
		KBSelectPageDown:          {DefaultKeys: []string{"pageDown"}, Description: "Selection page down"},
		KBSelectConfirm:           {DefaultKeys: []string{"enter"}, Description: "Confirm selection"},
		KBSelectCancel:            {DefaultKeys: []string{"escape", "ctrl+c"}, Description: "Cancel selection"},
		KBAltScreenPageUp:         {DefaultKeys: []string{"pageUp"}, Description: "Scroll viewport up one page"},
		KBAltScreenPageDown:       {DefaultKeys: []string{"pageDown"}, Description: "Scroll viewport down one page"},
		KBAltScreenHalfPageUp:     {DefaultKeys: []string{}, Description: "Scroll viewport up half a page"},
		KBAltScreenHalfPageDown:   {DefaultKeys: []string{}, Description: "Scroll viewport down half a page"},
		KBAltScreenLineUp:         {DefaultKeys: []string{}, Description: "Scroll viewport up one line"},
		KBAltScreenLineDown:       {DefaultKeys: []string{}, Description: "Scroll viewport down one line"},
		KBAltScreenPreviousPrompt: {DefaultKeys: PlatformKeys{Other: []string{"ctrl+shift+up", "ctrl+up"}, Windows: []string{"ctrl+up"}}.For(platform), Description: "Jump to previous semantic prompt"},
		KBAltScreenNextPrompt:     {DefaultKeys: PlatformKeys{Other: []string{"ctrl+shift+down", "ctrl+down"}, Windows: []string{"ctrl+down"}}.For(platform), Description: "Jump to next semantic prompt"},
		KBAltScreenSearch:         {DefaultKeys: PlatformKeys{Other: []string{"ctrl+shift+f"}, Windows: []string{"ctrl+f"}}.For(platform), Description: "Search the primary scroll view"},
		KBAltScreenSearchNext:     {DefaultKeys: []string{"enter", "ctrl+g"}, Description: "Select the next search match"},
		KBAltScreenSearchPrevious: {DefaultKeys: []string{"shift+enter", "ctrl+shift+g"}, Description: "Select the previous search match"},
		KBAltScreenSearchClose:    {DefaultKeys: []string{"escape"}, Description: "Close transcript search"},
		KBAltScreenTop:            {DefaultKeys: []string{"home"}, Description: "Scroll viewport to top"},
		KBAltScreenBottom:         {DefaultKeys: []string{"end"}, Description: "Scroll viewport to bottom"},
	}
}

// tuiKeybindingDefs is the built-in definition table for this host.
var tuiKeybindingDefs = TUIKeybindingDefinitionsFor(hostKeybindingPlatform)

// TUIKeybindingConflict records a key bound to multiple actions.
type TUIKeybindingConflict struct {
	Key     string
	Actions []string
}

// TUIKeybindingsManager resolves keybindings for TUI components.
// Mirrors upstream KeybindingsManager in packages/tui/src/keybindings.ts.
type TUIKeybindingsManager struct {
	definitions  map[string]TUIKeybindingDef
	userBindings map[string][]string // nil = not set (use default)
	keysById     map[string][]string
	conflicts    []TUIKeybindingConflict
}

// NewTUIKeybindingsManager creates a manager with the built-in TUI definitions
// for this host and optional user overrides.
func NewTUIKeybindingsManager(userBindings map[string][]string) *TUIKeybindingsManager {
	return NewKeybindingsManager(tuiKeybindingDefs, userBindings)
}

// NewKeybindingsManager creates a manager over an explicit definition table.
// Mirrors upstream `new KeybindingsManager(definitions, userBindings)`; the
// coding agent passes its merged tui.* and app.* table here so components in
// this package resolve app actions (tree labels, thinking save) through the
// same manager, as upstream's single KEYBINDINGS table does.
func NewKeybindingsManager(definitions map[string]TUIKeybindingDef, userBindings map[string][]string) *TUIKeybindingsManager {
	m := &TUIKeybindingsManager{
		definitions:  definitions,
		userBindings: userBindings,
		keysById:     make(map[string][]string),
	}
	if m.userBindings == nil {
		m.userBindings = make(map[string][]string)
	}
	m.rebuild()
	return m
}

func dedupeKeys(keys []string) []string {
	seen := make(map[string]bool, len(keys))
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

func (m *TUIKeybindingsManager) rebuild() {
	clear(m.keysById)
	m.conflicts = nil

	// Detect user-binding conflicts.
	userClaims := make(map[string]map[string]bool)
	for action, keys := range m.userBindings {
		for _, key := range dedupeKeys(keys) {
			if userClaims[key] == nil {
				userClaims[key] = make(map[string]bool)
			}
			userClaims[key][action] = true
		}
	}
	for key, actions := range userClaims {
		if len(actions) > 1 {
			var list []string
			for a := range actions {
				list = append(list, a)
			}
			slices.Sort(list)
			m.conflicts = append(m.conflicts, TUIKeybindingConflict{Key: key, Actions: list})
		}
	}

	// Resolve: user overrides if present, otherwise defaults.
	for id, def := range m.definitions {
		if userKeys, ok := m.userBindings[id]; ok {
			m.keysById[id] = dedupeKeys(userKeys)
		} else {
			m.keysById[id] = dedupeKeys(def.DefaultKeys)
		}
	}
	// The coding-agent manager shares app.* actions with TUI components in
	// upstream. Pig's packages are split, so retain unknown user-defined action
	// IDs here as an explicit bridge for components that consume app.* actions.
	for id, keys := range m.userBindings {
		if _, exists := m.keysById[id]; !exists {
			m.keysById[id] = dedupeKeys(keys)
		}
	}
}

// Matches checks whether raw terminal input data matches a keybinding action.
// Uses the same keyIDInputs lookup table as the app-level keybindings for
// legacy terminal sequence matching.
func (m *TUIKeybindingsManager) Matches(data string, action TUIKeybinding) bool {
	keys := m.keysById[action]
	for _, key := range keys {
		if matchesKeyID(data, key) {
			return true
		}
	}
	return false
}

// GetKeys returns the resolved keys for a keybinding action.
func (m *TUIKeybindingsManager) GetKeys(action TUIKeybinding) []string {
	keys := m.keysById[action]
	return slices.Clone(keys)
}

// HasBinding reports whether the manager owns an action, including an explicit
// empty user override for an app.* action bridged from coding-agent.
func (m *TUIKeybindingsManager) HasBinding(action TUIKeybinding) bool {
	_, ok := m.keysById[action]
	return ok
}

// GetDefinition returns the definition for a keybinding action.
func (m *TUIKeybindingsManager) GetDefinition(action TUIKeybinding) (TUIKeybindingDef, bool) {
	def, ok := m.definitions[action]
	return def, ok
}

// GetConflicts returns any user-binding conflicts detected.
func (m *TUIKeybindingsManager) GetConflicts() []TUIKeybindingConflict {
	out := make([]TUIKeybindingConflict, len(m.conflicts))
	for i, c := range m.conflicts {
		out[i] = TUIKeybindingConflict{Key: c.Key, Actions: slices.Clone(c.Actions)}
	}
	return out
}

// SetUserBindings replaces user overrides and rebuilds.
func (m *TUIKeybindingsManager) SetUserBindings(bindings map[string][]string) {
	m.userBindings = bindings
	if m.userBindings == nil {
		m.userBindings = make(map[string][]string)
	}
	m.rebuild()
}

// GetUserBindings returns a defensive copy of the raw user overrides.
// Mirrors upstream KeybindingsManager.getUserBindings().
func (m *TUIKeybindingsManager) GetUserBindings() map[string][]string {
	out := make(map[string][]string, len(m.userBindings))
	for k, v := range m.userBindings {
		out[k] = slices.Clone(v)
	}
	return out
}

// GetResolvedBindings returns the fully resolved binding table after applying
// defaults and user overrides. Mirrors upstream getResolvedBindings().
func (m *TUIKeybindingsManager) GetResolvedBindings() map[string]any {
	resolved := make(map[string]any, len(m.definitions))
	for id := range m.definitions {
		keys := m.keysById[id]
		switch len(keys) {
		case 0:
			resolved[id] = []string{}
		case 1:
			resolved[id] = keys[0]
		default:
			resolved[id] = slices.Clone(keys)
		}
	}
	return resolved
}

// ──────────────────────────────────────────────────────────────────────────────
// Key matching: maps key IDs (e.g. "ctrl+c", "escape", "alt+backspace") to
// the raw terminal byte sequences they produce.
//
// This is the TUI-level equivalent of the app-level keyIDInputs map in
// internal/codingagent/keybindings.go. It covers the full set of keys needed
// by TUI components (editor, select list, tree selector).
// ──────────────────────────────────────────────────────────────────────────────

// tuiKeyIDInputs maps key IDs to the raw terminal sequences they produce.
// Covers legacy terminal escapes; Kitty protocol sequences are not yet
// supported (matching upstream's legacy-first approach for component input).
var tuiKeyIDInputs = map[string][]string{
	// Special keys
	"esc":       {"\x1b"},
	"escape":    {"\x1b"},
	"enter":     {"\r"},
	"return":    {"\r"},
	"tab":       {"\t"},
	"space":     {" "},
	"backspace": {"\x7f", "\b"},
	"clear":     {"\x1b[E", "\x1bOE"},
	"delete":    {"\x1b[3~"},
	"insert":    {"\x1b[2~"},
	"home":      {"\x1b[H", "\x1bOH", "\x1b[1~", "\x1b[7~"},
	"end":       {"\x1b[F", "\x1bOF", "\x1b[4~", "\x1b[8~"},
	"pageUp":    {"\x1b[5~"},
	"pageDown":  {"\x1b[6~"},

	// Arrow keys
	"up":    {"\x1b[A", "\x1bOA"},
	"down":  {"\x1b[B", "\x1bOB"},
	"left":  {"\x1b[D", "\x1bOD"},
	"right": {"\x1b[C", "\x1bOC"},

	// Shift
	"shift+enter": {"\n", "\x1b\r", "\x1b\n", "\x1b[13;2u", "\x1b[13;2~", "\x1b[27;2;13~"},
	"shift+tab":   {"\x1b[Z"},
	"shift+clear": {"\x1b[e"},

	// Ctrl+letter: legacy byte + Kitty CSI-u + xterm modifyOtherKeys mode 2
	"ctrl+a": {"\x01", "\x1b[97;5u", "\x1b[27;5;97~"},
	"ctrl+b": {"\x02", "\x1b[98;5u", "\x1b[27;5;98~"},
	"ctrl+c": {"\x03", "\x1b[99;5u", "\x1b[27;5;99~"},
	"ctrl+d": {"\x04", "\x1b[100;5u", "\x1b[27;5;100~"},
	"ctrl+e": {"\x05", "\x1b[101;5u", "\x1b[27;5;101~"},
	"ctrl+f": {"\x06", "\x1b[102;5u", "\x1b[27;5;102~"},
	"ctrl+g": {"\x07", "\x1b[103;5u", "\x1b[27;5;103~"},
	// ctrl+j: CSI-u form only. The legacy byte \x0a is already claimed by
	// shift+enter (both insert a newline). Upstream 0.80.0 added ctrl+j as a
	// newline binding; this covers the kitty / modifyOtherKeys sequences.
	"ctrl+j": {"\x1b[106;5u", "\x1b[27;5;106~"},
	"ctrl+k": {"\x0b", "\x1b[107;5u", "\x1b[27;5;107~"},
	"ctrl+l": {"\x0c", "\x1b[108;5u", "\x1b[27;5;108~"},
	"ctrl+n": {"\x0e", "\x1b[110;5u", "\x1b[27;5;110~"},
	"ctrl+o": {"\x0f", "\x1b[111;5u", "\x1b[27;5;111~"},
	"ctrl+p": {"\x10", "\x1b[112;5u", "\x1b[27;5;112~"},
	"ctrl+r": {"\x12", "\x1b[114;5u", "\x1b[27;5;114~"},
	"ctrl+s": {"\x13", "\x1b[115;5u", "\x1b[27;5;115~"},
	"ctrl+t": {"\x14", "\x1b[116;5u", "\x1b[27;5;116~"},
	"ctrl+u": {"\x15", "\x1b[117;5u", "\x1b[27;5;117~"},
	"ctrl+v": {"\x16", "\x1b[118;5u", "\x1b[27;5;118~"},
	"ctrl+w": {"\x17", "\x1b[119;5u", "\x1b[27;5;119~"},
	"ctrl+x": {"\x18", "\x1b[120;5u", "\x1b[27;5;120~"},
	"ctrl+y": {"\x19", "\x1b[121;5u", "\x1b[27;5;121~"},
	"ctrl+z": {"\x1a", "\x1b[122;5u", "\x1b[27;5;122~"},

	// Ctrl+Shift+letter: Kitty CSI-u + xterm modifyOtherKeys mode 2
	// modifier 6 = Shift(1) + Ctrl(4) + 1 = 6
	"ctrl+shift+a": {"\x1b[97;6u", "\x1b[27;6;97~"},
	"ctrl+shift+b": {"\x1b[98;6u", "\x1b[27;6;98~"},
	"ctrl+shift+c": {"\x1b[99;6u", "\x1b[27;6;99~"},
	"ctrl+shift+d": {"\x1b[100;6u", "\x1b[27;6;100~"},
	"ctrl+shift+e": {"\x1b[101;6u", "\x1b[27;6;101~"},
	"ctrl+shift+f": {"\x1b[102;6u", "\x1b[27;6;102~"},
	"ctrl+shift+g": {"\x1b[103;6u", "\x1b[27;6;103~"},
	"ctrl+shift+h": {"\x1b[104;6u", "\x1b[27;6;104~"},
	"ctrl+shift+i": {"\x1b[105;6u", "\x1b[27;6;105~"},
	"ctrl+shift+j": {"\x1b[106;6u", "\x1b[27;6;106~"},
	"ctrl+shift+k": {"\x1b[107;6u", "\x1b[27;6;107~"},
	"ctrl+shift+l": {"\x1b[108;6u", "\x1b[27;6;108~"},
	"ctrl+shift+m": {"\x1b[109;6u", "\x1b[27;6;109~"},
	"ctrl+shift+n": {"\x1b[110;6u", "\x1b[27;6;110~"},
	"ctrl+shift+o": {"\x1b[111;6u", "\x1b[27;6;111~"},
	"ctrl+shift+p": {"\x1b[112;6u", "\x1b[27;6;112~"},
	"ctrl+shift+q": {"\x1b[113;6u", "\x1b[27;6;113~"},
	"ctrl+shift+r": {"\x1b[114;6u", "\x1b[27;6;114~"},
	"ctrl+shift+s": {"\x1b[115;6u", "\x1b[27;6;115~"},
	"ctrl+shift+t": {"\x1b[116;6u", "\x1b[27;6;116~"},
	"ctrl+shift+u": {"\x1b[117;6u", "\x1b[27;6;117~"},
	"ctrl+shift+v": {"\x1b[118;6u", "\x1b[27;6;118~"},
	"ctrl+shift+w": {"\x1b[119;6u", "\x1b[27;6;119~"},
	"ctrl+shift+x": {"\x1b[120;6u", "\x1b[27;6;120~"},
	"ctrl+shift+y": {"\x1b[121;6u", "\x1b[27;6;121~"},
	"ctrl+shift+z": {"\x1b[122;6u", "\x1b[27;6;122~"},

	// Ctrl+symbol
	"ctrl+]": {"\x1d"},
	"ctrl+-": {"\x1f"},

	// Alt+letter
	"alt+b": {"\x1bb"},
	"alt+d": {"\x1bd"},
	"alt+f": {"\x1bf"},
	"alt+y": {"\x1by"},

	// Alt+key
	"alt+backspace": {"\x1b\x7f", "\x1b\b"},
	"alt+delete":    {"\x1b[3;3~"},
	"alt+left":      {"\x1b[1;3D", "\x1bb"},
	"alt+right":     {"\x1b[1;3C", "\x1bf"},
	"alt+up":        {"\x1b[1;3A", "\x1bp"},
	"alt+down":      {"\x1b[1;3B", "\x1bn"},
	"alt+enter":     {"\x1b[13;3u", "\x1b[27;3;13~"},

	// Ctrl+arrow
	"ctrl+left":  {"\x1b[1;5D", "\x1bOd"},
	"ctrl+right": {"\x1b[1;5C", "\x1bOc"},
	"ctrl+up":    {"\x1b[1;5A", "\x1bOa"},
	"ctrl+down":  {"\x1b[1;5B", "\x1bOb"},
	"ctrl+clear": {"\x1bOe"},

	// Ctrl+Alt
	"ctrl+alt+]": {"\x1b\x1d"},

	// Function keys
	"f1":  {"\x1bOP", "\x1b[11~", "\x1b[[A"},
	"f2":  {"\x1bOQ", "\x1b[12~", "\x1b[[B"},
	"f3":  {"\x1bOR", "\x1b[13~", "\x1b[[C"},
	"f4":  {"\x1bOS", "\x1b[14~", "\x1b[[D"},
	"f5":  {"\x1b[15~", "\x1b[[E"},
	"f6":  {"\x1b[17~"},
	"f7":  {"\x1b[18~"},
	"f8":  {"\x1b[19~"},
	"f9":  {"\x1b[20~"},
	"f10": {"\x1b[21~"},
	"f11": {"\x1b[23~"},
	"f12": {"\x1b[24~"},
}

// MatchesKeyID checks if raw terminal data matches a key identifier string
// like "ctrl+c", "ctrl+shift+z", "enter", etc. Exported for use by extension
// shortcut dispatch in interactive mode.
func MatchesKeyID(data, keyID string) bool {
	return matchesKeyID(data, keyID)
}

// matchesKeyID checks if raw terminal data matches a key identifier string
// like "ctrl+c", "alt+backspace", "ctrl+shift+left", etc.
//
// Dynamically representable keys use the protocol- and mode-aware matcher.
// Function keys and clear fall back to their legacy sequence tables.
func matchesKeyID(data, keyID string) bool {
	baseKey, _, parsed := parseKeyID(keyID)
	if parsed {
		if _, dynamic := resolveKeyCodepoint(baseKey); dynamic {
			return matchesKeyDynamic(data, keyID)
		}
	}

	// Function keys and clear have no CSI-u codepoint in Pi's key vocabulary,
	// so they retain their legacy lookup. Every dynamically representable key
	// goes through the mode-aware matcher above; consulting this table first
	// would make ambiguous legacy bytes match the wrong key while Kitty mode is
	// active.
	normalized := strings.ToLower(keyID)
	if seqs, ok := tuiKeyIDInputs[keyID]; ok && slices.Contains(seqs, data) {
		return true
	}
	if normalized != keyID {
		if seqs, ok := tuiKeyIDInputs[normalized]; ok && slices.Contains(seqs, data) {
			return true
		}
	}
	return false
}

// ──────────────────────────────────────────────────────────────────────────────
// Global TUI keybindings: mirrors upstream setKeybindings / getKeybindings.
// ──────────────────────────────────────────────────────────────────────────────

var globalTUIKeybindings *TUIKeybindingsManager

// SetKeybindings mirrors upstream setKeybindings().
func SetKeybindings(kb *TUIKeybindingsManager) {
	globalTUIKeybindings = kb

}

// GetKeybindings mirrors upstream getKeybindings().
func GetKeybindings() *TUIKeybindingsManager {
	if globalTUIKeybindings == nil {
		globalTUIKeybindings = NewTUIKeybindingsManager(nil)
	}
	return globalTUIKeybindings
}

// SetTUIKeybindings sets the global TUI keybinding manager.
func SetTUIKeybindings(kb *TUIKeybindingsManager) {
	SetKeybindings(kb)
}

// GetTUIKeybindings returns the global TUI keybinding manager,
// creating a default one if none has been set.
func GetTUIKeybindings() *TUIKeybindingsManager {
	return GetKeybindings()
}
