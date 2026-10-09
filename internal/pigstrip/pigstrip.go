// Package pigstrip is the one strip state of a PiG process: the built-ins this
// process runs without. A Piglet Binary's OFF registration shims record the
// ones it compiled out from init, and the startup code records every entry of
// the active Piglet's strip list (cmd/pig recordPigletStrip), so a compiled
// out built-in and a runtime stripped one are the same record. Every
// registry that offers a built-in asks Has where the built-in is registered,
// so no surface derived from that registry can show it, and a request for a
// stripped built-in reports Error.
//
// pig additive (D92): Stock PiG compiles every shim in and runs without a
// Piglet strip list, so Has is always false there.
package pigstrip

import (
	"fmt"
	"slices"
	"sync"
)

// Strip lists, named as in a Piglet's strip key, in manifest order.
const (
	ListTools      = "tools"
	ListCommands   = "commands"
	ListExtensions = "extensions"
	ListAPIs       = "apis"
	ListFeatures   = "features"
)

// Lists returns the five strip lists in manifest order.
func Lists() []string {
	return []string{ListTools, ListCommands, ListExtensions, ListAPIs, ListFeatures}
}

// Known returns the stable IDs list may name, sorted, from the generated ID
// table (ids_generated.go). A command ID keeps its leading slash.
func Known(list string) []string {
	return slices.Clone(knownIDs[list])
}

// Feature IDs. The runtime-only features reuse a Pi switch where the feature
// loads; the others have registration shims a Piglet Binary compiles out with
// the build tag pig_strip_<id> (every character other than a letter or digit
// becomes an underscore).
const (
	// Themes disables custom theme discovery and --theme paths (Pi --no-themes).
	Themes = "themes"
	// Skills disables skill discovery and --skill paths (Pi --no-skills).
	Skills = "skills"
	// PromptTemplates disables prompt template discovery and --prompt-template
	// paths (Pi --no-prompt-templates).
	PromptTemplates = "prompt-templates"
	// NodeExtensions is the embedded Node runtime that runs TypeScript and
	// JavaScript extensions.
	NodeExtensions = "node-extensions"
	// ExtensionSDKGo, ExtensionSDKRust and ExtensionSDKPython are the
	// embedded SDK sources PiG stages to build Go and Rust source extensions
	// and to run Python extensions.
	ExtensionSDKGo     = "extension-sdk-go"
	ExtensionSDKRust   = "extension-sdk-rust"
	ExtensionSDKPython = "extension-sdk-python"
	// SyntaxHighlight is highlight.js grammar highlighting in code blocks and
	// tool output.
	SyntaxHighlight = "syntax-highlight"
	// WordDictionaries are the dictionaries that find word boundaries in CJK,
	// Thai, Lao, Khmer and Burmese text for word navigation.
	WordDictionaries = "word-dictionaries"
	// ExportHTML is the HTML session export (/export to .html, --export,
	// RPC export_html).
	ExportHTML = "export-html"
	// SelfUpdate is `pig update` of the pig binary and the startup new
	// version notice.
	SelfUpdate = "self-update"
	// Changelog is the bundled changelog behind /changelog and the startup
	// What's New.
	Changelog = "changelog"
	// Docs is the bundled PiG documentation (`pig docs`) and the system
	// prompt section that points the model at it.
	Docs = "docs"
	// Mermaid draws mermaid code blocks as diagrams.
	Mermaid = "mermaid"
	// PigletBuilder is `pig piglet build` and `pig piglet publish`.
	PigletBuilder = "piglet-builder"
	// ExperimentalServer is the experimental `server` and `client` commands.
	// Stock cmd/pig already leaves them out (only cmd/pig-experimental links
	// internal/experimental), so it has no build tag.
	ExperimentalServer = "experimental-server"
)

// Features lists every feature ID a Piglet can strip: the runtime-only
// features, then the ones a Piglet Binary compiles out.
func Features() []string {
	return append([]string{Themes, Skills, PromptTemplates, ExperimentalServer}, BinaryFeatures()...)
}

// BinaryFeatures lists the feature IDs a Piglet Binary can compile out with
// a pig_strip_<id> tag.
func BinaryFeatures() []string {
	return []string{
		NodeExtensions, ExtensionSDKGo, ExtensionSDKRust, ExtensionSDKPython,
		SyntaxHighlight, WordDictionaries, ExportHTML, SelfUpdate, Changelog,
		Docs, Mermaid, PigletBuilder,
	}
}

// Tag is the Go build tag that compiles id out: pig_strip_ followed by id
// with every character other than an ASCII letter or digit replaced by an
// underscore (llama.cpp becomes pig_strip_llama_cpp).
func Tag(id string) string {
	b := []byte("pig_strip_" + id)
	for i := len("pig_strip_"); i < len(b); i++ {
		if c := b[i]; (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			b[i] = '_'
		}
	}
	return string(b)
}

type entry struct{ list, id string }

var (
	mu       sync.RWMutex
	stripped []entry
)

// Strip records that list/id is absent from this process. A registration
// shim compiled with pig_strip_<id> calls it from init; the startup code
// calls it for each entry of the active Piglet's strip list. The returned
// function undoes the call.
func Strip(list, id string) (undo func()) {
	mu.Lock()
	defer mu.Unlock()
	e := entry{list, id}
	if slices.Contains(stripped, e) {
		return func() {}
	}
	stripped = append(stripped, e)
	return func() {
		mu.Lock()
		defer mu.Unlock()
		if i := slices.Index(stripped, e); i >= 0 {
			stripped = slices.Delete(stripped, i, i+1)
		}
	}
}

// Has reports whether list/id is stripped from this process. A command is
// named with its leading slash, as in the strip list.
func Has(list, id string) bool {
	mu.RLock()
	defer mu.RUnlock()
	return slices.Contains(stripped, entry{list, id})
}

// Active reports whether this process strips anything. It is false in Stock
// PiG.
func Active() bool {
	mu.RLock()
	defer mu.RUnlock()
	return len(stripped) > 0
}

// IDs lists the stripped IDs of list in the order they were stripped. It is empty in Stock PiG.
func IDs(list string) []string {
	mu.RLock()
	defer mu.RUnlock()
	var ids []string
	for _, e := range stripped {
		if e.list == list {
			ids = append(ids, e.id)
		}
	}
	return ids
}

// Error is the error a request for a stripped built-in reports. what names
// the built-in for the user, for example "HTML export".
func Error(what, list, id string) error {
	return fmt.Errorf("%s is stripped from this Piglet (strip.%s: %s)", what, list, id)
}
