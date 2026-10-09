package tui

// BashExecutionComponent renders `!cmd` and `!!cmd` output between dynamic borders. Output uses the shared width-aware text layout. Collapsed blocks retain the final previewLines visual rows; expanded blocks retain every available row. The excludeFromContext flag changes only context inclusion and header presentation.

import (
	"fmt"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/truncate"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// previewLines is the collapsed visual-row limit.
const previewLines = 20

// BashExecutionComponent displays a `!cmd` invocation with streaming
// output, top/bottom borders, and expand/collapse support.
type BashExecutionComponent struct {
	Container
	contentContainer   *Container
	stale              bool // content changed since contentContainer was last rebuilt
	command            string
	excludeFromContext bool
	output             strings.Builder
	status             bashStatus
	exitCode           *int
	startedAt          time.Time
	finishedAt         time.Time
	truncated          bool
	fullOutputPath     string
	expanded           bool
	loader             *Loader // animated spinner while running
	outputPad          int     // horizontal padding of the header, output and status rows (bash-execution.ts outputPad)
}

type bashStatus int

const (
	bashStatusRunning bashStatus = iota
	bashStatusComplete
	bashStatusCancelled
)

// IsDirty reports whether the block needs re-rendering. While running it
// embeds an animated Loader spinner advanced by the 100ms tick loop, but the
// tick ticks the loader without Invalidating this block. Reporting dirty
// while running keeps the per-child render cache from freezing the spinner.
func (b *BashExecutionComponent) IsDirty() bool {
	return b.stale || b.status == bashStatusRunning || b.Container.IsDirty()
}

// pig additive (D91): SurfaceLive reports that the spinner, which ticks without invalidating the
// block, draws in it.
func (b *BashExecutionComponent) SurfaceLive() bool { return b.status == bashStatusRunning }

// NewBashExecutionComponent returns a freshly-constructed bash-execution component in the running state
// (bash-execution.ts constructor(command, ui, excludeFromContext = false, outputPad = 1)). ui is the TUI the running Loader asks
// for a render after each spinner change; nil asks for none.
func NewBashExecutionComponent(command string, ui TUI, excludeFromContext bool, outputPad int) *BashExecutionComponent {
	b := &BashExecutionComponent{
		command:            command,
		excludeFromContext: excludeFromContext,
		status:             bashStatusRunning,
		outputPad:          outputPad,
		startedAt:          time.Now(),
		contentContainer:   NewContainer(),
	}
	// upstream: bash-execution.ts constructor: the loader's spinner and message, and both borders, style with theme.fg at render time.
	colorKey := b.colorKey()
	b.loader = NewLoader(ui,
		func(spinner string) string { return ActiveTheme().Fg(colorKey, spinner) },
		func(text string) string { return ActiveTheme().Fg("muted", text) },
		"Running... ("+ActionKeyText(KBSelectCancel)+" to cancel)", nil,
	)
	border := func(text string) string { return ActiveTheme().Fg(colorKey, text) }
	// upstream: bash-execution.ts constructor children: a spacer, the top border, the content and the bottom border.
	b.Add(NewSpacer(1))
	b.Add(NewDynamicBorder(border))
	b.Add(b.contentContainer)
	b.Add(NewDynamicBorder(border))
	b.updateDisplay()
	return b
}

// colorKey is upstream's colorKey: "dim" marks a `!!` command, whose output is excluded from the model context, and "bashMode" any other.
func (b *BashExecutionComponent) colorKey() string {
	if b.excludeFromContext {
		return "dim"
	}
	return "bashMode"
}

// Loader returns the embedded spinner when the block is still running,
// nil otherwise. The animation driver ticks it to advance the spinner.
func (b *BashExecutionComponent) Loader() *Loader {
	if b.status != bashStatusRunning {
		return nil
	}
	return b.loader
}

// AppendOutput streams a chunk into the block. It strips ANSI codes and normalizes line endings first (bash-execution.ts:appendOutput), so output that bypassed the bash executor, such as a user_bash extension result, renders the same.
func (b *BashExecutionComponent) AppendOutput(chunk string) {
	clean := strings.ReplaceAll(strings.ReplaceAll(widthx.StripAnsi(chunk), "\r\n", "\n"), "\r", "\n")
	b.output.WriteString(clean)
	b.markStale()
}

// GetOutput returns the accumulated output (bash-execution.ts:getOutput).
func (b *BashExecutionComponent) GetOutput() string {
	return b.output.String()
}

// GetCommand returns the command that was executed (bash-execution.ts:getCommand).
func (b *BashExecutionComponent) GetCommand() string {
	return b.command
}

// SetComplete transitions the block out of the running state (bash-execution.ts setComplete(exitCode, cancelled, truncationResult?, fullOutputPath?)).
// A non-nil truncationResult with Truncated set marks the output as truncated; fullOutputPath is the file holding the untruncated output and, with a
// truncated result, adds the "Output truncated. Full output: <path>" row. The output already streamed into the block is kept; SetCompleteWithOutput
// replaces it with the durable output.
func (b *BashExecutionComponent) SetComplete(exitCode *int, cancelled bool, truncationResult *TruncationResult, fullOutputPath string) {
	b.finishBashExecution(exitCode, cancelled, truncationResult != nil && truncationResult.Truncated, fullOutputPath)
}

// SetCompleteWithOutput replaces the streaming preview with the durable truncated output and records its full-output path before completing the block.
func (b *BashExecutionComponent) SetCompleteWithOutput(exitCode *int, cancelled, truncated bool, output, fullOutputPath string) {
	b.output.Reset()
	b.output.WriteString(output)
	b.SetComplete(exitCode, cancelled, truncationFlag(truncated), fullOutputPath)
}

// truncationFlag is `truncated ? { truncated: true } : undefined`, the argument interactive-mode.ts:3883 passes.
func truncationFlag(truncated bool) *TruncationResult {
	if !truncated {
		return nil
	}
	return &TruncationResult{Truncated: true}
}

func (b *BashExecutionComponent) finishBashExecution(exitCode *int, cancelled, truncated bool, fullOutputPath string) {
	if cancelled {
		b.status = bashStatusCancelled
	} else {
		b.status = bashStatusComplete
	}
	b.exitCode = exitCode
	b.truncated = truncated
	b.fullOutputPath = fullOutputPath
	b.finishedAt = time.Now()
	b.updateDisplay()
	b.Container.Invalidate()
}

// SetOutputPad is Pi's setOutputPad(outputPad): the padding of the command header, output and status rows, rebuilt at once (bash-execution.ts:73).
func (b *BashExecutionComponent) SetOutputPad(outputPad int) {
	b.outputPad = outputPad
	b.updateDisplay()
	b.Container.Invalidate()
}

// SetExpanded forces the body open (true) or collapsed-to-preview
// (false). Driven by the global Ctrl+O toggle so all bash blocks land
// in the same state as their tool-execution peers.
func (b *BashExecutionComponent) SetExpanded(expanded bool) {
	b.expanded = expanded
	b.updateDisplay()
	b.Container.Invalidate()
}

// markStale defers the rebuild to the next Render, so a stream of output chunks does not re-split the whole output per chunk.
func (b *BashExecutionComponent) markStale() {
	b.stale = true
	b.Container.Invalidate()
}

// Invalidate rebuilds the content so theme-baked colors follow the active theme (bash-execution.ts invalidate).
func (b *BashExecutionComponent) Invalidate() {
	b.Container.Invalidate()
	b.stale = true
}

// Render rebuilds the content when it changed, then renders the children. Commands and output wrap to the current terminal width, and collapsed previews retain the last previewLines visual rows.
func (b *BashExecutionComponent) Render(width int) []string {
	if width < 1 {
		width = 1
	}
	if b.stale {
		b.updateDisplay()
	}
	return b.Container.Render(width)
}

// updateDisplay rebuilds the content container: command header, output preview, then the loader or status rows (bash-execution.ts updateDisplay).
func (b *BashExecutionComponent) updateDisplay() {
	b.stale = false
	theme := ActiveTheme()
	children := make([]Component, 0, 4)

	// Upstream renders the command with Text(theme.fg(colorKey, theme.bold(`$ ${command}`)), outputPad, 0), including wrapped and multiline commands.
	header := theme.Fg(b.colorKey(), theme.Bold("$ "+b.command))
	children = append(children, NewPaddedText(header, b.outputPad, 0, nil))

	// Body: output split on \n. Preserve trailing newline so the final empty line is rendered as a blank row inside the box (bash-execution.ts:140-160: the available lines of "...\n".split("\n") end with an empty string).
	// Upstream first applies the bash tool's context limits (truncateTail with DEFAULT_MAX_LINES and DEFAULT_MAX_BYTES), so the block shows and counts only the lines the model would see (bash-execution.ts:122-130).
	contextTruncation := truncate.TruncateTail(b.output.String(), truncate.DefaultMaxBytes, truncate.DefaultMaxLines)
	bodyRaw, contextTruncated := contextTruncation.Content, contextTruncation.Truncated
	var allLines []string
	if bodyRaw != "" {
		allLines = strings.Split(bodyRaw, "\n")
	}
	// Preview selection follows upstream in two stages: retain the last 20 logical lines, then retain the last 20 visual rows after width-aware wrapping.
	previewLogicalLines := allLines
	if len(previewLogicalLines) > previewLines {
		previewLogicalLines = previewLogicalLines[len(previewLogicalLines)-previewLines:]
	}
	hidden := len(allLines) - len(previewLogicalLines)
	displayLines := previewLogicalLines
	if b.expanded {
		displayLines = allLines
	}
	if len(displayLines) > 0 {
		styled := make([]string, len(displayLines))
		for i, line := range displayLines {
			styled[i] = theme.Fg("muted", line)
		}
		text := "\n" + strings.Join(styled, "\n")
		if b.expanded {
			children = append(children, NewPaddedText(text, b.outputPad, 0, nil))
		} else {
			children = append(children, &bashPreview{text: text, pad: b.outputPad})
		}
	}

	if b.status == bashStatusRunning {
		children = append(children, b.loader)
	} else if statusLines := b.statusLines(hidden, contextTruncated); len(statusLines) > 0 {
		// Upstream emits the parts inside Text("\n" + parts.join("\n"), 1, 0).
		children = append(children, NewPaddedText("\n"+strings.Join(statusLines, "\n"), b.outputPad, 0, nil))
	}
	b.contentContainer.SetChildren(children...)
}

// bashPreview is the collapsed output: the last previewLines visual rows, cached per width (bash-execution.ts inline render object).
type bashPreview struct {
	text  string
	pad   int
	width int
	lines []string
}

func (p *bashPreview) Render(width int) []string {
	if p.lines == nil || p.width != width {
		p.lines = TruncateToVisualLines(p.text, previewLines, width, p.pad).VisualLines
		p.width = width
	}
	return p.lines
}

func (p *bashPreview) Invalidate() { p.lines = nil }

// statusLines returns the collapse, completion, and durable-truncation status rows in display order. Successful untruncated output with no hidden logical lines has no status row.
func (b *BashExecutionComponent) statusLines(hidden int, contextTruncated bool) []string {
	theme := ActiveTheme()
	lines := make([]string, 0, 3)

	// Collapse hint (bash-execution.ts updateDisplay: muted text around keyHint("app.tools.expand", ...)).
	if hidden > 0 {
		expand := theme.Fg("dim", AppKeyText("app.tools.expand", "ctrl+o"))
		if b.expanded {
			lines = append(lines, theme.Fg("muted", "(")+expand+theme.Fg("muted", " to collapse")+theme.Fg("muted", ")"))
		} else {
			lines = append(lines, theme.Fg("muted", fmt.Sprintf("... %d more lines (", hidden))+expand+theme.Fg("muted", " to expand")+theme.Fg("muted", ")"))
		}
	}

	switch b.status {
	case bashStatusCancelled:
		lines = append(lines, theme.Fg("warning", "(cancelled)"))
	case bashStatusComplete:
		// Upstream renders (exit N) in the error color for a non-zero exit code only.
		if b.exitCode != nil && *b.exitCode != 0 {
			lines = append(lines, theme.Fg("error", fmt.Sprintf("(exit %d)", *b.exitCode)))
		}
	}
	// The truncation warning does not depend on the status (bash-execution.ts:200-203).
	if (b.truncated || contextTruncated) && b.fullOutputPath != "" {
		lines = append(lines, theme.Fg("warning", "Output truncated. Full output: "+b.fullOutputPath))
	}
	return lines
}

// Color tokens. Truecolor matches upstream theme/dark.json:
//
//	bashMode  #b5bd68  green   (181,189,104)
//	dim       #666666  dimGray (102,102,102)
//	muted     #808080  gray    (128,128,128)
//
// Each follows the active theme's color mode.
func bashHeaderColor() string { return ActiveTheme().BashMode }
func bashDimColor() string    { return ActiveTheme().Dim }
func bashMutedColor() string  { return ActiveTheme().Muted }
