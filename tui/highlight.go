package tui

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// HighlightCode highlights code with the language's syntax, diff, and metadata theme colors and returns one styled line per source line. An empty or unrecognized language uses the code-block color.
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
	// emit writes v in fg; a token spanning lines resets the color at each line break so a multi-line string doesn't bleed into the next line's gutter / surrounding chrome.
	emit := func(fg, v string) {
		if fg == "" {
			buf.WriteString(v)
			return
		}
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
	// A string interpolation is an unmapped nested scope: its unstyled tokens inherit the string color as one segment, and a styled token (number, keyword) ends that segment.
	// upstream: packages/coding-agent/src/utils/syntax-highlight.ts:renderHighlightedHtml
	depth := 0
	var subst strings.Builder
	flushSubst := func() {
		if subst.Len() > 0 {
			emit(t.SyntaxString, subst.String())
			subst.Reset()
		}
	}
	for tok := it(); tok != chroma.EOF; tok = it() {
		fg := syntaxColorFor(tok.Type, t)
		v := tok.Value
		if tok.Type == chroma.LiteralStringInterpol && (depth > 0 || strings.HasSuffix(v, "{") || strings.HasSuffix(v, `\(`)) {
			// Coalesce merges adjacent delimiters ("}${", "}}", ")\(") and some lexers fold a conversion into the closer ("!r}", "=}"), so count every delimiter in the value.
			for _, r := range v {
				switch r {
				case '{', '(':
					depth++
				case '}', ')':
					if depth > 0 {
						depth--
					}
				}
			}
			subst.WriteString(v)
			if depth == 0 {
				flushSubst()
			}
			continue
		}
		if depth > 0 {
			if fg == "" {
				subst.WriteString(v)
				continue
			}
			flushSubst()
		}
		emit(fg, v)
	}
	flushSubst()
	highlighted := buf.String()
	// Chroma's EnsureNL lexers may emit their synthetic final newline. It is lexer input, not a source line for the caller to render.
	if lexer.Config().EnsureNL && !strings.HasSuffix(code, "\n") {
		highlighted = strings.TrimSuffix(highlighted, "\n")
	}
	return strings.Split(highlighted, "\n")
}

// syntaxColorFor maps lexer categories to Pi's syntax, diff, metadata, and tag-name colors.
// upstream: packages/coding-agent/src/modes/interactive/theme/theme.ts:buildCliHighlightTheme
func syntaxColorFor(t chroma.TokenType, theme *Theme) string {
	switch t {
	case chroma.GenericInserted:
		return theme.ToolDiffAdded
	case chroma.GenericDeleted:
		return theme.ToolDiffRemoved
	case chroma.NameDecorator:
		return theme.Muted
	case chroma.NameTag:
		return theme.SyntaxKeyword
	}
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
		case chroma.NameClass, chroma.NameNamespace:
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
