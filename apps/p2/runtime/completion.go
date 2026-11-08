package runtime

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/jsstring/localecompare"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Ports apps/p2/src/runtime/completion.ts: `@` and Tab file completion text handling, shared by the TUI provider and the
// web composers. Pure copies of pi-tui autocomplete.ts helpers (token boundaries, quoting, `@`/path prefix extraction,
// completion insertion, forced-Tab gating) without its filesystem and `fd` parts: suggestions come from the session's
// `FileIndex`. Completion is cwd-relative only: `~`, absolute and `..` prefixes complete nothing.
//
// Columns and offsets are byte offsets into UTF-8 strings, the convention of PiG's tui autocomplete providers (the
// editor converts to and from its UTF-16 positions at its boundary); the TypeScript original counts UTF-16 units.

// pathDelimiters is PATH_DELIMITERS: bytes that end a path token (all ASCII, so a byte test is exact in UTF-8).
const pathDelimiters = " \t\"'="

// pathWrapper is PATH_WRAPPERS: the closing counterpart of an opening wrapper that may precede a path in prose, or 0.
func pathWrapper(c byte) byte {
	switch c {
	case '(':
		return ')'
	case '[':
		return ']'
	case '{':
		return '}'
	case '<':
		return '>'
	case '`':
		return '`'
	}
	return 0
}

const cjkWideRunes = "，．：；！？（）［］｛｝“”‘’…—"

// isSeparator is the `separator` expression: JavaScript white space or a CJK punctuation break.
func isSeparator(r rune) bool {
	return widthx.IsJSSpace(r) || strings.ContainsRune(cjkWideRunes, r) || (unicode.IsPunct(r) && widthx.IsCJKBreak(string(r)))
}

// endsAtTokenBoundary is `tokenStart.test(text)`: text is empty or ends in a separator.
func endsAtTokenBoundary(text string) bool {
	if text == "" {
		return true
	}
	last, _ := utf8.DecodeLastRuneInString(text)
	return isSeparator(last)
}

// CompletionItem is one suggestion: the text to insert, what to show, and an optional description.
type CompletionItem struct {
	Value       string
	Label       string
	Description string
}

// DefaultMentionName is the agent name `isMentionToken` assumes when none is given.
const DefaultMentionName = "Pi"

// IsMentionToken reports whether at is chat mode's agent mention as a whole token (`@pi`, or the agent's name in the
// conversation); it is never completed as a file. Pass DefaultMentionName when the conversation names no agent.
func IsMentionToken(at, name string) bool {
	return jsstring.ToLower(at) == "@"+jsstring.ToLower(name)
}

// MentionItems is the mention offered first while chat mode is on (name: the agent's name there, "" when chat mode is
// off), when the typed prefix starts it.
func MentionItems(rawPrefix, name string) []CompletionItem {
	if name == "" {
		return nil
	}
	token := jsstring.ToLower(name)
	if !strings.HasPrefix(token, jsstring.ToLower(rawPrefix)) {
		return nil
	}
	return []CompletionItem{{Value: "@" + token, Label: "@" + token, Description: "mention " + name}}
}

// findLastDelimiter is the offset of the last byte of the last path delimiter or separator in text, or -1.
func findLastDelimiter(text string) int {
	last := -1
	for i, r := range text {
		if (r < utf8.RuneSelf && strings.IndexByte(pathDelimiters, byte(r)) >= 0) || isSeparator(r) {
			last = i + utf8.RuneLen(r) - 1
		}
	}
	return last
}

// stripLeadingWrappers: "(~/Dev" -> "~/Dev", "`src/ma" -> "src/ma"; a wrapper whose closer is in the token stays
// ("app/[slug]/pa").
func stripLeadingWrappers(token string) string {
	result := token
	for result != "" {
		closer := pathWrapper(result[0])
		if closer == 0 || strings.IndexByte(result[1:], closer) >= 0 {
			break
		}
		result = result[1:]
	}
	return result
}

// findUnclosedQuoteStart is the offset of the opening `"` that is still unclosed, or -1 when quotes balance.
func findUnclosedQuoteStart(text string) int {
	inQuotes := false
	start := -1
	for i := range len(text) {
		if text[i] == '"' {
			inQuotes = !inQuotes
			if inQuotes {
				start = i
			}
		}
	}
	if inQuotes {
		return start
	}
	return -1
}

// isTokenStart reports whether index starts a token: at the beginning or after a delimiter or separator, looking past
// opening wrappers such as `(` or a backtick.
func isTokenStart(text string, index int) bool {
	start := index
	for start > 0 && pathWrapper(text[start-1]) != 0 {
		start--
	}
	if start == 0 {
		return true
	}
	if strings.IndexByte(pathDelimiters, text[start-1]) >= 0 {
		return true
	}
	return endsAtTokenBoundary(text[:start])
}

// extractQuotedPrefix is the text from an unclosed quote (and the `@` before it) to the end, when the quote starts a
// token.
func extractQuotedPrefix(text string) (string, bool) {
	quoteStart := findUnclosedQuoteStart(text)
	if quoteStart < 0 {
		return "", false
	}
	if quoteStart > 0 && text[quoteStart-1] == '@' {
		if !isTokenStart(text, quoteStart-1) {
			return "", false
		}
		return text[quoteStart-1:], true
	}
	if !isTokenStart(text, quoteStart) {
		return "", false
	}
	return text[quoteStart:], true
}

// PathPrefix is a typed prefix split into its path and how it was written.
type PathPrefix struct {
	// RawPrefix is the path without the `@` and the opening quote.
	RawPrefix      string
	IsAtPrefix     bool
	IsQuotedPrefix bool
}

// ParsePathPrefix splits `@"x`, `"x`, `@x` and `x`.
func ParsePathPrefix(prefix string) PathPrefix {
	switch {
	case strings.HasPrefix(prefix, `@"`):
		return PathPrefix{prefix[2:], true, true}
	case strings.HasPrefix(prefix, `"`):
		return PathPrefix{prefix[1:], false, true}
	case strings.HasPrefix(prefix, "@"):
		return PathPrefix{prefix[1:], true, false}
	}
	return PathPrefix{prefix, false, false}
}

// CompletionValueOptions says how a completed path is written.
type CompletionValueOptions struct {
	IsDirectory    bool
	IsAtPrefix     bool
	IsQuotedPrefix bool
}

// BuildCompletionValue is the text inserted for path: quoted when the typed prefix was or the path has a separator.
func BuildCompletionValue(path string, options CompletionValueOptions) string {
	needsQuotes := options.IsQuotedPrefix || strings.ContainsFunc(path, isSeparator)
	prefix := ""
	if options.IsAtPrefix {
		prefix = "@"
	}
	if !needsQuotes {
		return prefix + path
	}
	return prefix + `"` + path + `"`
}

// ExtractAtPrefix is the `@` token before the cursor (`@src/ma`, `@"my file`), or false.
func ExtractAtPrefix(text string) (string, bool) {
	if quoted, ok := extractQuotedPrefix(text); ok && strings.HasPrefix(quoted, `@"`) {
		return quoted, true
	}
	token := text
	if last := findLastDelimiter(text); last != -1 {
		token = text[last+1:]
	}
	if token = stripLeadingWrappers(token); strings.HasPrefix(token, "@") {
		return token, true
	}
	return "", false
}

// ExtractPathPrefix is a path-like token before the cursor; with force (Tab) any token, else only path-looking ones.
func ExtractPathPrefix(text string, force bool) (string, bool) {
	if quoted, ok := extractQuotedPrefix(text); ok {
		return quoted, true
	}
	prefix := text
	if last := findLastDelimiter(text); last != -1 {
		prefix = text[last+1:]
	}
	prefix = stripLeadingWrappers(prefix)
	if force {
		return prefix, true
	}
	if strings.Contains(prefix, "/") || strings.HasPrefix(prefix, ".") || strings.HasPrefix(prefix, "~/") {
		return prefix, true
	}
	if prefix == "" && text != "" && endsAtTokenBoundary(text) {
		return prefix, true
	}
	return "", false
}

// ShouldTriggerFileCompletion is pi-tui's gate: forced Tab completes files unless a slash command name is being typed.
func ShouldTriggerFileCompletion(textBeforeCursor string) bool {
	trimmed := jsTrim(textBeforeCursor)
	return !(strings.HasPrefix(trimmed, "/") && !strings.Contains(trimmed, " "))
}

// OutOfScope reports paths that leave the session directory and so complete nothing (cwd-relative only): absolute,
// `~` and `..`.
func OutOfScope(rawPrefix string) bool {
	if strings.HasPrefix(rawPrefix, "/") || strings.HasPrefix(rawPrefix, "~") {
		return true
	}
	rest, ok := strings.CutPrefix(rawPrefix, "..")
	return ok && (rest == "" || rest[0] == '/')
}

// IndexedPath is one result of the file index: a path below the session directory and whether it is a directory.
type IndexedPath struct {
	Path string
	Dir  bool
}

// AtItems are the items for `@` completion (pi's `getFuzzyFileSuggestions` labels: the name, the path as description).
func AtItems(results []IndexedPath, isQuotedPrefix bool) []CompletionItem {
	items := make([]CompletionItem, len(results))
	for i, result := range results {
		name := result.Path[strings.LastIndexByte(result.Path, '/')+1:]
		path, label := result.Path, name
		if result.Dir {
			path, label = path+"/", name+"/"
		}
		items[i] = CompletionItem{
			Value:       BuildCompletionValue(path, CompletionValueOptions{IsDirectory: result.Dir, IsAtPrefix: true, IsQuotedPrefix: isQuotedPrefix}),
			Label:       label,
			Description: result.Path,
		}
	}
	return items
}

// PathCompletionParts is what Tab path completion lists: the directory ("" for the root), the typed part of the last
// segment, and the text shown before the name.
type PathCompletionParts struct {
	Dir, Name, Display string
}

// PathCompletion splits rawPrefix for listing one directory (pi-tui `getFileSuggestions`, cwd-relative).
func PathCompletion(rawPrefix string) PathCompletionParts {
	slash := strings.LastIndexByte(rawPrefix, '/')
	display, name := "", rawPrefix
	if slash == -1 {
		if strings.HasPrefix(rawPrefix, "./") {
			display = "./"
		}
	} else {
		display, name = rawPrefix[:slash+1], rawPrefix[slash+1:]
	}
	dir := strings.TrimRight(strings.TrimPrefix(display, "./"), "/")
	return PathCompletionParts{Dir: dir, Name: name, Display: display}
}

// DirectoryEntry is one entry of a directory listing.
type DirectoryEntry struct {
	Name string
	Dir  bool
}

// PathItems are the Tab items over one directory's entries: those starting with the typed name, directories first,
// then by name.
func PathItems(entries []DirectoryEntry, rawPrefix string, isAtPrefix, isQuotedPrefix bool) []CompletionItem {
	parts := PathCompletion(rawPrefix)
	lower := jsstring.ToLower(parts.Name)
	var matched []DirectoryEntry
	for _, entry := range entries {
		if strings.HasPrefix(jsstring.ToLower(entry.Name), lower) {
			matched = append(matched, entry)
		}
	}
	slices.SortStableFunc(matched, func(a, b DirectoryEntry) int {
		if a.Dir != b.Dir {
			if a.Dir {
				return -1
			}
			return 1
		}
		return localecompare.Compare(a.Name, b.Name)
	})
	items := make([]CompletionItem, len(matched))
	for i, entry := range matched {
		path, label := parts.Display+entry.Name, entry.Name
		if entry.Dir {
			path, label = path+"/", label+"/"
		}
		items[i] = CompletionItem{
			Value: BuildCompletionValue(path, CompletionValueOptions{IsDirectory: entry.Dir, IsAtPrefix: isAtPrefix, IsQuotedPrefix: isQuotedPrefix}),
			Label: label,
		}
	}
	return items
}

// ApplyFileCompletion is pi-tui `applyCompletion` for file items: it replaces prefix before the cursor with the item's
// value; files get a trailing space after `@` items, directories none (completion continues), a closing quote is not
// doubled. cursorCol and the result's column are byte offsets.
func ApplyFileCompletion(line string, cursorCol int, item CompletionItem, prefix string) (string, int) {
	cursorCol = min(max(cursorCol, 0), len(line))
	// String.prototype.slice counts a negative end from the end of the line.
	end := cursorCol - len(prefix)
	if end < 0 {
		end = max(len(line)+end, 0)
	}
	before, after := line[:end], line[cursorCol:]
	quoted := strings.HasPrefix(prefix, `"`) || strings.HasPrefix(prefix, `@"`)
	if quoted && strings.HasSuffix(item.Value, `"`) && strings.HasPrefix(after, `"`) {
		after = after[1:]
	}
	isDirectory := strings.HasSuffix(item.Label, "/")
	suffix := ""
	if strings.HasPrefix(prefix, "@") && !isDirectory {
		suffix = " "
	}
	offset := len(item.Value)
	if isDirectory && strings.HasSuffix(item.Value, `"`) {
		offset--
	}
	return before + item.Value + suffix + after, len(before) + offset + len(suffix)
}
