package tui

// Ports packages/coding-agent/src/modes/interactive/components/bash-execution.ts.

import (
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// previewLines is upstream PREVIEW_LINES, the collapsed visual-row limit.
const previewLines = 20

// bashContextTruncation is upstream updateDisplay's truncateTail(fullOutput, {DEFAULT_MAX_LINES, DEFAULT_MAX_BYTES}): the
// output the block shows and whether it was cut. nil shows the output whole.
var bashContextTruncation func(output string) (content string, truncated bool)

// SetBashContextTruncation installs upstream's truncateTail with the bash tool's default limits, which the TUI cannot
// import (the tools package imports it).
func SetBashContextTruncation(truncate func(output string) (content string, truncated bool)) {
	bashContextTruncation = truncate
}

// BashExecutionBlock is upstream BashExecutionComponent: a `!cmd` or `!!cmd` with its streaming output between two
// dynamic borders, the spinner while it runs and its status after.
type BashExecutionBlock struct {
	invalidatable
	command            string
	excludeFromContext bool
	output             strings.Builder
	status             bashStatus
	exitCode           *int
	truncated          bool
	fullOutputPath     string
	expanded           bool
	// displayed reports that upstream's updateDisplay has run: from then on the header is drawn in "bashMode" even for
	// an excluded command, as upstream rebuilds it.
	displayed bool
	loader    *Loader // animated spinner while running
}

type bashStatus int

const (
	bashStatusRunning bashStatus = iota
	bashStatusComplete
	bashStatusCancelled
	bashStatusError
)

// IsDirty reports whether the block needs re-rendering. While running it
// embeds an animated Loader spinner advanced by the 100ms tick loop, but the
// tick ticks the loader without Invalidating this block. Reporting dirty
// while running keeps the per-child render cache from freezing the spinner.
func (b *BashExecutionBlock) IsDirty() bool {
	if b.invalidatable.IsDirty() {
		return true
	}
	return b.status == bashStatusRunning
}

// NewBashExecutionBlock returns a running block, as upstream's constructor: the spinner in the command's color and
// its message in "muted".
func NewBashExecutionBlock(command string, excludeFromContext bool) *BashExecutionBlock {
	colorKey := bashColorKey(excludeFromContext)
	loader := NewLoader("Running... (" + tuiKeyText(KBSelectCancel) + " to cancel)")
	loader.SpinnerColorFn = func(text string) string { return ActiveTheme().FgText(colorKey, text) }
	loader.MessageColorFn = func(text string) string { return ActiveTheme().FgText("muted", text) }
	return &BashExecutionBlock{
		command:            command,
		excludeFromContext: excludeFromContext,
		status:             bashStatusRunning,
		loader:             loader,
	}
}

// bashColorKey is upstream's colorKey: "dim" for a command excluded from the context, "bashMode" otherwise.
func bashColorKey(excludeFromContext bool) string {
	if excludeFromContext {
		return "dim"
	}
	return "bashMode"
}

// tuiKeyText is upstream keyText for a tui keybinding: its keys joined by "/".
func tuiKeyText(action TUIKeybinding) string {
	return FormatKeyText(strings.Join(GetTUIKeybindings().GetKeys(action), "/"), false)
}

// Loader returns the embedded spinner when the block is still running,
// nil otherwise. The animation driver ticks it to advance the spinner.
func (b *BashExecutionBlock) Loader() *Loader {
	if b.status != bashStatusRunning {
		return nil
	}
	return b.loader
}

// AppendOutput is upstream appendOutput: the chunk without ANSI escapes and with "\r\n" and "\r" as "\n" continues
// the last output line.
func (b *BashExecutionBlock) AppendOutput(chunk string) {
	b.output.WriteString(cleanBashOutput(chunk))
	b.displayed = true
	b.Invalidate()
}

func cleanBashOutput(chunk string) string {
	return strings.ReplaceAll(strings.ReplaceAll(widthx.StripAnsi(chunk), "\r\n", "\n"), "\r", "\n")
}

// SetComplete is upstream setComplete(exitCode, cancelled, truncated ? {truncated} : undefined): a cancelled command,
// one that exited non-zero (error), or a completed one.
func (b *BashExecutionBlock) SetComplete(exitCode *int, cancelled, truncated bool) {
	b.finishBashExecution(exitCode, cancelled, truncated, "")
}

// SetCompleteWithOutput replaces the streaming preview with the durable truncated output, cleaned as AppendOutput
// cleans a chunk, and records its full-output path before completing the block.
func (b *BashExecutionBlock) SetCompleteWithOutput(exitCode *int, cancelled, truncated bool, output, fullOutputPath string) {
	b.output.Reset()
	b.output.WriteString(cleanBashOutput(output))
	b.finishBashExecution(exitCode, cancelled, truncated, fullOutputPath)
}

// SetCompleteFullOutput is upstream setComplete with a full-output path, keeping the output the block shows.
func (b *BashExecutionBlock) SetCompleteFullOutput(exitCode *int, cancelled, truncated bool, fullOutputPath string) {
	b.finishBashExecution(exitCode, cancelled, truncated, fullOutputPath)
}

func (b *BashExecutionBlock) finishBashExecution(exitCode *int, cancelled, truncated bool, fullOutputPath string) {
	switch {
	case cancelled:
		b.status = bashStatusCancelled
	case exitCode != nil && *exitCode != 0:
		b.status = bashStatusError
	default:
		b.status = bashStatusComplete
	}
	b.exitCode = exitCode
	b.truncated = truncated
	b.fullOutputPath = fullOutputPath
	b.displayed = true
	b.Invalidate()
}

// SetExpanded is upstream setExpanded: the whole output, or a preview of its last previewLines visual rows.
func (b *BashExecutionBlock) SetExpanded(expanded bool) {
	b.expanded = expanded
	b.displayed = true
	b.Invalidate()
}

// Render draws upstream's children: a spacer, the top border, the header, the output, the spinner or the status, and
// the bottom border.
func (b *BashExecutionBlock) Render(width int) []string {
	theme := ActiveTheme()
	colorKey := bashColorKey(b.excludeFromContext)
	border := NewDynamicBorderFunc(func(text string) string { return theme.FgText(colorKey, text) }).Render(width)
	out := make([]string, 0, 8)
	out = append(out, "")
	out = append(out, border...)

	headerKey := colorKey
	if b.displayed {
		headerKey = "bashMode"
	}
	out = append(out, NewPaddedText(theme.FgText(headerKey, "\x1b[1m$ "+b.command+SGRBoldDimReset), 1, 0, nil).Render(width)...)

	if !b.displayed {
		out = append(out, b.loader.Render(width)...)
		return append(out, border...)
	}

	content, contextTruncated := b.output.String(), false
	if bashContextTruncation != nil {
		content, contextTruncated = bashContextTruncation(content)
	}
	var available []string
	if content != "" {
		available = strings.Split(content, "\n")
	}
	preview := available[max(0, len(available)-previewLines):]
	hidden := len(available) - len(preview)
	if len(available) > 0 {
		if b.expanded {
			out = append(out, NewPaddedText("\n"+mutedLines(theme, available), 1, 0, nil).Render(width)...)
		} else {
			out = append(out, TruncateToVisualLines("\n"+mutedLines(theme, preview), previewLines, width, 1).VisualLines...)
		}
	}

	if b.status == bashStatusRunning {
		out = append(out, b.loader.Render(width)...)
	} else if parts := b.statusParts(theme, hidden, contextTruncated); len(parts) > 0 {
		out = append(out, NewPaddedText("\n"+strings.Join(parts, "\n"), 1, 0, nil).Render(width)...)
	}
	return append(out, border...)
}

func mutedLines(theme *Theme, lines []string) string {
	styled := make([]string, len(lines))
	for i, line := range lines {
		styled[i] = theme.FgText("muted", line)
	}
	return strings.Join(styled, "\n")
}

// statusParts are upstream's status rows in order: the collapse hint, the cancelled or exit status, and the
// truncation warning.
func (b *BashExecutionBlock) statusParts(theme *Theme, hidden int, contextTruncated bool) []string {
	var parts []string
	if hidden > 0 {
		key := AppKeyText("app.tools.expand", "ctrl+o")
		if b.expanded {
			parts = append(parts, theme.FgText("muted", "(")+KeyHint(key, "to collapse")+theme.FgText("muted", ")"))
		} else {
			parts = append(parts, theme.FgText("muted", "... "+strconv.Itoa(hidden)+" more lines (")+KeyHint(key, "to expand")+theme.FgText("muted", ")"))
		}
	}
	switch b.status {
	case bashStatusCancelled:
		parts = append(parts, theme.FgText("warning", "(cancelled)"))
	case bashStatusError:
		parts = append(parts, theme.FgText("error", "(exit "+strconv.Itoa(*b.exitCode)+")"))
	}
	if (b.truncated || contextTruncated) && b.fullOutputPath != "" {
		parts = append(parts, theme.FgText("warning", "Output truncated. Full output: "+b.fullOutputPath))
	}
	return parts
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
