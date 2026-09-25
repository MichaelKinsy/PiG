package tui

// Slash-command autocomplete suggestions.
//
// Mirrors the slash-command slice of upstream
// `.upstream/current/packages/tui/src/autocomplete.ts` (783 LOC).
// The full combined provider (slash + `@<file>` + force-on-Tab naked
// path completion) lives in `file_autocomplete.go`.
//
// Single-source-of-truth notes:
//   - The slash registry comes from `internal/codingagent/slash_commands.go::BuiltinSlashCommands()`.
//     Editor wiring imports it via the host (interactive.go) and converts
//     to `[]tui.SlashCommand` so the tui package stays free of agent deps.
//   - Argument completion for `/model` is attached at the host level
//     (interactive.go) using `ReachableProviders()` ∩ `AuthenticatedProviders()`,
//
// mirroring the picker's default `scoped` view.

import (
	"strings"
)

// AutocompleteItem is one popup row.
type AutocompleteItem struct {
	Value       string // text inserted at cursor on accept
	Label       string // displayed primary text
	Description string // optional secondary text (dim)
}

// AutocompleteSuggestions is the provider return shape. Prefix is the
// buffer slice the popup is matching against: applyCompletion uses
// its length to know how many chars to replace.
type AutocompleteSuggestions struct {
	Items  []AutocompleteItem
	Prefix string
}

// SlashCommand is the autocomplete-side view of a registered slash
// command. Matches upstream's `SlashCommand` interface in
// autocomplete.ts. GetArgumentCompletions is optional; nil means
// the command takes no completable arguments.
type SlashCommand struct {
	Name                   string
	Description            string
	ArgumentHint           string
	GetArgumentCompletions func(argPrefix string) []AutocompleteItem
}

// AutocompleteProvider is the editor-facing surface. Synchronous -
// upstream's async/AbortController machinery is deferred until
// extension-supplied providers need it (out of scope for 2.6a).
type AutocompleteProvider interface {
	// GetSuggestions returns suggestions for the given buffer state.
	// Return nil when no popup should be shown (no match, wrong context).
	GetSuggestions(lines []string, cursorLine, cursorCol int) *AutocompleteSuggestions

	// ApplyCompletion edits the buffer to insert `item.Value` in place
	// of the trailing `prefix`. Returns the new buffer + cursor.
	ApplyCompletion(lines []string, cursorLine, cursorCol int, item AutocompleteItem, prefix string) (newLines []string, newLine, newCol int)
}

// ForcefulAutocompleteProvider is an optional extension implemented by
// providers that support "force" file-completion, triggered by Tab when
// the popup is closed and the buffer is not in a slash-command-name
// context. Mirrors upstream `AutocompleteProvider.getSuggestions`
// `{force: true}` branch (autocomplete.ts:281).
type ForcefulAutocompleteProvider interface {
	GetSuggestionsForce(lines []string, cursorLine, cursorCol int) *AutocompleteSuggestions
}

// SlashOnlyProvider serves builtin slash commands and (when the buffer
// is `/<cmd> <args>`) delegates argument completion to the matched
// command's GetArgumentCompletions callback. No `@<file>`, no path
// completion, no async machinery: just slash.
//
// Naming note: previously called "StaticProvider" during the port.
// Renamed to SlashOnlyProvider because the per-command
// GetArgumentCompletions callback makes it non-static (e.g. `/model`
// computes a fresh model list each invocation). The defining trait is
// "slash only, no @/path", which the name reflects.
type SlashOnlyProvider struct {
	Commands []SlashCommand
}

// NewSlashOnlyProvider constructs a provider over the given command list.
func NewSlashOnlyProvider(cmds []SlashCommand) *SlashOnlyProvider {
	return &SlashOnlyProvider{Commands: cmds}
}

// GetSuggestions implements AutocompleteProvider. Three cases:
//
//  1. Buffer doesn't start with `/` (after trim of leading line slice
//     up to cursor) → return nil (popup off).
//  2. `/<partial>` (no space) → fuzzy-filter command names.
//  3. `/<cmd> <argPrefix>` (space present) → delegate to the matched
//     command's GetArgumentCompletions.
//
// Mirrors `CombinedAutocompleteProvider.getSuggestions` slash branch
// (autocomplete.ts:262-342): minus the `@`/path branches.
func (p *SlashOnlyProvider) GetSuggestions(lines []string, cursorLine, cursorCol int) *AutocompleteSuggestions {
	if cursorLine < 0 || cursorLine >= len(lines) {
		return nil
	}
	line := lines[cursorLine]
	if cursorCol > len(line) {
		cursorCol = len(line)
	}
	before := line[:cursorCol]

	// Slash-command path requires the buffer (the *entire* current
	// logical line up to cursor) to start with `/`. Multi-line is
	// allowed only on the first line.
	if !strings.HasPrefix(before, "/") {
		return nil
	}
	if cursorLine != 0 {
		return nil
	}

	spaceIdx := strings.IndexAny(before, " \t")
	if spaceIdx == -1 {
		// `/partial`: name completion.
		prefix := before[1:] // strip leading "/"
		type item struct {
			cmd  SlashCommand
			text string
		}
		all := make([]item, 0, len(p.Commands))
		for _, c := range p.Commands {
			all = append(all, item{cmd: c, text: c.Name})
		}
		// Upstream autocomplete.ts matches skill commands by their bare name
		// unless the query itself names the skill: namespace.
		filtered := FuzzyFilter(all, prefix, func(it item) string {
			if !strings.HasPrefix(prefix, "skill:") && strings.HasPrefix(it.text, "skill:") {
				return strings.TrimPrefix(it.text, "skill:")
			}
			return it.text
		})
		if len(filtered) == 0 {
			return nil
		}
		out := make([]AutocompleteItem, 0, len(filtered))
		for _, f := range filtered {
			desc := f.cmd.Description
			if f.cmd.ArgumentHint != "" {
				if desc == "" {
					desc = f.cmd.ArgumentHint
				} else {
					desc = f.cmd.ArgumentHint + " — " + desc
				}
			}
			out = append(out, AutocompleteItem{
				Value:       f.cmd.Name,
				Label:       f.cmd.Name,
				Description: desc,
			})
		}
		return &AutocompleteSuggestions{Items: out, Prefix: before}
	}

	// `/<cmd> <args>`: argument completion path.
	name := before[1:spaceIdx]
	argText := before[spaceIdx+1:]
	for _, c := range p.Commands {
		if c.Name != name {
			continue
		}
		if c.GetArgumentCompletions == nil {
			return nil
		}
		items := c.GetArgumentCompletions(argText)
		if len(items) == 0 {
			return nil
		}
		return &AutocompleteSuggestions{Items: items, Prefix: argText}
	}
	return nil
}

// ApplyCompletion replaces the trailing `prefix` chars of the current
// line with the completion text. Three behaviors:
//
//   - Slash-name completion (prefix starts with `/`, no space, at line
//     start): inserts `/<value> ` (note trailing space). Cursor lands
//     after the space. Mirrors autocomplete.ts:368-385.
//   - Argument completion (prefix is the arg text, line contains
//     `/cmd `): inserts `value` at cursor. No trailing space.
//   - Other (defensive fallthrough): same as argument completion.
func (p *SlashOnlyProvider) ApplyCompletion(lines []string, cursorLine, cursorCol int, item AutocompleteItem, prefix string) ([]string, int, int) {
	if cursorLine < 0 || cursorLine >= len(lines) {
		return lines, cursorLine, cursorCol
	}
	line := lines[cursorLine]
	if cursorCol > len(line) {
		cursorCol = len(line)
	}
	if len(prefix) > cursorCol {
		// Defensive: prefix longer than what's before cursor. Treat as no-op.
		return lines, cursorLine, cursorCol
	}
	beforePrefix := line[:cursorCol-len(prefix)]
	afterCursor := line[cursorCol:]

	out := make([]string, len(lines))
	copy(out, lines)

	// Slash-name completion: `prefix` starts with `/` and contains no
	// further `/` (i.e. it's `/foo` not `/foo/bar`), and the text
	// before the prefix is empty/whitespace (line start).
	isSlashName := strings.HasPrefix(prefix, "/") &&
		!strings.Contains(prefix[1:], "/") &&
		strings.TrimSpace(beforePrefix) == ""
	if isSlashName {
		newLine := beforePrefix + "/" + item.Value + " " + afterCursor
		out[cursorLine] = newLine
		return out, cursorLine, len(beforePrefix) + len(item.Value) + 2 // "/" + value + " "
	}

	// Argument completion: replace prefix with value verbatim.
	newLine := beforePrefix + item.Value + afterCursor
	out[cursorLine] = newLine
	return out, cursorLine, len(beforePrefix) + len(item.Value)
}
