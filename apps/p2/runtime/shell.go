package runtime

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Ports apps/p2/src/runtime/shell.ts: user shell commands (`!cmd`, `!!cmd`) as Durable entries. A finished command is
// placed as a `p2.shell` entry through a write submission: it never starts a run. With `includeInContext` (`!`) the
// entry also carries a user model message, pi's `bashExecutionToText`, so the agent sees it on its next run; `!!`
// entries are display-only. The data never has a `text` field, so readers never take it for a person's message.

// ShellEntry is the entry kind of a finished user shell command (`SHELL_ENTRY`).
const ShellEntry = "p2.shell"

// The output a command keeps: the tail within the bash tool's limits (Durable's DEFAULT_MAX_LINES/BYTES).
const (
	ShellMaxLines = 2000
	ShellMaxBytes = 50 * 1024
)

// ShellData is the data of a `p2.shell` entry (`ShellDataSchema`).
type ShellData struct {
	Author Author
	// Command is the command line.
	Command string
	// Output is the retained tail of stdout and stderr, as they arrived.
	Output string
	// ExitCode is nil when the command did not end by itself (cancelled, failed to start, timed out).
	ExitCode  *float64
	Cancelled bool
	// Truncated says earlier output was dropped from Output.
	Truncated bool
	// FullOutputPath is where the environment kept the complete output, when it spilled (a path on the machine the
	// command ran on).
	FullOutputPath *string
	// IncludeInContext is `!` (true) or `!!` (false).
	IncludeInContext bool
	// Error is why it did not run or finish, e.g. a start failure.
	Error *string
}

// ShellContextText is pi's `bashExecutionToText` (coding-agent core/messages.ts): what the model reads for an included
// command.
func ShellContextText(data ShellData) string {
	var text strings.Builder
	fmt.Fprintf(&text, "Ran `%s`\n", data.Command)
	if data.Output != "" {
		fmt.Fprintf(&text, "```\n%s\n```", data.Output)
	} else {
		text.WriteString("(no output)")
	}
	switch {
	case data.Cancelled:
		text.WriteString("\n\n(command cancelled)")
	case data.Error != nil && *data.Error != "":
		text.WriteString("\n\n" + *data.Error)
	case data.ExitCode != nil && *data.ExitCode != 0:
		fmt.Fprintf(&text, "\n\nCommand exited with code %s", jsNumber(*data.ExitCode))
	}
	if data.Truncated && data.FullOutputPath != nil && *data.FullOutputPath != "" {
		fmt.Fprintf(&text, "\n\n[Output truncated. Full output: %s]", *data.FullOutputPath)
	}
	return text.String()
}

// ShellTail returns the tail of text within the limits: whole lines from the end, at most maxLines lines and maxBytes
// UTF-8 bytes (a single longer line keeps its end). truncated says whether anything was dropped.
//
// Go forced difference: a single line longer than maxBytes keeps its end cut on a character boundary, where upstream
// cuts between UTF-16 units and may leave half a surrogate pair at the start.
func ShellTail(text string, maxLines, maxBytes int) (tail string, truncated bool) {
	lines := strings.Split(text, "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
		truncated = true
	}
	kept := strings.Join(lines, "\n")
	if len(kept) > maxBytes {
		truncated = true
		bytes := 0
		start := len(lines)
		for start > 0 {
			size := len(lines[start-1])
			if start < len(lines) {
				size++
			}
			if bytes+size > maxBytes {
				break
			}
			bytes += size
			start--
		}
		kept = strings.Join(lines[start:], "\n")
		// One line longer than the limit: its end.
		if start == len(lines) {
			last := lines[len(lines)-1]
			cut := max(0, len(last)-maxBytes)
			for cut < len(last) && !utf8.RuneStart(last[cut]) {
				cut++
			}
			kept = last[cut:]
		}
	}
	return kept, truncated
}

// ShellTailDefault is ShellTail with the bash tool's limits (the defaults of `shellTail`).
func ShellTailDefault(text string) (string, bool) {
	return ShellTail(text, ShellMaxLines, ShellMaxBytes)
}
