package runtime

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/schema"
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

// ShellDataSchema is the schema of a `p2.shell` entry's data.
var ShellDataSchema = schema.Obj(schema.Properties{
	schema.Prop("author", AuthorSchema),
	schema.Prop("command", schema.Str()),
	schema.Prop("output", schema.Str()),
	schema.Opt("exitCode", schema.Num()),
	schema.Prop("cancelled", schema.Bool()),
	schema.Prop("truncated", schema.Bool()),
	schema.Opt("fullOutputPath", schema.Str()),
	schema.Prop("includeInContext", schema.Bool()),
	schema.Opt("error", schema.Str()),
}, schema.Open())

var isShellData = check(ShellDataSchema)

// ShellDataOf returns the shell data an entry of kind `p2.shell` records, if data is one (`shellData`). data is a decoded JSON value
// (objects as *delta.JsonObject).
func ShellDataOf(data any) (ShellData, bool) {
	if !isShellData(data) {
		return ShellData{}, false
	}
	value, author := object(data), object(object(data).Value("author"))
	result := ShellData{
		Author:           Author{Kind: AuthorKind(text(author, "kind")), ID: text(author, "id"), Name: text(author, "name")},
		Command:          text(value, "command"),
		Output:           text(value, "output"),
		Cancelled:        value.Value("cancelled") == true,
		Truncated:        value.Value("truncated") == true,
		IncludeInContext: value.Value("includeInContext") == true,
	}
	if code, ok := value.Value("exitCode").(float64); ok {
		result.ExitCode = &code
	}
	if path, ok := value.Value("fullOutputPath").(string); ok {
		result.FullOutputPath = &path
	}
	if message, ok := value.Value("error").(string); ok {
		result.Error = &message
	}
	return result, true
}
