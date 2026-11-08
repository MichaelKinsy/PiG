package tui

// Runtime-level commands (exit, reload, view toggles, palette, /hotkeys), contributed through the same TuiCommands registry as plugin commands so they are
// rebindable, listed and conflict-checked (p2-tui-port.md section 6.3). Like the header/footer fallbacks (section 7.1), the worker runtime always loads this
// facet.
//
// pi: apps/p2/src/tui/core-commands.ts

import (
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	pitui "github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// KeybindingSources are the config locations as display text only: the worker never reads files (the launcher does).
var KeybindingSources = []string{"~/.pi/agent/keybindings.json", "~/.pi/p2-tui/prefs.json (keybindings)"}

func border(ctx runtime.TuiContext, width int) string {
	return ctx.Theme().Current().Fg("border", strings.Repeat("─", max(1, width)))
}

func truncate(text string, width int) string {
	return widthx.TruncateToWidth(text, width, "…", false)
}

// padEnd pads with spaces to n UTF-16 units, as String.padEnd does.
func padEnd(text string, n int) string {
	if units := len(utf16.Encode([]rune(text))); units < n {
		return text + strings.Repeat(" ", n-units)
	}
	return text
}

type paletteItem struct{ id, title, detail string }

// CommandPalette is the fuzzy search over the commands applicable in the context it was opened from.
type CommandPalette struct {
	focused bool
	ctx     runtime.TuiContext
	input   *pitui.TextInput
	list    *pitui.SelectList
	items   []paletteItem
	onDone  func(id string, ok bool)
}

// NewCommandPalette lists the commands applicable now; onDone gets the chosen id, or ok false when cancelled.
func NewCommandPalette(ctx runtime.TuiContext, onDone func(id string, ok bool)) *CommandPalette {
	active := map[string]bool{}
	for term := range ctx.Commands().Contexts() {
		if !strings.HasPrefix(term, "overlay") {
			active[term] = true
		}
	}
	p := &CommandPalette{ctx: ctx, input: pitui.NewInput(pitui.InputOptions{}), onDone: onDone}
	for _, item := range ctx.Commands().List(active) {
		if item.Command.HiddenFromPalette || item.ID == "p2.palette" {
			continue
		}
		var detail []string
		for _, part := range []string{render.FormatKeyText(strings.Join(item.Keys, "/"), false), slashText(item.Slash), item.Command.Category} {
			if part != "" {
				detail = append(detail, part)
			}
		}
		p.items = append(p.items, paletteItem{item.ID, item.Command.Title, strings.Join(detail, " · ")})
	}
	p.filter()
	return p
}

func slashText(name string) string {
	if name == "" {
		return ""
	}
	return "/" + name
}

func (p *CommandPalette) filter() {
	items := pitui.FuzzyFilter(slices.Clone(p.items), p.input.GetValue(), func(item paletteItem) string { return item.title + " " + item.id + " " + item.detail })
	selectItems := make([]pitui.SelectItem, len(items))
	for i, item := range items {
		selectItems[i] = pitui.SelectItem{Value: item.id, Label: item.title, Description: item.detail}
	}
	p.list = pitui.NewSelectList(selectItems, 10, p.ctx.Theme().SelectList)
	p.list.OnSelect = func(item pitui.SelectItem) { p.onDone(item.Value, true) }
	p.list.OnCancel = func() { p.onDone("", false) }
}

// SetFocused records the focus.
func (p *CommandPalette) SetFocused(focused bool) { p.focused = focused }

// HandleInput routes list keys to the list, the rest to the filter.
func (p *CommandPalette) HandleInput(data string) {
	keys := p.ctx.Keys()
	for _, id := range []string{"tui.select.up", "tui.select.down", "tui.select.confirm", "tui.select.cancel"} {
		if keys.Matches(data, id) {
			p.list.HandleInput(data)
			return
		}
	}
	p.input.HandleInput(data)
	p.filter()
}

// Render renders the palette.
func (p *CommandPalette) Render(width int) []string {
	p.input.SetFocused(p.focused)
	current := p.ctx.Theme().Current()
	lines := []string{border(p.ctx, width), truncate(current.Fg("accent", "Commands"), width)}
	lines = append(lines, p.input.Render(width)...)
	lines = append(lines, p.list.Render(width)...)
	return append(lines, border(p.ctx, width))
}

// Invalidate drops cached renders.
func (p *CommandPalette) Invalidate() {
	p.input.Invalidate()
	p.list.Invalidate()
}

// HotkeysText is /hotkeys, generated from the registry: commands by category with contexts, launcher keys, conflicts, sources.
func HotkeysText(ctx runtime.TuiContext) []string {
	report := ctx.Commands().Report()
	current := ctx.Theme().Current()
	keys := func(list []string) string {
		if len(list) == 0 {
			return "unbound"
		}
		return render.FormatKeyText(strings.Join(list, "/"), false)
	}
	var lines []string
	heading := func(text string) { lines = append(lines, "", current.Bold(current.Fg("accent", text))) }
	row := func(left, right string) { lines = append(lines, "  "+current.Fg("dim", padEnd(left, 26))+" "+right) }
	var categories []string
	byCategory := map[string][]runtime.TuiCommandListing{}
	for _, item := range ctx.Commands().List(nil) {
		category := item.Command.Category
		if category == "" {
			category = "General"
		}
		if _, ok := byCategory[category]; !ok {
			categories = append(categories, category)
		}
		byCategory[category] = append(byCategory[category], item)
	}
	for _, category := range categories {
		heading(category)
		for _, item := range byCategory[category] {
			title := item.Command.Title
			if item.Slash != "" {
				hint := ""
				if item.Command.Slash != nil && item.Command.Slash.ArgumentHint != "" {
					hint = " " + item.Command.Slash.ArgumentHint
				}
				title += current.Fg("muted", "  /"+item.Slash+hint)
			}
			if item.Command.When != "" {
				title += current.Fg("muted", "  ["+item.Command.When+"]")
			}
			row(keys(item.Keys), title)
		}
	}
	definitions := prefs.BuiltinKeybindings(prefs.DefaultKeyPlatform)
	// Keys of prompts that are not commands (owner): where a session runs, as the footer writes it.
	if ctx.Role() == runtime.RoleOwner {
		heading("Working directory and environment")
		row(keys(ctx.Keys().GetKeys("tui.input.tab")), "New-session and /cd prompts: next environment (this device, then /envs)")
		row("[env:]path", "As the footer shows it: gpu:/data on an environment, /data on this device")
		row("/cd [[env:]path]", "Relative to the session's directory; Tab completes folders; local: is this device")
		row("/envs", "Enter moves the session to the environment (● marks its current one); Ctrl+T test, Ctrl+K host key, Ctrl+F forget")
	}
	heading("Editor and transcript (pi-tui)")
	ids := make([]string, 0, len(definitions))
	for id := range definitions {
		if strings.HasPrefix(id, "tui.editor.") || strings.HasPrefix(id, "tui.altScreen.") {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	describe := func(id string) string {
		if description := definitions[id].Description; description != "" {
			return description
		}
		return id
	}
	for _, id := range ids {
		row(keys(ctx.Keys().GetKeys(id)), describe(id))
	}
	heading("Launcher-owned (handled by your local launcher, never by server code)")
	for _, id := range prefs.PrivilegedActions {
		row(keys(report.Privileged.Keys[id]), describe(id))
	}
	if len(report.Conflicts) > 0 || len(report.Unknown) > 0 || len(report.Problems) > 0 {
		heading("Conflicts and problems")
		for _, conflict := range report.Conflicts {
			lines = append(lines, "  "+current.Fg("warning", conflict.Message))
		}
		for _, id := range report.Unknown {
			lines = append(lines, "  "+current.Fg("warning", "Unknown keybinding id kept: "+id))
		}
		for _, problem := range report.Problems {
			lines = append(lines, "  "+current.Fg("warning", problem))
		}
	}
	heading("Configuration")
	lines = append(lines, "  "+current.Fg("muted", "Keybindings: "+strings.Join(KeybindingSources, ", then ")+"; server defaults apply below them."))
	return lines[1:]
}

// TextOverlay is a scrollable read-only text overlay.
type TextOverlay struct {
	focused bool
	offset  int
	ctx     runtime.TuiContext
	title   string
	lines   func() []string
	onClose func()
}

// NewTextOverlay is the TextOverlay constructor.
func NewTextOverlay(ctx runtime.TuiContext, title string, lines func() []string, onClose func()) *TextOverlay {
	return &TextOverlay{ctx: ctx, title: title, lines: lines, onClose: onClose}
}

const textOverlayHeight = 18

// SetFocused records the focus.
func (o *TextOverlay) SetFocused(focused bool) { o.focused = focused }

// HandleInput scrolls or closes.
func (o *TextOverlay) HandleInput(data string) {
	keys := o.ctx.Keys()
	maxOffset := max(0, len(o.lines())-textOverlayHeight)
	switch {
	case keys.Matches(data, "tui.select.cancel") || keys.Matches(data, "tui.select.confirm"):
		o.onClose()
	case keys.Matches(data, "tui.select.up"):
		o.offset = max(0, o.offset-1)
	case keys.Matches(data, "tui.select.down"):
		o.offset = min(maxOffset, o.offset+1)
	case keys.Matches(data, "tui.select.pageUp"):
		o.offset = max(0, o.offset-textOverlayHeight)
	case keys.Matches(data, "tui.select.pageDown"):
		o.offset = min(maxOffset, o.offset+textOverlayHeight)
	}
}

// Render renders the visible window.
func (o *TextOverlay) Render(width int) []string {
	current := o.ctx.Theme().Current()
	all := o.lines()
	end := min(len(all), o.offset+textOverlayHeight)
	visible := all[min(o.offset, end):end]
	lines := []string{border(o.ctx, width), truncate(current.Fg("accent", o.title), width)}
	for _, line := range visible {
		lines = append(lines, truncate(line, width))
	}
	lines = append(lines,
		truncate(current.Fg("dim", itoa(o.offset+1)+"-"+itoa(o.offset+len(visible))+" of "+itoa(len(all))+" · ↑↓ PgUp/PgDn · Esc closes"), width),
		border(o.ctx, width))
	return lines
}

// Invalidate has nothing to drop.
func (o *TextOverlay) Invalidate() {}

func itoa(n int) string { return strconv.Itoa(n) }

var widthOf = func(percent float64) *pitui.OverlayValue { return &pitui.OverlayValue{Value: percent, Percent: true} }

// CoreCommands are the runtime-level commands.
func CoreCommands() p2runtime.Keyed[runtime.TuiCommand] {
	cmd := p2runtime.Entry[runtime.TuiCommand]
	return p2runtime.Keyed[runtime.TuiCommand]{
		cmd("p2.palette", runtime.TuiCommand{
			Title: "Command palette", Category: "General", Keys: []string{"f1"}, When: "!overlay", HiddenFromPalette: true,
			Run: func(ctx runtime.TuiContext, _ string) error {
				var handle runtime.TuiOverlayHandle
				handle = ctx.Overlay("palette", NewCommandPalette(ctx, func(id string, ok bool) {
					handle.Close()
					if ok {
						ctx.Commands().Run(id, "")
					}
				}), pitui.OverlaySpec{Width: widthOf(70), Anchor: "top-center", OffsetY: 2})
				return nil
			},
		}),
		cmd("p2.hotkeys", runtime.TuiCommand{
			Title: "Keyboard shortcuts", Category: "General", Slash: &runtime.TuiCommandSlash{Name: "hotkeys"}, When: "!overlay",
			Run: func(ctx runtime.TuiContext, _ string) error {
				var handle runtime.TuiOverlayHandle
				handle = ctx.Overlay("hotkeys", NewTextOverlay(ctx, "Keyboard shortcuts", func() []string { return HotkeysText(ctx) }, func() { handle.Close() }),
					pitui.OverlaySpec{Width: widthOf(90), Anchor: "top-center", OffsetY: 1})
				return nil
			},
		}),
		// P10: the visitor's own preferences; saved by the launcher (savePrefs) after it shows the diff.
		cmd("p2.settings", runtime.TuiCommand{
			Title: "Settings", Category: "General", Slash: &runtime.TuiCommandSlash{Name: "settings"}, When: "!overlay",
			Run: func(ctx runtime.TuiContext, _ string) error { return OpenSettings(ctx) },
		}),
		cmd("p2.keybindings", runtime.TuiCommand{
			Title: "Rebind keys", Category: "General", Slash: &runtime.TuiCommandSlash{Name: "keybindings"}, When: "!overlay",
			Run: func(ctx runtime.TuiContext, _ string) error { OpenKeybindings(ctx); return nil },
		}),
		cmd("app.exit", runtime.TuiCommand{
			Title: "Exit", Category: "General",
			// pi: Ctrl+D exits only on an empty editor; otherwise the editor deletes forward.
			When: "!overlay && (!editorFocused || editorEmpty)", Slash: &runtime.TuiCommandSlash{Name: "quit"},
			Run: func(ctx runtime.TuiContext, _ string) error { ctx.Exit(); return nil },
		}),
		cmd("p2.tui.reload", runtime.TuiCommand{
			Title: "Reload the TUI from the server", Category: "General", Slash: &runtime.TuiCommandSlash{Name: "reload"},
			Run: func(ctx runtime.TuiContext, _ string) error { ctx.Reload(); return nil },
		}),
		cmd("p2.panel.focus", runtime.TuiCommand{
			Title: "Focus the side panel or the editor", Category: "View", Keys: []string{"f2"}, When: "panel && !overlay",
			Run: func(ctx runtime.TuiContext, _ string) error { ctx.Panel().Focus(!ctx.Panel().Focused()); return nil },
		}),
		cmd("p2.panel.close", runtime.TuiCommand{
			Title: "Close the side panel", Category: "View", When: "panel && !overlay", Slash: &runtime.TuiCommandSlash{Name: "close-panel"},
			Run: func(ctx runtime.TuiContext, _ string) error { ctx.Panel().Close(); return nil },
		}),
		cmd("app.tools.expand", runtime.TuiCommand{
			Title: "Toggle tool output", Category: "View", When: "!overlay",
			Run: func(ctx runtime.TuiContext, _ string) error {
				ctx.SetExpandedTools(!ctx.View().ExpandedTools)
				return nil
			},
		}),
		cmd("app.thinking.toggle", runtime.TuiCommand{
			Title: "Toggle thinking blocks", Category: "View", When: "!overlay",
			Run: func(ctx runtime.TuiContext, _ string) error {
				ctx.SetShowThinking(!ctx.View().ShowThinking)
				return nil
			},
		}),
	}
}

// CoreFacet is the facet the worker runtime always loads (tests load it next to plugin facets): it serves the TUI's registries and contributes the core
// commands and renderers.
var CoreFacet = host.DefineFacet(func(facet host.FacetHost, self host.FacetSelf) error {
	for _, serve := range []func(host.FacetHost) error{
		runtime.TuiSlots.Serve, runtime.TuiCommands.Serve, runtime.TuiSlashSources.Serve, runtime.TuiToolRenderers.Serve,
		runtime.TuiToolRendererResolvers.Serve, runtime.TuiSessionSources.Serve, runtime.TuiSessionPlaces.Serve,
		runtime.TuiSettingFields.Serve, runtime.TuiEntryRenderers.Serve, runtime.TuiMarkdownTransformers.Serve,
	} {
		if err := serve(facet); err != nil {
			return err
		}
	}
	if err := runtime.TuiCommands.Contribute(self, CoreCommands()...); err != nil {
		return err
	}
	// pi's built-in transformer, ahead of plugin transformers ("10:…").
	if err := runtime.TuiMarkdownTransformers.Contribute(self, p2runtime.Entry("00:mermaid", render.MarkdownTransformer(components.MermaidMarkdownTransformer))); err != nil {
		return err
	}
	// The host's user shell entries (`!cmd`, runtime/shell.go) as pi's bash execution component.
	return runtime.TuiEntryRenderers.Contribute(self, p2runtime.Entry(p2runtime.ShellEntry, components.ShellEntryRenderer))
})
