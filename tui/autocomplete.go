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
	"context"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/tui/widthx"
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
	// AwaitArgumentCompletions is the Promise-returning form. The editor invokes it on its owned query worker, not while reading/rendering input.
	AwaitArgumentCompletions func(argPrefix string) ([]AutocompleteItem, error)
}

// AutocompleteProvider supplies the local synchronous portion of a query. Deferred callbacks and filesystem searches are captured by AsyncSuggestionPlanner and AsyncFileSearcher; extension chains use AsyncAutocompleteProvider.
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

// SlashOnlyProvider serves command names and their immediate or awaited argument completions. It does not perform path or attachment completion.
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
	// The slash-command path applies when the current line up to the cursor starts with `/` after leading whitespace (autocomplete.ts:338-339, #10218).
	before := widthx.JSTrimStart(line[:cursorCol])
	if !strings.HasPrefix(before, "/") {
		return nil
	}

	if !strings.Contains(before, " ") {
		// `/partial`: name completion.
		prefix := before[1:] // strip leading "/"
		type item struct {
			cmd  SlashCommand
			text string
			idx  int
		}
		all := make([]item, 0, len(p.Commands))
		for i, c := range p.Commands {
			all = append(all, item{cmd: c, text: c.Name, idx: i})
		}
		// Upstream autocomplete.ts matches skill commands by their bare name
		// first, then by their full skill: name, so `/skill` lists skills whose
		// names lack its letters and `/skbra` still finds skill:brainstorm.
		bareNameMatches := FuzzyFilter(all, prefix, func(it item) string {
			return strings.TrimPrefix(it.text, "skill:")
		})
		bareNameMatched := make(map[int]bool, len(bareNameMatches))
		for _, match := range bareNameMatches {
			bareNameMatched[match.idx] = true
		}
		var fullNameCandidates []item
		for _, it := range all {
			if strings.HasPrefix(it.text, "skill:") && !bareNameMatched[it.idx] {
				fullNameCandidates = append(fullNameCandidates, it)
			}
		}
		fullNameOnlyMatches := FuzzyFilter(fullNameCandidates, prefix, func(it item) string { return it.text })
		filtered := slices.Concat(bareNameMatches, fullNameOnlyMatches)
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
	name, argText, _ := slashArgumentPrefix(before)
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

// slashArgumentPrefix splits `/<cmd> <args>` after leading whitespace into the command name and argument text.
func slashArgumentPrefix(before string) (string, string, bool) {
	before = widthx.JSTrimStart(before)
	if !strings.HasPrefix(before, "/") {
		return "", "", false
	}
	space := strings.IndexByte(before, ' ')
	if space < 0 {
		return "", "", false
	}
	return before[1:space], before[space+1:], true
}

// SuggestionTask captures a Promise-returning command callback without invoking it on the input loop.
func (p *SlashOnlyProvider) SuggestionTask(lines []string, line, col int, _ bool) (string, func(context.Context) ([]AutocompleteItem, error), bool) {
	if line < 0 || line >= len(lines) || col < 0 {
		return "", nil, false
	}
	name, prefix, ok := slashArgumentPrefix(lines[line][:min(col, len(lines[line]))])
	if !ok {
		return "", nil, false
	}
	for _, command := range p.Commands {
		if command.Name == name && command.AwaitArgumentCompletions != nil {
			return prefix, func(ctx context.Context) ([]AutocompleteItem, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				return command.AwaitArgumentCompletions(prefix)
			}, true
		}
	}
	return "", nil, false
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
		widthx.JSTrim(beforePrefix) == ""
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
