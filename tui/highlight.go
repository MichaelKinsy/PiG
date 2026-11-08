package tui

// Ports packages/coding-agent/src/utils/syntax-highlight.ts and the highlightCode and getCliHighlightTheme helpers of packages/coding-agent/src/modes/interactive/theme/theme.ts.

import (
	"errors"
	"math"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// pig additive (D92): highlightEngine, highlightRegistry and highlightStripped live in highlight_hljs_on.go, or in highlight_hljs_off.go for a Piglet Binary built without syntax-highlight.

// HighlightFormatter styles the text of one highlight.js scope.
type HighlightFormatter func(text string) string

// HighlightTheme maps highlight.js scope names to formatters; "default" styles text outside every mapped scope.
type HighlightTheme map[string]HighlightFormatter

// HighlightOptions are the options of syntax-highlight.ts highlight. An empty Language auto-detects among LanguageSubset, or every language.
type HighlightOptions struct {
	Language       string
	IgnoreIllegals bool
	LanguageSubset []string
	Theme          HighlightTheme
}

var loadAllHighlightLanguagesOnce sync.Once

// LoadAllHighlightLanguages registers every highlight.js language, as loadAllHighlightLanguages does, and returns when they are available. It runs once; later calls wait for that load. Highlighted code cached before the load is discarded, because more languages now highlight.
func LoadAllHighlightLanguages() {
	// pig additive (D92): a Piglet that strips syntax-highlight loads no grammar.
	if highlightStripped() {
		return
	}
	loadAllHighlightLanguagesOnce.Do(func() {
		highlightRegistry().LoadAllLanguages()
		hlMu.Lock()
		resetHighlightCacheLocked()
		hlGeneration++
		hlMu.Unlock()
	})
}

// SupportsLanguage reports whether highlight.js has a language or alias of that name.
func SupportsLanguage(name string) bool {
	// pig additive (D92): a Piglet that strips syntax-highlight supports no language.
	if highlightStripped() {
		return false
	}
	return highlightRegistry().SupportsLanguage(name)
}

// Highlight highlights code with highlight.js and renders its scopes with the theme. An error is an exception highlight.js throws, such as an unknown language.
func Highlight(code string, options HighlightOptions) (string, error) {
	// pig additive (D92): a Piglet that strips syntax-highlight highlights as highlight.js does with no language registered.
	if highlightStripped() {
		out, err := highlightPlain(code, options)
		return jsstring.Canonical(out), err
	}
	out, err := highlightWith(highlightRegistry(), code, options)
	return jsstring.Canonical(out), err
}

func highlightWith(registry highlightEngine, code string, options HighlightOptions) (string, error) {
	var html string
	var err error
	if options.Language != "" {
		html, err = registry.Highlight(code, options.Language, options.IgnoreIllegals)
	} else {
		html, err = registry.HighlightAuto(code, options.LanguageSubset)
	}
	if err != nil {
		return "", err
	}
	return renderHighlightedHTML(html, options.Theme)
}

const highlightClassPrefix = "hljs-"

// getScopeFromSpanTag returns the first hljs- class of a span tag without its prefix.
func getScopeFromSpanTag(tag string) (string, bool) {
	classValue, ok := spanClassValue(tag)
	if !ok || classValue == "" {
		return "", false
	}
	for _, className := range splitJSWhitespace(classValue) {
		if scope, ok := strings.CutPrefix(className, highlightClassPrefix); ok {
			return scope, true
		}
	}
	return "", false
}

// spanClassValue is the value matched by /\sclass\s*=\s*(?:"([^"]*)"|'([^']*)')/ in a tag.
func spanClassValue(tag string) (string, bool) {
	for i := 0; i < len(tag); {
		r, size := jsstring.DecodeRuneInString(tag[i:])
		if !jsstring.IsSpace(r) || !strings.HasPrefix(tag[i+size:], "class") {
			i += size
			continue
		}
		j := skipJSWhitespace(tag, i+size+len("class"))
		if j < len(tag) && tag[j] == '=' {
			j = skipJSWhitespace(tag, j+1)
			if j < len(tag) && (tag[j] == '"' || tag[j] == '\'') {
				if end := strings.IndexByte(tag[j+1:], tag[j]); end >= 0 {
					return tag[j+1 : j+1+end], true
				}
			}
		}
		i += size
	}
	return "", false
}

func skipJSWhitespace(text string, i int) int {
	for i < len(text) {
		r, size := jsstring.DecodeRuneInString(text[i:])
		if !jsstring.IsSpace(r) {
			break
		}
		i += size
	}
	return i
}

// splitJSWhitespace is String.prototype.split(/\s+/).
func splitJSWhitespace(text string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(text); {
		r, size := jsstring.DecodeRuneInString(text[i:])
		if !jsstring.IsSpace(r) {
			i += size
			continue
		}
		parts = append(parts, text[start:i])
		i = skipJSWhitespace(text, i)
		start = i
	}
	return append(parts, text[start:])
}

// formatter is a theme[scope] property read, true when the value is truthy. The theme is an object literal: an own property shadows Object.prototype even when it is undefined (a nil entry), and a scope naming an Object.prototype member finds that member. syntax-highlight.ts calls it as a strict-mode function with an undefined receiver: Object(text) is a String wrapper that concatenation reads back as text, toString returns "[object Undefined]", isPrototypeOf returns false for a string, __proto__ is an object, and the other members throw a TypeError.
func (theme HighlightTheme) formatter(scope string) (HighlightFormatter, bool) {
	if f, ok := theme[scope]; ok {
		return f, f != nil
	}
	switch scope {
	case "constructor":
		return func(text string) string { return text }, true
	case "toString":
		return func(string) string { return "[object Undefined]" }, true
	case "isPrototypeOf":
		return func(string) string { return "false" }, true
	case "toLocaleString":
		return throwHighlightThemeError("Object.prototype.toLocaleString called on null or undefined"), true
	case "__proto__":
		return throwHighlightThemeError("formatter is not a function"), true
	case "__defineGetter__", "__defineSetter__", "__lookupGetter__", "__lookupSetter__", "hasOwnProperty", "propertyIsEnumerable", "valueOf":
		return throwHighlightThemeError("Cannot convert undefined or null to object"), true
	}
	return nil, false
}

func throwHighlightThemeError(message string) HighlightFormatter {
	return func(string) string { panic(highlightThemeError{message}) }
}

// isHighlightThemeThrow reports whether err is what a theme formatter throws: the TypeError of a formatter read from Object.prototype, or the Error of theme.fg for a token the theme lacks.
func isHighlightThemeThrow(err error) bool {
	var typeError highlightThemeError
	var unknownColor unknownThemeColorError
	return errors.As(err, &typeError) || errors.As(err, &unknownColor)
}

// highlightThemeError is the TypeError a theme formatter read from Object.prototype throws.
type highlightThemeError struct{ message string }

func (e highlightThemeError) Error() string { return e.message }

func getScopeFormatter(scope string, theme HighlightTheme) (HighlightFormatter, bool) {
	if f, ok := theme.formatter(scope); ok {
		return f, true
	}
	if before, _, ok := strings.Cut(scope, "."); ok {
		if f, ok := theme.formatter(before); ok {
			return f, true
		}
	}
	if before, _, ok := strings.Cut(scope, "-"); ok {
		if f, ok := theme.formatter(before); ok {
			return f, true
		}
	}
	return nil, false
}

// scopeEntry is a span on the scope stack; a span without an hljs- class pushes an undefined scope.
type scopeEntry struct {
	scope   string
	defined bool
}

func getActiveFormatter(scopes []scopeEntry, theme HighlightTheme) (HighlightFormatter, bool) {
	for _, scope := range slices.Backward(scopes) {
		if !scope.defined {
			continue
		}
		if f, ok := getScopeFormatter(scope.scope, theme); ok {
			return f, true
		}
	}
	return theme.formatter("default")
}

func isSpanOpenTagStart(html string, index int) bool {
	if !strings.HasPrefix(html[index:], "<span") {
		return false
	}
	if index+len("<span") >= len(html) {
		return false
	}
	switch html[index+len("<span")] {
	case '>', ' ', '\t', '\n', '\r':
		return true
	}
	return false
}

// RenderHighlightedHtml renders highlight.js HTML as text: each run of text takes the formatter of its innermost mapped scope, or the theme's default, and HTML character references are decoded.
// The text keeps JavaScript's UTF-16 units: highlight.js can split a surrogate pair between two scopes, and the lone halves stay lone (measured as nothing, as Pi measures them) until the terminal write encodes them. Halves that end up side by side take the ordinary spelling of the pair.
func RenderHighlightedHtml(html string, theme HighlightTheme) string {
	out, err := renderHighlightedHTML(html, theme)
	if err != nil {
		panic(err)
	}
	return jsstring.Canonical(out)
}

func renderHighlightedHTML(html string, theme HighlightTheme) (output string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			thrown, ok := recovered.(error)
			if !ok || !isHighlightThemeThrow(thrown) {
				panic(recovered)
			}
			err = thrown
		}
	}()
	var out, textBuffer strings.Builder
	var scopes []scopeEntry
	flushText := func() {
		if textBuffer.Len() == 0 {
			return
		}
		if f, ok := getActiveFormatter(scopes, theme); ok {
			// Each non-empty line takes the formatter on its own, so a token that spans lines (a multiline string or comment) keeps its color on every line after a terminal wraps or splits the output (#10143).
			for i, line := range strings.Split(textBuffer.String(), "\n") {
				if i > 0 {
					out.WriteByte('\n')
				}
				if line != "" {
					out.WriteString(f(line))
				}
			}
		} else {
			out.WriteString(textBuffer.String())
		}
		textBuffer.Reset()
	}
	for index := 0; index < len(html); {
		if isSpanOpenTagStart(html, index) {
			if tagEnd := strings.IndexByte(html[index+5:], '>'); tagEnd >= 0 {
				tagEnd += index + 5
				flushText()
				scope, ok := getScopeFromSpanTag(html[index : tagEnd+1])
				scopes = append(scopes, scopeEntry{scope, ok})
				index = tagEnd + 1
				continue
			}
		}
		if strings.HasPrefix(html[index:], "</span>") {
			flushText()
			if len(scopes) > 0 {
				scopes = scopes[:len(scopes)-1]
			}
			index += len("</span>")
			continue
		}
		if html[index] == '&' {
			if text, length, ok := decodeHTMLEntityAt(html, index); ok {
				textBuffer.WriteString(text)
				index += length
				continue
			}
		}
		textBuffer.WriteByte(html[index])
		index++
	}
	flushText()
	return out.String(), nil
}

// decodeHTMLEntityAt ports utils/html.ts decodeHtmlEntityAt: the reference ends at the next semicolon within 16 UTF-16 code units. length is in bytes of html.
func decodeHTMLEntityAt(html string, index int) (text string, length int, ok bool) {
	semicolon := strings.IndexByte(html[index+1:], ';')
	if semicolon < 0 {
		return "", 0, false
	}
	semicolon += index + 1
	if len(jsstring.ToUTF16(html[index:semicolon])) > 16 {
		return "", 0, false
	}
	decoded, ok := decodeHTMLEntity(html[index+1 : semicolon])
	if !ok {
		return "", 0, false
	}
	return decoded, semicolon - index + 1, true
}

// decodeHTMLEntity ports utils/html.ts decodeHtmlEntity.
func decodeHTMLEntity(entity string) (string, bool) {
	switch entity {
	case "amp":
		return "&", true
	case "lt":
		return "<", true
	case "gt":
		return ">", true
	case "quot":
		return `"`, true
	case "apos":
		return "'", true
	}
	if rest, ok := strings.CutPrefix(entity, "#x"); ok {
		return decodeCodePoint(jsParseInt(rest, 16))
	}
	if rest, ok := strings.CutPrefix(entity, "#X"); ok {
		return decodeCodePoint(jsParseInt(rest, 16))
	}
	if rest, ok := strings.CutPrefix(entity, "#"); ok {
		return decodeCodePoint(jsParseInt(rest, 10))
	}
	return "", false
}

// decodeCodePoint is String.fromCodePoint for an integer code point; a surrogate code point stays a lone UTF-16 unit (WTF-8).
func decodeCodePoint(codePoint float64) (string, bool) {
	if math.IsNaN(codePoint) || math.IsInf(codePoint, 0) || codePoint != math.Trunc(codePoint) || codePoint < 0 || codePoint > 0x10ffff {
		return "", false
	}
	r := rune(codePoint)
	if r >= 0xd800 && r <= 0xdfff {
		return string([]byte{byte(0xe0 | (r >> 12)), byte(0x80 | ((r >> 6) & 0x3f)), byte(0x80 | (r & 0x3f))}), true
	}
	return string(r), true
}

// jsParseInt is Number.parseInt(text, radix) for radix 10 or 16.
func jsParseInt(text string, radix int) float64 {
	text = text[skipJSWhitespace(text, 0):]
	sign := 1.0
	if text != "" && (text[0] == '+' || text[0] == '-') {
		if text[0] == '-' {
			sign = -1
		}
		text = text[1:]
	}
	if radix == 16 && len(text) >= 2 && text[0] == '0' && (text[1] == 'x' || text[1] == 'X') {
		text = text[2:]
	}
	value, digits := 0.0, 0
	for _, c := range []byte(text) {
		var d int
		switch {
		case c >= '0' && c <= '9':
			d = int(c - '0')
		case radix == 16 && c >= 'a' && c <= 'f':
			d = int(c-'a') + 10
		case radix == 16 && c >= 'A' && c <= 'F':
			d = int(c-'A') + 10
		default:
			d = radix
		}
		if d >= radix {
			break
		}
		value = float64(value*float64(radix)) + float64(d)
		digits++
	}
	if digits == 0 {
		return math.NaN()
	}
	return sign * value
}

// cliHighlightTokens are the theme color tokens theme.ts buildCliHighlightTheme gives the highlight.js classes that take a color, in its order.
var cliHighlightTokens = [...]struct{ class, token string }{
	{"keyword", "syntaxKeyword"},
	{"built_in", "syntaxType"},
	{"literal", "syntaxNumber"},
	{"number", "syntaxNumber"},
	{"regexp", "syntaxString"},
	{"string", "syntaxString"},
	{"subst", "text"},
	{"comment", "syntaxComment"},
	{"doctag", "syntaxComment"},
	{"meta", "muted"},
	{"function", "syntaxFunction"},
	{"title", "syntaxFunction"},
	{"class", "syntaxType"},
	{"type", "syntaxType"},
	{"tag", "syntaxPunctuation"},
	{"name", "syntaxKeyword"},
	{"attr", "syntaxVariable"},
	{"variable", "syntaxVariable"},
	{"params", "syntaxVariable"},
	{"operator", "syntaxOperator"},
	{"punctuation", "syntaxPunctuation"},
	{"addition", "toolDiffAdded"},
	{"deletion", "toolDiffRemoved"},
}

// cliHighlightTheme is theme.ts buildCliHighlightTheme for a theme.
func cliHighlightTheme(t *Theme) HighlightTheme {
	theme := make(HighlightTheme, len(cliHighlightTokens)+3)
	for _, entry := range &cliHighlightTokens {
		token := entry.token
		theme[entry.class] = func(s string) string { return t.Fg(token, s) }
	}
	theme["emphasis"] = func(s string) string { return markdownDecoration("\x1b[3m", SGRItalicReset, s) }
	theme["strong"] = func(s string) string { return markdownDecoration("\x1b[1m", SGRBoldDimReset, s) }
	theme["link"] = func(s string) string { return markdownDecoration("\x1b[4m", SGRUnderlineReset, s) }
	return theme
}

// HighlightCode highlights code for the active theme and returns its lines, as theme.ts highlightCode does, which getMarkdownTheme().highlightCode is too. Code in a language highlight.js does not support, and code highlight.js fails on, take the mdCodeBlock color.
func HighlightCode(code, lang string) []string {
	return highlightCodeLines(code, lang)
}

// WarmHighlight highlights code for the active theme as HighlightCode does and discards the lines. HighlightCode of a longer
// text that starts with code then continues from the state this leaves, instead of highlighting from the first line, so a
// caller whose text streams in can pay for the whole text a step at a time and not all at once when the text is complete.
// The lines are not kept in the memo: each warm-up is a new text that nothing asks for again, and kept lines would fill the
// memo's budget, whose reset also drops the resume state.
// It reports whether such a state is held; false means the text cannot resume (a language highlight.js lacks, a build without
// syntax highlighting, or code the engine cannot resume over), so a caller that warms as the text grows stops warming.
// pig additive (D112): the lines HighlightCode returns are the same with or without a warm-up; only when they are computed differs.
func WarmHighlight(code, lang string) bool {
	if highlightStripped() {
		return false
	}
	t := ActiveTheme()
	hlMu.Lock()
	if t != hlTheme {
		// The active theme changed: drop the colors cached for the old one, as highlightCodeLines does.
		resetHighlightCacheLocked()
		hlTheme = t
	}
	hlMu.Unlock()
	highlightResumable(highlightRegistry(), code, lang, t)
	return highlightResumeHeld(code, lang, t)
}

// HighlightEpoch is what HighlightCode's lines depend on besides its arguments: the active theme and the highlight.js
// languages loaded. Within one epoch HighlightCode returns the same lines for the same code and language, so a caller may
// keep lines it highlighted instead of highlighting the same code again.
type HighlightEpoch struct {
	theme      *Theme
	generation int
}

// CurrentHighlightEpoch is the epoch HighlightCode highlights in now.
func CurrentHighlightEpoch() HighlightEpoch {
	theme := ActiveTheme()
	hlMu.Lock()
	defer hlMu.Unlock()
	return HighlightEpoch{theme: theme, generation: hlGeneration}
}

// CodeHighlighter highlights code for one owner, such as a tool row, and reuses its last result while the code, the
// language and the HighlightEpoch stay the same (theme.ts CodeHighlighter). A row redraws on every progress update, timer
// tick and expand toggle and cannot tell a theme change from any other redraw, so the highlighter checks what its result
// depends on. Each owner keeps its own, so a result lives as long as the code it shows: the process-wide memo of
// HighlightCode is bounded and drops an entry larger than its budget, which a row's whole file can be. The zero value is
// ready; a CodeHighlighter is not safe for concurrent use.
//
// Ports packages/coding-agent/src/modes/interactive/theme/theme.ts (CodeHighlighter).
type CodeHighlighter struct {
	code, lang string
	epoch      HighlightEpoch
	lines      []string
}

// Highlight is HighlightCode, and the result is shared with later calls, so the caller must not modify it.
func (h *CodeHighlighter) Highlight(code, lang string) []string {
	// The epoch is read before highlighting: a grammar that loads meanwhile makes the next call highlight again.
	epoch := CurrentHighlightEpoch()
	if h.lines != nil && h.code == code && h.lang == lang && h.epoch == epoch {
		return h.lines
	}
	h.code, h.lang, h.epoch, h.lines = code, lang, epoch, HighlightCode(code, lang)
	return h.lines
}

// highlightCodeLines is highlightCode with the memo and the resume state (D112). Both keep the lines they computed, so
// each caller gets its own copy: theme.ts highlightCode returns a new array on every call, and its callers write into
// it (write.ts updateWriteHighlightCacheIncremental sets highlightedLines[i]), which must not change a later call's lines.
func highlightCodeLines(code, lang string) []string {
	t := ActiveTheme()
	// pig additive (D92): a Piglet that strips syntax-highlight renders every code block as Pi renders a language highlight.js does not support.
	if highlightStripped() {
		return codeBlockLines(code, t)
	}
	key := hlKey{lang: lang, code: code}

	hlMu.Lock()
	if t != hlTheme {
		// The active theme changed (e.g. /theme): drop the colors cached for the old one.
		resetHighlightCacheLocked()
		hlTheme = t
	}
	if v, ok := hlCache[key]; ok {
		hlMu.Unlock()
		return slices.Clone(v)
	}
	generation := hlGeneration
	hlMu.Unlock()

	out, ok := highlightResumable(highlightRegistry(), code, lang, t)
	if !ok {
		out = highlightCodeUncached(highlightRegistry(), code, lang, t)
	}

	hlMu.Lock()
	if t == hlTheme && generation == hlGeneration {
		storeHighlightLocked(key, out)
	}
	hlMu.Unlock()
	return slices.Clone(out)
}

// storeHighlightLocked caches out for key; hlMu must be held. A streamed code block is highlighted once per frame with a longer prefix of its code, and only the newest prefix can be asked for again, so the entry this one extends is dropped. Past hlCacheMax entries or hlCacheMaxBytes the memo starts over; an entry larger than the budget is not cached.
func storeHighlightLocked(key hlKey, out []string) {
	size := hlEntrySize(key, out)
	if size > hlCacheMaxBytes {
		return
	}
	// A code block that is streamed stores a longer prefix per frame, and each replaces the entry before it, so those keys are not copied, or streaming a block of n bytes would copy O(n) bytes per frame more (TestScalingStreamedCodeBlockHighlight). The key of the frame that ends up staying is copied when the stream ends, below.
	streamed := false
	if last := hlLast; last.lang == key.lang && strings.HasPrefix(key.code, last.code) {
		if lines, ok := hlCache[last]; ok {
			delete(hlCache, last)
			hlCacheBytes -= hlEntrySize(last, lines)
			streamed = true
		}
	}
	// Any other key must not hold the text it was sliced from: the code of a fence is a part of a chunk that the block lexer copied the document's strings to, and a memo entry would keep the whole chunk, which hlEntrySize does not count.
	if !streamed {
		// The block that was streamed before this one is complete: its key, the last of its frames, keeps the text it was sliced from until it has its own.
		if lines, ok := hlCache[hlLast]; ok && hlLastShared {
			delete(hlCache, hlLast)
			hlCache[hlKey{lang: strings.Clone(hlLast.lang), code: strings.Clone(hlLast.code)}] = lines
		}
		key = hlKey{lang: strings.Clone(key.lang), code: strings.Clone(key.code)}
	}
	if len(hlCache) >= hlCacheMax || hlCacheBytes+size > hlCacheMaxBytes {
		resetHighlightCacheLocked()
	}
	hlCache[key] = out
	hlCacheBytes += size
	hlLast, hlLastShared = key, streamed
}

// hlKey memoizes highlighted lines for the active theme and loaded languages.
type hlKey struct {
	lang, code string
}

// pig additive (D112): Pi keeps no process-wide highlight memo. Its Markdown component keeps the highlighting of each code block it rendered until invalidate(), and a CodeHighlighter keeps a row's last result (markdown.ts render, theme.ts CodeHighlighter; Markdown.highlights and CodeHighlighter here), so a streamed answer and a redrawn row highlight the same code once. The memo also serves a new component and a code block that was streamed through a longer prefix (the resume state). Its bounds are a Go CPU choice, and a miss, an eviction or a reset returns the same lines as a hit.
// hlCacheMax bounds the memo so a long session cannot grow it without limit; clearing on overflow keeps memory bounded at the cost of an occasional cold re-highlight.
const hlCacheMax = 1024

// hlCacheMaxBytes bounds the bytes the memo retains. Streaming a code block re-highlights a longer prefix every frame, and each prefix is a new key that is never asked for again, so the entry count alone lets a large block pin hundreds of megabytes.
// An entry larger than the budget is not cached.
// pig additive (D112): see hlCacheMax.
const hlCacheMaxBytes = 8 << 20

var (
	hlMu         sync.Mutex
	hlTheme      *Theme
	hlGeneration int
	hlCache      = map[hlKey][]string{}
	hlCacheBytes int
	// hlLast is the entry stored last, which the next prefix of a streamed block replaces. hlLastShared reports that its key was stored without a copy of its strings, which the next store that does not continue the block makes.
	hlLast       hlKey
	hlLastShared bool
)

// hlEntrySize is the bytes a memo entry retains: the code held by its key and the highlighted lines.
func hlEntrySize(key hlKey, lines []string) int {
	size := len(key.code) + len(key.lang)
	for _, line := range lines {
		size += len(line)
	}
	return size
}

// resetHighlightCacheLocked empties the memo; hlMu must be held.
func resetHighlightCacheLocked() {
	clear(hlCache)
	hlCacheBytes = 0
	hlLast, hlLastShared = hlKey{}, false
	resetHighlightResumesLocked()
}

// highlightCodeUncached is the pure highlighter behind the memo: streaming re-parses a message every frame, so closed code blocks would otherwise re-highlight with unchanged input.
func highlightCodeUncached(registry highlightEngine, code, lang string, t *Theme) []string {
	if lang == "" || !registry.SupportsLanguage(lang) {
		return codeBlockLines(code, t)
	}
	highlighted, err := highlightWith(registry, code, HighlightOptions{Language: lang, IgnoreIllegals: true, Theme: cliHighlightTheme(t)})
	if err != nil {
		// theme.ts highlightCode: `catch { return code.split("\n").map((line) => theme.fg("mdCodeBlock", line)); }`.
		return codeBlockLines(code, t)
	}
	return strings.Split(jsstring.Canonical(highlighted), "\n")
}

func codeBlockLines(code string, t *Theme) []string {
	lines := strings.Split(code, "\n")
	for i, line := range lines {
		lines[i] = t.Fg("mdCodeBlock", line)
	}
	return lines
}

// LanguageFromPath returns the highlight language for a file path, or "" when none maps, as theme.ts getLanguageFromPath does: the extension is the text after the path's last dot, or the whole path when it has none, lowercased as String.prototype.toLowerCase does.
func LanguageFromPath(path string) string {
	ext := path[strings.LastIndexByte(path, '.')+1:]
	if ext == "" {
		return ""
	}
	// strings.ToLower maps U+0130 to "i"; toLowerCase maps it to "i\u0307" (SpecialCasing.txt). Every other character lowercases to the same ASCII letters in both.
	return extToLang()[strings.ToLower(strings.ReplaceAll(ext, "\u0130", "i\u0307"))]
}

// extToLang is getLanguageFromPath's extension table, built on first use, not on every start.
var extToLang = sync.OnceValue(func() map[string]string {
	return map[string]string{
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
		"dart":       "dart", // pig divergence (D260): Pi main c13f6acfb maps dart, groovy, gradle and nix
		"groovy":     "groovy",
		"gradle":     "groovy",
		"nix":        "nix",
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
})
