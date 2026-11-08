package components

// Copied from coding-agent interactive/components/bash-execution.ts (p2-tui-port.md section 10.8, `!cmd`/`!!cmd`). Changes: the instance theme and
// key hints (section 5.4) instead of globals; the output is server text, sanitized (`SafeText`) instead of `stripAnsi`; pi's `truncateTail` is
// `ShellTail` (runtime/shell.go, the same limits); the loader exists only for a live command (an entry renderer has no TUI); the author is named
// for others' commands.
//
// pi: apps/p2/src/tui/components/bash-execution.ts

import (
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/components/visualtruncate"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/tui"
)

// previewLines is the preview line limit when not expanded (matches tool execution behavior).
const previewLines = 20

type bashStatus string

const (
	bashRunning   bashStatus = "running"
	bashComplete  bashStatus = "complete"
	bashCancelled bashStatus = "cancelled"
	bashError     bashStatus = "error"
)

// BashExecutionOptions are the options of a BashExecutionComponent.
type BashExecutionOptions struct {
	// ExcludeFromContext marks `!!` commands, whose output is excluded from the model context.
	ExcludeFromContext bool
	// OutputPad is the horizontal padding of output; nil is 1.
	OutputPad *int
	// TUI makes the component show a loader while the command runs; nil for a finished command.
	TUI tui.TUI
	// By is shown after the command for someone else's command ("Alice (guest)").
	By string
}

// BashExecutionComponent is a user shell command (`!cmd`) with its output.
type BashExecutionComponent struct {
	*tui.Container
	command        string
	outputLines    []string
	status         bashStatus
	exitCode       *float64
	err            *string
	loader         *tui.Loader
	truncated      bool
	fullOutputPath string
	expanded       bool
	content        *tui.Container
	// colorKey is "dim" for `!!` commands, "bashMode" otherwise.
	colorKey  string
	outputPad int
	ctx       render.RenderContext
	by        string
}

type borderComponent struct{ c *BashExecutionComponent }

func (b borderComponent) Render(width int) []string {
	return []string{b.c.ctx.Theme().Current().Fg(b.c.colorKey, strings.Repeat("─", max(1, width)))}
}
func (borderComponent) Invalidate() {}

// NewBashExecutionComponent is the constructor.
func NewBashExecutionComponent(command string, ctx render.RenderContext, options BashExecutionOptions) *BashExecutionComponent {
	c := &BashExecutionComponent{Container: tui.NewContainer(), ctx: ctx, command: command, by: options.By, colorKey: "bashMode", outputPad: 1, status: bashRunning}
	if options.ExcludeFromContext {
		c.colorKey = "dim"
	}
	if options.OutputPad != nil {
		c.outputPad = *options.OutputPad
	}
	border := borderComponent{c}
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(border)
	c.content = tui.NewContainer()
	c.AddChild(c.content)
	if options.TUI != nil {
		c.loader = tui.NewLoader(options.TUI,
			func(spinner string) string { return ctx.Theme().Current().Fg(c.colorKey, spinner) },
			func(text string) string { return ctx.Theme().Current().Fg("muted", text) },
			"Running... ("+ctx.KeyText("app.interrupt")+" to cancel)", nil)
	}
	c.AddChild(border)
	c.updateDisplay()
	return c
}

// SetExpanded sets whether the output is expanded (shows full output) or collapsed (preview only).
func (c *BashExecutionComponent) SetExpanded(expanded bool) {
	c.expanded = expanded
	c.updateDisplay()
}

// Invalidate drops the caches and rebuilds the display.
func (c *BashExecutionComponent) Invalidate() {
	c.Container.Invalidate()
	c.updateDisplay()
}

// SetOutput replaces the output (p2 publishes the retained tail as state, not chunks).
func (c *BashExecutionComponent) SetOutput(output string, truncated bool) {
	// Server text: terminal controls are stripped; line endings normalized.
	clean := render.SafeText(strings.ReplaceAll(strings.ReplaceAll(output, "\r\n", "\n"), "\r", "\n"))
	c.outputLines = nil
	if clean != "" {
		c.outputLines = strings.Split(clean, "\n")
	}
	c.truncated = truncated
	c.updateDisplay()
}

// BashCompletion are the optional details of a finished command.
type BashCompletion struct {
	Truncated      bool
	FullOutputPath string
	// Error is why the command did not run or finish; nil when it did.
	Error *string
}

// SetComplete shows how the command ended; exitCode is nil when it did not end by itself.
func (c *BashExecutionComponent) SetComplete(exitCode *float64, cancelled bool, options BashCompletion) {
	c.exitCode = exitCode
	c.err = options.Error
	switch {
	case cancelled:
		c.status = bashCancelled
	case options.Error != nil || (exitCode != nil && *exitCode != 0):
		c.status = bashError
	default:
		c.status = bashComplete
	}
	c.truncated = c.truncated || options.Truncated
	c.fullOutputPath = options.FullOutputPath
	if c.loader != nil {
		c.loader.Stop()
	}
	c.updateDisplay()
}

// Dispose stops the loader's timer (a live command's component that is dropped while running).
func (c *BashExecutionComponent) Dispose() {
	if c.loader != nil {
		c.loader.Stop()
	}
}

// previewComponent renders the preview lines with width-aware caching.
type previewComponent struct {
	input         string
	outputPad     int
	cachedWidth   int
	cachedLines   []string
	cachedPresent bool
}

func (p *previewComponent) Render(width int) []string {
	if !p.cachedPresent || p.cachedWidth != width {
		p.cachedLines = visualtruncate.TruncateToVisualLines(p.input, previewLines, width, p.outputPad, false).VisualLines
		p.cachedWidth, p.cachedPresent = width, true
	}
	return p.cachedLines
}

func (p *previewComponent) Invalidate() { p.cachedPresent, p.cachedLines = false, nil }

func (c *BashExecutionComponent) updateDisplay() {
	theme := c.ctx.Theme().Current()
	// Apply truncation for LLM context limits (same limits as bash tool).
	contextText, contextTruncated := runtime.ShellTailDefault(strings.Join(c.outputLines, "\n"))
	// The lines to potentially display (after context truncation).
	var available []string
	if contextText != "" {
		available = strings.Split(contextText, "\n")
	}
	// Apply preview truncation based on expanded state.
	preview := available[max(0, len(available)-previewLines):]
	hidden := len(available) - len(preview)

	c.content.Clear()
	by := ""
	if c.by != "" {
		by = theme.Fg("muted", " · "+render.LineText(c.by))
	}
	c.content.AddChild(tui.NewPaddedText(theme.Fg(c.colorKey, theme.Bold("$ "+render.LineText(c.command)))+by, c.outputPad, 0, nil))

	if len(available) > 0 {
		if c.expanded {
			styled := make([]string, len(available))
			for i, line := range available {
				styled[i] = theme.Fg("muted", line)
			}
			c.content.AddChild(tui.NewPaddedText("\n"+strings.Join(styled, "\n"), c.outputPad, 0, nil))
		} else {
			styled := make([]string, len(preview))
			for i, line := range preview {
				styled[i] = theme.Fg("muted", line)
			}
			c.content.AddChild(&previewComponent{input: "\n" + strings.Join(styled, "\n"), outputPad: c.outputPad})
		}
	}

	if c.status == bashRunning {
		if c.loader != nil {
			c.content.AddChild(c.loader)
		}
		return
	}
	var parts []string
	// How many lines are hidden (collapsed preview).
	if hidden > 0 {
		if c.expanded {
			parts = append(parts, theme.Fg("muted", "(")+render.KeyHint(c.ctx, "app.tools.expand", "to collapse")+theme.Fg("muted", ")"))
		} else {
			parts = append(parts, theme.Fg("muted", "... "+jsnumber.String(float64(hidden))+" more lines (")+render.KeyHint(c.ctx, "app.tools.expand", "to expand")+theme.Fg("muted", ")"))
		}
	}
	switch {
	case c.status == bashCancelled:
		parts = append(parts, theme.Fg("warning", "(cancelled)"))
	case c.status == bashError && c.err != nil:
		parts = append(parts, theme.Fg("error", "("+render.LineText(*c.err)+")"))
	case c.status == bashError:
		code := "undefined"
		if c.exitCode != nil {
			code = jsnumber.String(*c.exitCode)
		}
		parts = append(parts, theme.Fg("error", "(exit "+code+")"))
	}
	// Truncation warning (context truncation, not preview truncation).
	if (c.truncated || contextTruncated) && c.fullOutputPath != "" {
		parts = append(parts, theme.Fg("warning", "Output truncated. Full output: "+render.LineText(c.fullOutputPath)))
	}
	if len(parts) > 0 {
		c.content.AddChild(tui.NewPaddedText("\n"+strings.Join(parts, "\n"), c.outputPad, 0, nil))
	}
}

// ShellAuthor is who ran someone else's command: guests by name; the owner's commands carry no label ("").
func ShellAuthor(author runtime.Author) string {
	if author.Kind == runtime.AuthorGuest {
		return author.Name + " (guest)"
	}
	return ""
}

// EntryRenderContext is the render context of an entry renderer.
type EntryRenderContext interface {
	render.RenderContext
	Expanded() bool
}

// EntryRenderer is a component for a committed entry by entry kind (`TuiEntryRenderer`). A registered kind replaces the transcript's own display of
// the entry (people's messages, notices); a nil component hides it. Entry data is server data: sanitize text you show. A renderer is a pointer so
// the transcript can tell whether the one registered for a kind is the one a view was built with.
type EntryRenderer struct {
	Render func(entry durable.EntryRecord, ctx EntryRenderContext) tui.Component
}

// ShellEntryRenderer is a finished `p2.shell` entry (runtime/shell.go): this component, completed. The core facet registers it, as the entry kind
// is the host's, not a plugin's.
var ShellEntryRenderer = &EntryRenderer{Render: renderShellEntry}

func renderShellEntry(entry durable.EntryRecord, ctx EntryRenderContext) tui.Component {
	data, ok := runtime.ShellDataOf(entry.Data)
	if !ok {
		return nil
	}
	component := NewBashExecutionComponent(data.Command, ctx, BashExecutionOptions{ExcludeFromContext: !data.IncludeInContext, By: ShellAuthor(data.Author)})
	component.SetExpanded(ctx.Expanded())
	component.SetOutput(data.Output, data.Truncated)
	completion := BashCompletion{Truncated: data.Truncated, Error: data.Error}
	if data.FullOutputPath != nil {
		completion.FullOutputPath = *data.FullOutputPath
	}
	component.SetComplete(data.ExitCode, data.Cancelled, completion)
	return component
}
