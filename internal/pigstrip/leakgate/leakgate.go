// Package leakgate holds the tables of the strip leak gates: the user-visible names of every strip ID, which the
// interactive gate (internal/codingagent TestStripLeakGate) and the CLI gate (cmd/pig TestStripLeakGate) look for on
// the surfaces a stripped built-in must leave. Only tests import it.
//
// pig additive (D92).
package leakgate

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// CommandPhrases are the /hotkeys rows and startup hints of the key actions a command owns. A stripped command's keys
// do nothing, so its rows and hints go too.
var CommandPhrases = map[string][]string{
	"/model":    {"Open model selector", "Cycle models", "to select model", "to cycle models"},
	"/thinking": {"Cycle thinking level", "to cycle thinking level"},
	"/copy":     {"Copy selection or last assistant message"},
	"/tree":     {"Tree filter mode"},
}

// FeatureNames are the user-visible names of each feature. Every feature ID must have an entry, so a new feature
// fails the gates until its names are listed; a feature no surface names has an empty list.
var FeatureNames = map[string][]string{
	pigstrip.Themes:             {`\bthemes\b`, `--theme\b`, `--no-themes`},
	pigstrip.Skills:             {`(?i)\bskills?\b`},
	pigstrip.PromptTemplates:    {`(?i)\bprompt[- ]templates?\b`},
	pigstrip.ExperimentalServer: {},
	pigstrip.NodeExtensions:     {},
	pigstrip.ExtensionSDKGo:     {`extension init.*\bGo\b`},
	pigstrip.ExtensionSDKRust:   {`extension init.*\bRust\b`},
	pigstrip.ExtensionSDKPython: {`extension init.*\bPython\b`},
	pigstrip.SyntaxHighlight:    {},
	pigstrip.WordDictionaries:   {},
	pigstrip.ExportHTML:         {`--export\b`},
	pigstrip.SelfUpdate:         {`\bupdate self\b`, `\|self\]`},
	pigstrip.Changelog:          {`(?i)\bchangelog\b`},
	pigstrip.Docs:               {`\bpig docs\b`, `\bdocs[/\\]`, `\bits docs\b`, `PiG documentation`},
	pigstrip.Mermaid:            {`(?i)\bmermaid\b`},
	pigstrip.PigletBuilder:      {`\bpiglet build\b`, `\bpiglet publish\b`, `\bbuild Piglets\b`},
}

// ExtensionNames are names a built-in extension shows besides its own: its commands and providers.
var ExtensionNames = map[string][]string{
	"pig-login": {`/sprite\b`, `(?i)\bpick a sprite\b`},
	"llama.cpp": {`/llama\b`},
	"mcp":       {`(?i)\bmcp\b`},
}

// Bounded matches name as a whole token: not inside a longer identifier, path segment or hyphenated word.
func Bounded(name string) string {
	return `(^|[^\w-])` + regexp.QuoteMeta(name) + `($|[^\w-])`
}

// Names returns the patterns of id's user-visible names for list. It fails for a list or feature the tables do not
// map, so a new one fails the gates until its names are listed.
func Names(list, id string) ([]*regexp.Regexp, error) {
	var names []string
	switch list {
	case pigstrip.ListTools:
		names = []string{Bounded(id)}
	case pigstrip.ListCommands:
		names = []string{Bounded(id)}
		for _, phrase := range CommandPhrases[id] {
			names = append(names, regexp.QuoteMeta(phrase))
		}
	case pigstrip.ListExtensions:
		names = append([]string{Bounded(id), Bounded("builtin:" + id)}, ExtensionNames[id]...)
	case pigstrip.ListAPIs:
		names = apiNames(id)
		if len(names) == 0 {
			return nil, fmt.Errorf("no built-in provider uses only %s", id)
		}
	case pigstrip.ListFeatures:
		feature, ok := FeatureNames[id]
		if !ok {
			return nil, fmt.Errorf("feature %q has no entry in leakgate.FeatureNames: list the names its surfaces show", id)
		}
		names = feature
	default:
		return nil, fmt.Errorf("strip list %q has no leak names: map it to the surfaces that show it", list)
	}
	patterns := make([]*regexp.Regexp, len(names))
	for i, name := range names {
		patterns[i] = regexp.MustCompile(name)
	}
	return patterns, nil
}

// apiNames are the IDs and display names of the built-in providers whose catalog models all use api: with api
// stripped, they offer no model.
func apiNames(api string) []string {
	var names []string
	for _, provider := range ai.ListRuntimeProviders() {
		models := ai.ListModels(provider)
		if len(models) == 0 || slices.ContainsFunc(models, func(model ai.GeneratedModel) bool { return string(model.API) != api }) {
			continue
		}
		names = append(names, Bounded(provider), Bounded(ai.ProviderDisplayName(provider)))
	}
	return names
}

// Find returns the lines of text that match any pattern, except lines that contain one of incidental: lines that
// name an ID in another sense, such as "edit" the verb.
func Find(text string, incidental []string, patterns []*regexp.Regexp) []string {
	var found []string
	for line := range strings.SplitSeq(widthx.StripAnsi(text), "\n") {
		if slices.ContainsFunc(incidental, func(phrase string) bool { return strings.Contains(line, phrase) }) {
			continue
		}
		for _, pattern := range patterns {
			if pattern.MatchString(line) {
				found = append(found, fmt.Sprintf("%q (matches %s)", strings.TrimSpace(line), pattern))
				break
			}
		}
	}
	return found
}
