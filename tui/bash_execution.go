package tui

// BashExecutionBlock renders `!cmd` and `!!cmd` output between dynamic borders. Output uses the shared width-aware text layout. Collapsed blocks retain the final previewLines visual rows; expanded blocks retain every available row. The excludeFromContext flag changes only context inclusion and header presentation.

import (
	"fmt"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// previewLines is the collapsed visual-row limit.
const previewLines = 20

// BashExecutionBlock displays a `!cmd` invocation with streaming
// output, top/bottom borders, and expand/collapse support.
type BashExecutionBlock struct {
	invalidatable
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
func (b *BashExecutionBlock) IsDirty() bool {
	if b.invalidatable.IsDirty() {
		return true
	}
	return b.status == bashStatusRunning
}

// NewBashExecutionBlock returns a freshly-constructed bash-execution
// component in the running state.
func NewBashExecutionBlock(command string, excludeFromContext bool) *BashExecutionBlock {
	// Upstream uses Loader with themed spinner/message colors.
	colorKey := bashHeaderColor()
	if excludeFromContext {
		colorKey = bashDimColor()
	}
	return &BashExecutionBlock{
		command:            command,
		excludeFromContext: excludeFromContext,
		status:             bashStatusRunning,
		startedAt:          time.Now(),
		loader: NewStyledLoader(
			colorKey, bashMutedColor(),
			"Running... (Esc to cancel)", nil,
		),
	}
}

// Loader returns the embedded spinner when the block is still running,
// nil otherwise. The animation driver ticks it to advance the spinner.
func (b *BashExecutionBlock) Loader() *Loader {
	if b.status != bashStatusRunning {
		return nil
	}
	return b.loader
}

// AppendOutput streams a sanitized chunk into the block.
func (b *BashExecutionBlock) AppendOutput(chunk string) {
	b.output.WriteString(chunk)
	b.Invalidate()
}

// SetComplete transitions the block out of the running state.
func (b *BashExecutionBlock) SetComplete(exitCode *int, cancelled, truncated bool) {
	b.finishBashExecution(exitCode, cancelled, truncated, "")
}

// SetCompleteWithOutput replaces the streaming preview with the durable truncated output and records its full-output path before completing the block.
func (b *BashExecutionBlock) SetCompleteWithOutput(exitCode *int, cancelled, truncated bool, output, fullOutputPath string) {
	b.output.Reset()
	b.output.WriteString(output)
	b.finishBashExecution(exitCode, cancelled, truncated, fullOutputPath)
}

func (b *BashExecutionBlock) finishBashExecution(exitCode *int, cancelled, truncated bool, fullOutputPath string) {
	if cancelled {
		b.status = bashStatusCancelled
	} else {
		b.status = bashStatusComplete
	}
	b.exitCode = exitCode
	b.truncated = truncated
	b.fullOutputPath = fullOutputPath
	b.finishedAt = time.Now()
	b.Invalidate()
}

// SetExpanded forces the body open (true) or collapsed-to-preview
// (false). Driven by the global Ctrl+O toggle so all bash blocks land
// in the same state as their tool-execution peers.
func (b *BashExecutionBlock) SetExpanded(expanded bool) {
	b.expanded = expanded
	b.Invalidate()
}

// Render produces the block's lines. Output wraps to the current terminal width, and collapsed previews retain the last previewLines visual rows.
func (b *BashExecutionBlock) Render(width int) []string {
	if width < 1 {
		width = 1
	}
	colorKey := bashHeaderColor()
	if b.excludeFromContext {
		colorKey = bashDimColor()
	}
	bold := "\033[1m"
	reset := "\033[0m"
	muted := bashMutedColor()

	// Border line: full-width box-drawing horizontal rule, colored.
	border := colorKey + strings.Repeat("\u2500", width) + reset

	out := make([]string, 0, 8)

	// Leading spacer (upstream Spacer(1) before contentBox).
	out = append(out, "")

	// Top border.
	out = append(out, border)

	// Header: " $ command" with 1-col indent matching upstream
	// Text(text, 1, 0) (1-col left padding).
	header := fmt.Sprintf(" %s%s$ %s%s", colorKey, bold, b.command, reset)
	out = append(out, header)

	// Body: output split on \n. Preserve trailing newline so the
	// final empty line is rendered as a blank row inside the box -
	// matches upstream `bash-execution.ts:140-160` where output is
	// rendered via `new Text(\`\n${displayText}\`, 1, 0)` and the
	// `availableLines` from splitting `"...\n".split("\n")` contains
	// a trailing empty string.
	bodyRaw := b.output.String()
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
			styled[i] = muted + line + SGRFgReset
		}
		text := "\n" + strings.Join(styled, "\n")
		var rendered []string
		if b.expanded {
			rendered = NewPaddedText(text, 1, 0, nil).Render(width)
		} else {
			rendered = TruncateToVisualLines(text, previewLines, width, 1).VisualLines
		}
		for _, line := range rendered {
			if strings.TrimSpace(widthx.StripAnsi(line)) == "" {
				out = append(out, "")
				continue
			}
			out = append(out, strings.TrimRight(line, " "))
		}
	}

	// Loader or status block.
	if b.status == bashStatusRunning {
		// Upstream adds the Loader as a child, which renders the animated
		// spinner. Mirrors bash-execution.ts:63 (contentContainer.addChild(this.loader)).
		out = append(out, b.loader.Render(width)...)
	} else {
		// Status block. Upstream emits these inside `Text("\n" + parts.join("\n"), 1, 0)`
		// so the block is preceded by a blank row and each part is on its
		// own row. When complete && exitCode==0 && nothing truncated &&
		// nothing hidden, statusLines is empty and NOTHING is appended.
		statusLines := b.statusLines(hidden)
		if len(statusLines) > 0 {
			out = append(out, "")
			for _, ln := range statusLines {
				out = append(out, " "+ln)
			}
		}
	}

	// Bottom border.
	out = append(out, border)

	// Trailing blank (matches existing 2.19a chat spacing).
	out = append(out, "")
	return out
}

// statusLines returns the collapse, completion, and durable-truncation status rows in display order. Successful untruncated output with no hidden logical lines has no status row.
func (b *BashExecutionBlock) statusLines(hidden int) []string {
	dim := "\033[2m"
	red := "\033[31m"
	yellow := "\033[33m"
	reset := "\033[0m"

	lines := make([]string, 0, 3)

	// Collapse hint.
	if hidden > 0 {
		if b.expanded {
			lines = append(lines, dim+"(ctrl+o to collapse)"+reset)
		} else {
			lines = append(lines, fmt.Sprintf("%s... %d more lines%s (ctrl+o to expand)", dim, hidden, reset))
		}
	}

	switch b.status {
	case bashStatusCancelled:
		// Upstream: theme.fg("warning", "(cancelled)"): no duration.
		lines = append(lines, yellow+"(cancelled)"+reset)
	case bashStatusComplete:
		// Upstream only renders (exit N) for NON-zero exit codes
		// (the "error" branch). Success renders nothing.
		if b.exitCode != nil && *b.exitCode != 0 {
			lines = append(lines, fmt.Sprintf("%s(exit %d)%s", red, *b.exitCode, reset))
		}
		if b.truncated && b.fullOutputPath != "" {
			lines = append(lines, yellow+"Output truncated. Full output: "+b.fullOutputPath+reset)
		}
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
func bashHeaderColor() string { return ThemeHexFg("#b5bd68") } // bashMode
func bashDimColor() string    { return ThemeHexFg("#666666") } // dim
func bashMutedColor() string  { return ThemeHexFg("#808080") } // muted
