package tui

// P10: `/settings` (after coding-agent's settings-selector.ts, the guest-local G settings of section 1.14), its theme picker (theme-selector.ts, with an
// isolated preview pane) and `/keybindings` (rebinding). Nothing is written here: changes are collected and proposed to the launcher as one `savePrefs`
// patch, which shows the diff and writes the visitor's own prefs.json after a local confirmation; the launcher's file watcher then applies them.
//
// Forced Go difference: `ctx.prefs.save` blocks until the launcher answers, so a save runs on its own goroutine and reports through Notify, which the runtime
// posts to the owner loop.
//
// pi: apps/p2/src/tui/settings.ts

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/jsstring/localecompare"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

// field is a G setting as pi's selector shows it: its path in prefs.json, pi's default and the values it cycles.
type field struct {
	id, label, description string
	path                   []string
	fallback               any
	values                 []any
}

var builtinFields = []field{
	// p2 hides thinking until the setting says otherwise (pi shows it by default).
	{"hide-thinking", "Hide thinking", "Hide thinking blocks in assistant responses", []string{"hideThinkingBlock"}, true, []any{true, false}},
	{"mermaid-rendering", "Mermaid diagrams", "Render Mermaid code blocks as diagrams", []string{"markdown", "mermaid"}, "streaming", []any{"off", "final", "streaming"}},
	{"quiet-startup", "Quiet startup", "Hide the startup header (or keep only its first line)", []string{"quietStartup"}, false, []any{true, "header", false}},
	{"show-hardware-cursor", "Show hardware cursor", "Show the terminal cursor while still positioning it for IME support", []string{"showHardwareCursor"}, false, []any{true, false}},
	{"editor-padding", "Editor padding", "Horizontal padding for the input editor (0-3)", []string{"editorPaddingX"}, 0, []any{0, 1, 2, 3}},
	{"output-padding", "Output padding", "Horizontal padding for transcript output", []string{"outputPad"}, 1, []any{0, 1}},
	{"autocomplete-max-visible", "Autocomplete max items", "Maximum visible items in autocomplete", []string{"autocompleteMaxVisible"}, 5, []any{3, 5, 7, 10, 15, 20}},
	{"clear-on-shrink", "Clear on shrink", "Clear empty rows when content shrinks", []string{"terminal", "clearOnShrink"}, false, []any{true, false}},
	{"terminal-progress", "Terminal progress", "Show progress in the terminal tab while the agent works", []string{"terminal", "showTerminalProgress"}, false, []any{true, false}},
	{"fullscreen-exit-output", "Fullscreen exit output", "What to print when leaving fullscreen", []string{"fullscreenExitOutput"}, "transcript", []any{"transcript", "resume-hint"}},
	{"fullscreen-scrollbar", "Fullscreen scrollbar", "When to show the transcript scrollbar", []string{"fullscreenScrollbar"}, "auto", []any{"auto", "always", "hidden"}},
	{"fullscreen-copy-on-select", "Fullscreen copy on select", "Copy a mouse selection when the button is released", []string{"fullscreenCopyOnSelect"}, true, []any{true, false}},
	{"fullscreen-wheel-scroll-lines", "Fullscreen wheel scrolling", "Lines per mouse-wheel event; 'auto' speeds up fast spins", []string{"fullscreenWheelScrollLines"}, "auto", []any{"auto", 1, 2, 3, 5, 10}},
	{"show-images", "Show images", "Render images inline (when the launcher supports them)", []string{"terminal", "showImages"}, true, []any{true, false}},
	{"image-width-cells", "Image width", "Preferred inline image width in terminal cells", []string{"terminal", "imageWidthCells"}, 60, []any{60, 80, 120}},
}

// readSetting is the value at path of the settings as JSON; nil when unset.
func readSetting(settings prefs.TuiSettings, path []string) any {
	encoded, err := json.Marshal(settings)
	if err != nil {
		return nil
	}
	var value any
	if json.Unmarshal(encoded, &value) != nil {
		return nil
	}
	for _, key := range path {
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = object[key]
	}
	return value
}

func str(value any) string { return fmt.Sprint(value) }

type funcComponent struct {
	render func(width int) []string
}

func (c funcComponent) Render(width int) []string { return c.render(width) }
func (funcComponent) Invalidate()                 {}

// box is a bordered, titled body over a footer line.
func box(ctx runtime.TuiContext, title string, body pitui.Component, footer func() string) *pitui.Container {
	container := pitui.NewContainer()
	rule := &funcComponent{func(width int) []string { return []string{border(ctx, width)} }}
	container.Add(rule)
	container.Add(&funcComponent{func(width int) []string {
		current := ctx.Theme().Current()
		return []string{truncate(current.Fg("accent", current.Bold(title)), width)}
	}})
	container.Add(body)
	container.Add(&funcComponent{func(width int) []string {
		return []string{truncate(ctx.Theme().Current().Fg("dim", footer()), width)}
	}})
	container.Add(rule)
	return container
}

// sample is a few lines in every theme token family, rendered with the preview's own ThemeRef.
func sample(ref *theme.ThemeRef, width int) []string {
	current := ref.Current()
	markdown := pitui.NewMarkdownWithOptions("## Preview\nSome **bold** text, a [link](https://pi.dev) and `code`.\n```ts\nconst answer = 42; // comment\n```", 1, 0, &ref.Markdown, nil, nil)
	lines := markdown.Render(width)
	return append(lines,
		" "+current.Fg("toolDiffAdded", "+ added line")+"  "+current.Fg("toolDiffRemoved", "- removed line"),
		" "+current.Fg("success", "success")+" "+current.Fg("warning", "warning")+" "+current.Fg("error", "error")+" "+current.Fg("muted", "muted")+" "+current.Fg("accent", "accent"),
		current.Bg("userMessageBg", padEnd(" "+current.Fg("userMessageText", "A user message")+" ", max(1, width-1))))
}

// ThemePicker is the theme list on the left in pi's look, the preview below it with its own ThemeRef, so the live transcript keeps its theme until the patch
// is saved. "automatic" picks a light and a dark theme (pi's pair setting).
type ThemePicker struct {
	focused bool
	ctx     runtime.TuiContext
	list    *pitui.SelectList
	preview *theme.ThemeRef
	light   string
	step    string
	done    func(value string, ok bool)
}

// NewThemePicker starts on the theme list with current selected; done gets the chosen setting, or ok false when cancelled.
func NewThemePicker(ctx runtime.TuiContext, current string, done func(value string, ok bool)) *ThemePicker {
	p := &ThemePicker{ctx: ctx, done: done, step: "theme", preview: ctx.Prefs().PreviewTheme(current)}
	p.list = p.menu(current, "theme")
	return p
}

func (p *ThemePicker) menu(current, step string) *pitui.SelectList {
	var items []pitui.SelectItem
	for _, name := range p.ctx.Prefs().ThemeNames() {
		prefix := "  "
		if name == current {
			prefix = "✓ "
		}
		item := pitui.SelectItem{Value: name, Label: prefix + render.LineText(name)}
		if name == theme.SystemThemeName {
			item.Description = "Theme created from your terminal's colors"
		}
		items = append(items, item)
	}
	if step == "theme" {
		items = append(items, pitui.SelectItem{Value: "/", Label: "  automatic", Description: "Separate themes for light and dark terminals"})
	}
	list := pitui.NewSelectList(items, 8, p.ctx.Theme().SelectList)
	pair, isPair := theme.ParseAutoThemeSetting(current)
	want := current
	if isPair {
		want = "/"
	}
	if index := slices.IndexFunc(items, func(item pitui.SelectItem) bool { return item.Value == want }); index >= 0 {
		list.SetSelectedIndex(index)
	}
	list.OnSelectionChange = func(item pitui.SelectItem) {
		if item.Value != "/" {
			p.preview = p.ctx.Prefs().PreviewTheme(item.Value)
		}
		p.ctx.RequestRender()
	}
	list.OnCancel = func() { p.done("", false) }
	list.OnSelect = func(item pitui.SelectItem) {
		switch {
		case step == "theme" && item.Value == "/":
			light := pair.LightTheme
			if !isPair {
				light = theme.SystemThemeName
			}
			p.list = p.menu(light, "light")
		case step == "light":
			p.light = item.Value
			dark := pair.DarkTheme
			if !isPair {
				dark = item.Value
			}
			p.list = p.menu(dark, "dark")
		case step == "dark":
			p.done(p.light+"/"+item.Value, true)
		default:
			p.done(item.Value, true)
		}
		p.ctx.RequestRender()
	}
	p.step = step
	return list
}

// SetFocused records the focus.
func (p *ThemePicker) SetFocused(focused bool) { p.focused = focused }

// HandleInput passes keys to the list.
func (p *ThemePicker) HandleInput(data string) { p.list.HandleInput(data) }

// Render renders the list over the preview.
func (p *ThemePicker) Render(width int) []string {
	title := "Theme"
	switch p.step {
	case "light":
		title = "Light theme"
	case "dark":
		title = "Dark theme"
	}
	lines := []string{truncate(p.ctx.Theme().Current().Fg("accent", title), width)}
	lines = append(lines, p.list.Render(width)...)
	return append(lines, sample(p.preview, width)...)
}

// Invalidate drops cached renders.
func (p *ThemePicker) Invalidate() { p.list.Invalidate() }

func notifySave(ctx runtime.TuiContext, noun string, result runtime.GrantResult, applied string) {
	switch {
	case result.OK:
		ctx.Notify(applied, runtime.NoticeInfo)
	case result.Error == "denied":
		ctx.Notify(noun+" not saved", runtime.NoticeWarning)
	default:
		ctx.Notify(noun+" not saved ("+result.Error+")", runtime.NoticeWarning)
	}
}

// OpenSettings is `/settings`: pi's list; changes stay pending until the overlay closes, then go to the launcher as one patch. The built-in rows, then the
// plugins' `TuiSettingFields` rows in key order.
func OpenSettings(ctx runtime.TuiContext) error {
	source, err := ctx.Host().Registry(context.Background(), runtime.TuiSettingFields.Registry, host.GetOptions{})
	if err != nil {
		return err
	}
	table, err := runtime.TuiSettingFields.Table(source)
	if err != nil {
		return err
	}
	settings := ctx.Settings()
	fields := slices.Clone(builtinFields)
	keys := table.Keys()
	slices.SortStableFunc(keys, localecompare.Compare)
	for _, key := range keys {
		pluginField, _ := table.Get(key)
		values := make([]any, len(pluginField.Values))
		for i, value := range pluginField.Values {
			values[i] = value
		}
		fields = append(fields, field{
			id: "plugin:" + key, label: pluginField.Label, description: pluginField.Description,
			path: []string{"plugins", pluginField.Plugin, pluginField.Name}, fallback: pluginField.Fallback, values: values,
		})
	}
	patchSettings := map[string]any{}
	patchTheme := ""
	set := func(path []string, value any) {
		target := patchSettings
		for _, key := range path[:len(path)-1] {
			next, ok := target[key].(map[string]any)
			if !ok {
				next = map[string]any{}
				target[key] = next
			}
			target = next
		}
		target[path[len(path)-1]] = value
	}
	themeSetting := ctx.Prefs().ThemeSetting()
	if themeSetting == "" {
		themeSetting = theme.SystemThemeName
	}
	items := []pitui.SettingItem{{
		ID: "theme", Label: "Theme", Description: "Color theme for the interface", CurrentValue: themeSetting,
		Submenu: func(current string, done func(selected *string, options *pitui.SubmenuDoneOptions)) pitui.Component {
			return NewThemePicker(ctx, current, func(value string, ok bool) {
				if ok {
					done(&value, nil)
				} else {
					done(nil, nil)
				}
			})
		},
	}}
	for _, each := range fields {
		current := readSetting(settings, each.path)
		if current == nil {
			current = each.fallback
		}
		values := make([]string, len(each.values))
		for i, value := range each.values {
			values[i] = str(value)
		}
		items = append(items, pitui.SettingItem{ID: each.id, Label: each.label, Description: each.description, CurrentValue: str(current), Values: values})
	}
	changed := false
	var overlay runtime.TuiOverlayHandle
	finish := func() {
		overlay.Close()
		if !changed {
			return
		}
		body := map[string]any{}
		if len(patchSettings) > 0 {
			body["settings"] = patchSettings
		}
		if patchTheme != "" {
			body["theme"] = patchTheme
		}
		go func() {
			notifySave(ctx, "Preferences", ctx.Prefs().Save(body), "Preferences saved; they apply as the launcher reloads them")
		}()
	}
	list := pitui.NewSettingsList(items, min(len(items), 12), ctx.Theme().SettingsList(), func(id, value string) {
		changed = true
		if id == "theme" {
			patchTheme = value
			return
		}
		index := slices.IndexFunc(fields, func(each field) bool { return each.id == id })
		if index < 0 {
			return
		}
		if typed := slices.IndexFunc(fields[index].values, func(option any) bool { return str(option) == value }); typed >= 0 {
			set(fields[index].path, fields[index].values[typed])
		}
	}, finish)
	framed := box(ctx, "Settings", list, func() string {
		if changed {
			return "Esc saves the changes (the launcher shows them first)"
		}
		return "Your TUI preferences on this computer · server settings: /model, /session"
	})
	overlay = ctx.Overlay("settings", &settingsOverlay{framed: framed, list: list}, pitui.OverlaySpec{Width: widthOf(100), Anchor: "bottom-center"})
	return nil
}

type settingsOverlay struct {
	framed *pitui.Container
	list   *pitui.SettingsList
}

func (o *settingsOverlay) Render(width int) []string { return o.framed.Render(width) }
func (o *settingsOverlay) Invalidate()               { o.framed.Invalidate() }
func (o *settingsOverlay) HandleInput(data string)   { o.list.HandleInput(data) }

// KeyCapture waits for one key and turns it into pi's key id (`ctrl+k`, `alt+enter`); Esc cancels.
type KeyCapture struct {
	focused bool
	ctx     runtime.TuiContext
	title   string
	current string
	// done gets the key; unbind is Backspace, restore Delete; cancelled is Esc.
	done func(key string, action KeyCaptureAction)
	err  string
}

// KeyCaptureAction is what a captured key asks for.
type KeyCaptureAction int

// The captures.
const (
	KeyBind KeyCaptureAction = iota
	KeyUnbind
	KeyRestore
	KeyCancel
)

// NewKeyCapture is the KeyCapture constructor.
func NewKeyCapture(ctx runtime.TuiContext, title, current string, done func(key string, action KeyCaptureAction)) *KeyCapture {
	return &KeyCapture{ctx: ctx, title: title, current: current, done: done}
}

// SetFocused records the focus.
func (c *KeyCapture) SetFocused(focused bool) { c.focused = focused }

// HandleInput captures the key.
func (c *KeyCapture) HandleInput(data string) {
	if pitui.IsKeyRelease(data) {
		return
	}
	key, ok := pitui.ParseKey(data)
	switch {
	case ok && key == "escape":
		c.done("", KeyCancel)
	case ok && key == "backspace":
		c.done("", KeyUnbind)
	case ok && key == "delete":
		c.done("", KeyRestore)
	case ok && prefs.IsKeyID(key):
		c.done(key, KeyBind)
	default:
		c.err = "That key cannot be bound"
		c.ctx.RequestRender()
	}
}

// Render renders the prompt.
func (c *KeyCapture) Render(width int) []string {
	current := c.ctx.Theme().Current()
	line := func(text string) string { return truncate(text, width) }
	rule := current.Fg("border", strings.Repeat("─", max(1, width)))
	shown := c.current
	if shown == "" {
		shown = "unbound"
	}
	lines := []string{rule, line(current.Fg("accent", current.Bold("Rebind: "+render.LineText(c.title)))), line("Current: " + shown), line("Press the new key.")}
	if c.err != "" {
		lines = append(lines, line(current.Fg("warning", c.err)))
	}
	return append(lines, line(current.Fg("dim", "Esc cancels · Backspace unbinds · Delete restores the default")), rule)
}

// Invalidate has nothing to drop.
func (c *KeyCapture) Invalidate() {}

// OpenKeybindings is `/keybindings`: every binding with its keys; Enter rebinds through `savePrefs`. Launcher-owned keys are listed only.
func OpenKeybindings(ctx runtime.TuiContext) {
	definitions := prefs.BuiltinKeybindings(prefs.DefaultKeyPlatform)
	rows := func() []components.ListRow {
		type entry struct{ id, title string }
		var all []entry
		known := map[string]bool{}
		for _, item := range ctx.Commands().List(nil) {
			all = append(all, entry{item.ID, item.Command.Title})
			known[item.ID] = true
		}
		ids := make([]string, 0, len(definitions))
		for id := range definitions {
			if !known[id] && (strings.HasPrefix(id, "tui.") || prefs.IsPrivileged(id)) {
				ids = append(ids, id)
			}
		}
		slices.Sort(ids)
		for _, id := range ids {
			title := definitions[id].Description
			if title == "" {
				title = id
			}
			all = append(all, entry{id, title})
		}
		out := make([]components.ListRow, len(all))
		for i, each := range all {
			keys := render.FormatKeyText(strings.Join(ctx.Keys().GetKeys(each.id), "/"), false)
			if keys == "" {
				keys = "unbound"
			}
			if prefs.IsPrivileged(each.id) {
				out[i] = components.ListRow{Key: each.id, Label: render.LineText(each.title), Detail: keys + " · launcher-owned (edit keybindings.json)", Color: "muted"}
			} else {
				out[i] = components.ListRow{Key: each.id, Label: render.LineText(each.title), Detail: keys}
			}
		}
		return out
	}
	var overlay runtime.TuiOverlayHandle
	overlay = ctx.Overlay("keybindings", components.NewListOverlay(runtime.Selectors(ctx), components.ListOverlayOptions{
		Title: func() string { return "Keybindings" },
		Rows:  rows,
		OnSelect: func(row components.ListRow) {
			if prefs.IsPrivileged(row.Key) {
				ctx.Notify("Launcher-owned keys change only in your own keybindings.json", runtime.NoticeWarning)
				return
			}
			var capture runtime.TuiOverlayHandle
			capture = ctx.Overlay("key-capture", NewKeyCapture(ctx, row.Label, row.Detail, func(key string, action KeyCaptureAction) {
				capture.Close()
				if action == KeyCancel {
					return
				}
				var value any = key
				switch action {
				case KeyUnbind:
					value = []string{}
				case KeyRestore:
					value = nil
				}
				go func() {
					result := ctx.Prefs().Save(map[string]any{"keybindings": map[string]any{row.Key: value}})
					notifySave(ctx, "Keybinding", result, "Keybinding saved; /hotkeys lists any conflict")
				}()
			}), pitui.OverlaySpec{Width: widthOf(100), Anchor: "bottom-center"})
		},
		OnClose: func() { overlay.Close() },
		Height:  14,
	}), pitui.OverlaySpec{Width: widthOf(100), Anchor: "bottom-center"})
}
