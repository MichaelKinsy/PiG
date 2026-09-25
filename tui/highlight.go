package tui

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// HighlightCode highlights a code string using the given language hint and
// returns the result split on newlines. Each returned line has the language's
// syntax tokens wrapped in the theme's syntax* foreground colors.
//
// Mirrors upstream
// .upstream/current/packages/coding-agent/src/modes/interactive/theme/theme.ts::highlightCode
// (1099-1118 in v0.69.0). Upstream uses cli-highlight (a Node binding to
// highlight.js); pig uses chroma (pure-Go lexer set covering 200+
// languages). The behaviour contract is the same: return one styled line
// per source line, or fall back to a single-color render when no lexer is
// available.
//
// When lang is empty or unrecognized, returns each line wrapped in
// SyntaxComment-ish "code block" colour (matches upstream's mdCodeBlock
// fallback path).
func HighlightCode(code, lang string) []string {
	t := ActiveTheme()
	key := hlKey{lang: lang, code: code}

	hlMu.Lock()
	if t != hlTheme {
		// Active theme changed (e.g. /theme): the memoized colors are
		// stale. Drop the cache and rebind to the new theme.
		clear(hlCache)
		hlTheme = t
	}
	if v, ok := hlCache[key]; ok {
		hlMu.Unlock()
		return v
	}
	hlMu.Unlock()

	// Compute outside the lock; chroma lexing is the slow part.
	out := highlightCodeUncached(code, lang, t)

	hlMu.Lock()
	if t == hlTheme { // theme unchanged while we computed
		if len(hlCache) >= hlCacheMax {
			clear(hlCache)
		}
		hlCache[key] = out
	}
	hlMu.Unlock()
	return out
}

// hlKey memoizes HighlightCode by (lang, code) for the active theme.
type hlKey struct{ lang, code string }

// hlCacheMax bounds the memo so a long session cannot grow it without
// limit. A single streaming turn has only a handful of code blocks, so it
// never approaches the cap; clearing on overflow keeps memory O(1) at the
// cost of an occasional cold re-highlight.
const hlCacheMax = 1024

var (
	hlMu    sync.Mutex
	hlTheme *Theme
	hlCache = map[hlKey][]string{}
)

// highlightCodeUncached is the pure highlighter. HighlightCode wraps it with
// a memo: syntax highlighting dominates markdown parse cost, and streaming
// re-parses the whole message every frame, re-highlighting already-closed
// code blocks whose (lang, code) never change. The output is a pure function
// of (lang, code, theme), so the memo is byte-identical to calling this
// directly. Callers treat the returned slice as read-only.
func highlightCodeUncached(code, lang string, t *Theme) []string {
	if lang == "" {
		return fallbackCodeLines(code, t)
	}
	lexer := lexers.Get(lang)
	if lexer == nil {
		lexer = lexers.Match("file." + lang)
	}
	if lexer == nil {
		return fallbackCodeLines(code, t)
	}
	lexer = chroma.Coalesce(lexer)

	it, err := lexer.Tokenise(nil, code)
	if err != nil {
		return fallbackCodeLines(code, t)
	}

	var buf strings.Builder
	for tok := it(); tok != chroma.EOF; tok = it() {
		fg := syntaxColorFor(tok.Type, t)
		v := tok.Value
		if fg == "" {
			buf.WriteString(v)
			continue
		}
		// Tokens may span multiple lines; we still want each line to
		// reset color at the line break so a multi-line string doesn't
		// bleed into the next line's gutter / surrounding chrome.
		segs := strings.Split(v, "\n")
		for i, seg := range segs {
			if seg != "" {
				buf.WriteString(fg)
				buf.WriteString(seg)
				buf.WriteString(SGRFgReset)
			}
			if i < len(segs)-1 {
				buf.WriteByte('\n')
			}
		}
	}
	return strings.Split(buf.String(), "\n")
}

// syntaxColorFor maps a chroma TokenType to a theme syntax color. We collapse
// chroma's ~140 token types onto the 9 syntax* tokens upstream defines
// (comment / keyword / function / variable / string / number / type /
// operator / punctuation). This matches upstream's cli-highlight palette
// which only colors those nine groups.
//
// Note: chroma's `Category()` returns the top-level group (Keyword, Name,
// Literal, …) so we check Category for those that are uniform, and
// SubCategory for the Literal family where the relevant distinction
// (String vs Number) lives.
func syntaxColorFor(t chroma.TokenType, theme *Theme) string {
	switch t.SubCategory() {
	case chroma.LiteralString:
		return theme.SyntaxString
	case chroma.LiteralNumber:
		return theme.SyntaxNumber
	}
	switch t.Category() {
	case chroma.Comment:
		return theme.SyntaxComment
	case chroma.Keyword:
		// KeywordType has its own SyntaxType slot upstream.
		if t == chroma.KeywordType {
			return theme.SyntaxType
		}
		return theme.SyntaxKeyword
	case chroma.Operator:
		return theme.SyntaxOperator
	case chroma.Punctuation:
		return theme.SyntaxPunctuation
	case chroma.Name:
		switch t {
		case chroma.NameFunction, chroma.NameFunctionMagic, chroma.NameBuiltin:
			return theme.SyntaxFunction
		case chroma.NameClass, chroma.NameNamespace, chroma.NameDecorator:
			return theme.SyntaxType
		case chroma.NameVariable, chroma.NameVariableClass, chroma.NameVariableGlobal,
			chroma.NameVariableInstance, chroma.NameAttribute:
			return theme.SyntaxVariable
		default:
			return "" // plain name: leave uncolored
		}
	}
	return ""
}

func fallbackCodeLines(code string, t *Theme) []string {
	lines := strings.Split(code, "\n")
	out := make([]string, len(lines))
	for i, ln := range lines {
		if ln == "" {
			out[i] = ""
			continue
		}
		out[i] = t.MDCodeBlock + ln + SGRFgReset
	}
	return out
}

// LanguageFromPath returns a language identifier for the given file path,
// or empty if no mapping exists. Mirrors upstream getLanguageFromPath in
// theme.ts:1015-1077. The extension table is kept in lock-step with
// upstream so a file rendered through `read` displays identically in both
// implementations.
func LanguageFromPath(path string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	if ext == "" {
		// Some files use the basename as the "extension" (Dockerfile,
		// Makefile, CMakeLists.txt). Upstream uses the last dot-segment;
		// we fall back to the basename for the extensionless cases.
		base := strings.ToLower(filepath.Base(path))
		if lang, ok := extToLang[base]; ok {
			return lang
		}
		return ""
	}
	return extToLang[ext]
}

// extToLang mirrors theme.ts:1019-1076 byte-for-byte. Do not reorder or
// extend without checking upstream first.
var extToLang = map[string]string{
	"ts":         "typescript",
	"tsx":        "typescript",
	"js":         "javascript",
	"jsx":        "javascript",
	"mjs":        "javascript",
	"cjs":        "javascript",
	"py":         "python",
	"rb":         "ruby",
	"rs":         "rust",
	"go":         "go",
	"java":       "java",
	"kt":         "kotlin",
	"swift":      "swift",
	"c":          "c",
	"h":          "c",
	"cpp":        "cpp",
	"cc":         "cpp",
	"cxx":        "cpp",
	"hpp":        "cpp",
	"cs":         "csharp",
	"php":        "php",
	"sh":         "bash",
	"bash":       "bash",
	"zsh":        "bash",
	"fish":       "fish",
	"ps1":        "powershell",
	"sql":        "sql",
	"html":       "html",
	"htm":        "html",
	"css":        "css",
	"scss":       "scss",
	"sass":       "sass",
	"less":       "less",
	"json":       "json",
	"yaml":       "yaml",
	"yml":        "yaml",
	"toml":       "toml",
	"xml":        "xml",
	"md":         "markdown",
	"markdown":   "markdown",
	"dockerfile": "dockerfile",
	"makefile":   "makefile",
	"cmake":      "cmake",
	"lua":        "lua",
	"perl":       "perl",
	"r":          "r",
	"scala":      "scala",
	"clj":        "clojure",
	"ex":         "elixir",
	"exs":        "elixir",
	"erl":        "erlang",
	"hs":         "haskell",
	"ml":         "ocaml",
	"vim":        "vim",
	"graphql":    "graphql",
	"proto":      "protobuf",
	"tf":         "hcl",
	"hcl":        "hcl",
}
