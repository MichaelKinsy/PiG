package tui_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// typedAutocompleteProvider is the provider testdata/editor_typed_autocomplete.mjs defines for Pi: command names after "/", the argument completions of "/load-skills ", and the tokens that follow an @, # or $ (a trigger character) at a token boundary.
type typedAutocompleteProvider struct{}

// RE2's \s and \S are ASCII only; the oracle's are JavaScript's, so the provider spells JavaScript's white space out (the corpus types U+3000).
const (
	typedAutocompleteSpace    = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`
	typedAutocompleteNotSpace = `[^` + typedAutocompleteSpace + `]*`
)

var (
	typedAutocompleteCommandRe  = regexp.MustCompile(`^/(` + typedAutocompleteNotSpace + `)$`)
	typedAutocompleteArgumentRe = regexp.MustCompile(`^/load-skills (` + typedAutocompleteNotSpace + `)$`)
	typedAutocompleteTokenRe    = regexp.MustCompile("(?:^|[" + typedAutocompleteSpace + "([{<`])([@#$])(" + typedAutocompleteNotSpace + ")$")
	typedAutocompleteCommands   = []string{"load-skills", "list", "help"}
	typedAutocompleteSkills     = []string{"skill-a", "skill-b", "éskill-a"}
	typedAutocompleteTokens     = []string{"alpha", "alpine", "beta"}
)

func typedAutocompleteMatching(names []string, prefix string) []tui.AutocompleteItem {
	var items []tui.AutocompleteItem
	for _, name := range names {
		if strings.HasPrefix(name, prefix) {
			items = append(items, tui.AutocompleteItem{Value: name, Label: name})
		}
	}
	return items
}

func (typedAutocompleteProvider) TriggerCharacters() []string { return []string{"$"} }

func (typedAutocompleteProvider) GetSuggestions(_ context.Context, lines []string, cursorLine, cursorCol int, _ tui.AutocompleteSuggestionOptions) *tui.AutocompleteSuggestions {
	before := lines[cursorLine][:cursorCol]
	if match := typedAutocompleteCommandRe.FindStringSubmatch(before); match != nil {
		if items := typedAutocompleteMatching(typedAutocompleteCommands, match[1]); len(items) > 0 {
			return &tui.AutocompleteSuggestions{Items: items, Prefix: before}
		}
		return nil
	}
	if match := typedAutocompleteArgumentRe.FindStringSubmatch(before); match != nil {
		if items := typedAutocompleteMatching(typedAutocompleteSkills, match[1]); len(items) > 0 {
			return &tui.AutocompleteSuggestions{Items: items, Prefix: match[1]}
		}
		return nil
	}
	if match := typedAutocompleteTokenRe.FindStringSubmatch(before); match != nil {
		if items := typedAutocompleteMatching(typedAutocompleteTokens, match[2]); len(items) > 0 {
			return &tui.AutocompleteSuggestions{Items: items, Prefix: match[1] + match[2]}
		}
	}
	return nil
}

func (typedAutocompleteProvider) ApplyCompletion(lines []string, cursorLine, cursorCol int, item tui.AutocompleteItem, prefix string) ([]string, int, int) {
	line := lines[cursorLine]
	start := max(0, cursorCol-len(prefix))
	out := append([]string{}, lines...)
	out[cursorLine] = line[:start] + item.Value + line[cursorCol:]
	return out, cursorLine, start + len(item.Value)
}

func typedAutocompleteProviderFor(*testing.T) tui.AutocompleteProvider {
	return typedAutocompleteProvider{}
}

// typedAutocompleteProbes types each character in each context with the popup closed, then each of a few second characters, and types every short sequence of characters into an empty editor.
func typedAutocompleteProbes() []awaitedAcceptProbe {
	typed := []string{" ", "é", "日", "a", "1", ".", "-", "_", "/", "@", "#", "$", "!", "(", "，", `"`, "\u3000", "ab", "é!", "!a"}
	setups := []string{
		"", "/", "/l", "/load-skills", "/load-skills ", "/load-skills s", "/load-skills é", "/list", " /load-skills", "  ", "hi", "hi ", "hi @", "hi @a", "@", "(@",
		"$", "x$", "x$ ", "/load-skills @", "/load-skills $a", "/load-skills #", "a\n/load-skills", "a\n@", "a\n$", "@\"a b", "日本 @", "x#", "@alpha", "/load-skills é ",
	}
	var probes []awaitedAcceptProbe
	keys := func(text ...string) []awaitedAcceptOp {
		ops := make([]awaitedAcceptOp, len(text))
		for i := range text {
			ops[i] = awaitedAcceptOp{Keys: &text[i]}
		}
		return ops
	}
	for _, setup := range setups {
		for _, first := range typed {
			probes = append(probes, awaitedAcceptProbe{Ops: append([]awaitedAcceptOp{{SetText: new(setup)}}, keys(first)...)})
		}
	}
	for _, setup := range []string{"/load-skills", "/load-skills ", "hi @", "$"} {
		for _, first := range typed {
			for _, second := range []string{" ", "é", "a", "/", "@", "$", ".", "日"} {
				probes = append(probes, awaitedAcceptProbe{Ops: append([]awaitedAcceptOp{{SetText: new(setup)}}, keys(first, second)...)})
			}
		}
	}
	alphabet := []string{"/", "l", " ", "é", "@", "a", "$", "日"}
	sequences := [][]string{nil}
	for length := range 3 {
		for _, sequence := range sequences[len(sequences)-pow(len(alphabet), length):] {
			for _, next := range alphabet {
				sequences = append(sequences, append(append([]string{}, sequence...), next))
			}
		}
	}
	for _, sequence := range sequences[1:] {
		probes = append(probes, awaitedAcceptProbe{Ops: keys(sequence...)})
	}
	return probes
}

func pow(base, exponent int) int {
	result := 1
	for range exponent {
		result *= base
	}
	return result
}

// TestEditorTypedCharactersOpenAClosedPopupLikePi types characters into an editor whose popup is closed. Pi's insertCharacter opens a closed popup only for "/" at the start of the message, for a trigger character at a token boundary, and for a letter, digit, ".", "-", "_" or CJK character in a slash command or trigger context; a space or "é" typed after "/load-skills" opens none, although the command's argument popup would match.
func TestEditorTypedCharactersOpenAClosedPopupLikePi(t *testing.T) {
	compareAwaitedAcceptProbesWithPi(t, "editor_typed_autocomplete.mjs", typedAutocompleteProbes(), typedAutocompleteProviderFor)
}
