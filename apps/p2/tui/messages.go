// Package tui is p2's terminal UI: the parts that sit above the component, tool and runtime packages. Pi's own tui package is imported as pitui.
package tui

import (
	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tools"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

// TranscriptOptions are the optional parts of TranscriptComponent.
type TranscriptOptions struct {
	// Theme is the theme to draw with; nil selects the built-in dark theme in true color.
	Theme *theme.ThemeRef
	Cwd   string
	// Renderers are the tool renderers by tool name; plugins register theirs in runtime.TuiToolRenderers.
	Renderers map[string]*tools.ToolRenderers
}

// staticContext is the context of a transcript outside a runtime: no transformers, settings or entry renderers.
type staticContext struct {
	theme     *theme.ThemeRef
	cwd       string
	renderers map[string]*tools.ToolRenderers
}

func (c *staticContext) Theme() *theme.ThemeRef { return c.theme }
func (c *staticContext) KeyText(id string) string {
	keys := pitui.GetKeybindings().GetKeys(pitui.TUIKeybinding(id))
	return render.FormatKeyText(joinKeys(keys), false)
}
func (c *staticContext) RequestRender()                                     {}
func (c *staticContext) MarkdownTransformers() []render.MarkdownTransformer { return nil }
func (c *staticContext) Settings() *prefs.TuiSettings                       { return nil }
func (c *staticContext) OutputPad() int                                     { return 1 }
func (c *staticContext) Cwd() string                                        { return c.cwd }
func (c *staticContext) EntryRenderers() map[string]*components.EntryRenderer {
	return nil
}
func (c *staticContext) ToolRenderers(name string) *tools.ToolRenderers {
	return tools.ResolveToolRenderers(name, nil, c.renderers, prefs.TuiSettings{})
}

func joinKeys(keys []string) string {
	out := ""
	for i, key := range keys {
		if i > 0 {
			out += "/"
		}
		out += key
	}
	return out
}

// TranscriptComponent is a whole transcript as one component outside a runtime (tests, previews): the chat screen's TranscriptView with the
// built-in dark theme and the current keymap's hints, tools drawn by options.Renderers or pi's generic card.
func TranscriptComponent(tr *transcript.Transcript, showThinking, expandedTools bool, options TranscriptOptions) (*components.TranscriptView, error) {
	ref := options.Theme
	if ref == nil {
		dark, _ := theme.BuiltinTheme("dark")
		built, err := theme.CreateTheme(dark, pitui.TerminalColorModeTrueColor, theme.TerminalState{})
		if err != nil {
			return nil, err
		}
		ref = theme.NewThemeRef(built)
	}
	view := components.NewTranscriptView(&staticContext{theme: ref, cwd: options.Cwd, renderers: options.Renderers},
		components.ViewState{Expanded: expandedTools, ShowThinking: showThinking}, nil)
	view.Sync(tr, nil)
	return view, nil
}
