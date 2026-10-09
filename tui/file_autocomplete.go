package tui

// Attachment autocomplete uses fd for fuzzy search. Direct path completion uses os.ReadDir.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/nodespawn"
	"github.com/MichaelKinsy/PiG/tui/widthx"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// pathDelimiters defines characters that separate path tokens in the
// editor buffer. Matches upstream PATH_DELIMITERS (autocomplete.ts:7).
var pathDelimiters = map[byte]bool{
	' ':  true,
	'\t': true,
	'"':  true,
	'\'': true,
	'=':  true,
}

// pathWrappers maps the opening wrappers that may precede a path in prose to their closing counterpart (autocomplete.ts:11, PATH_WRAPPERS).
var pathWrappers = map[byte]byte{'(': ')', '[': ']', '{': '}', '<': '>', '`': '`'}

// stripLeadingWrappers strips opening wrappers before a path, e.g. "(~/Dev" becomes "~/Dev" and "`src/ma" becomes "src/ma". A wrapper stays if the token also contains its closer, e.g. "app/[slug]/pa" or "(group)/pa". Mirrors autocomplete.ts:61 stripLeadingWrappers.
func stripLeadingWrappers(token string) string {
	result := token
	for len(result) > 0 {
		closer, ok := pathWrappers[result[0]]
		if !ok || strings.IndexByte(result[1:], closer) >= 0 {
			break
		}
		result = result[1:]
	}
	return result
}

// CombinedProvider handles both slash-command and @-file autocomplete.
// Mirrors upstream CombinedAutocompleteProvider (autocomplete.ts:238).
type CombinedProvider struct {
	slash   *SlashOnlyProvider
	baseDir string // project working directory
	fdPath  string // path to fd binary ("" if not available)

	// asyncFileSearch defers the @-file fd subprocess off the synchronous
	// GetSuggestions path. Upstream runs getFuzzyFileSuggestions async with
	// an AbortSignal (autocomplete.ts:717); a deep `--follow --hidden` tree
	// walk (e.g. @~/<rare-name>) can take many seconds, so running it on the
	// input thread freezes typing. When enabled, GetSuggestions returns nil
	// for @-fd queries and the editor runs FileSearchTask off-thread with
	// cancellation. Off by default so direct/sync callers (tests, print mode)
	// keep the synchronous contract.
	asyncFileSearch bool
}

// SetAsyncFileSearch enables deferred, cancellable fd-backed @-file search.
// Interactive mode enables this so a slow tree walk cannot block keystrokes.
func (p *CombinedProvider) SetAsyncFileSearch(v bool) { p.asyncFileSearch = v }

// NewCombinedProvider creates a provider for slash commands, attachment search, and direct paths. An empty fdPath disables attachment suggestions without affecting direct path completion.
func NewCombinedProvider(cmds []SlashCommand, baseDir, fdPath string) *CombinedProvider {
	return &CombinedProvider{
		slash:   NewSlashOnlyProvider(cmds),
		baseDir: baseDir,
		fdPath:  fdPath,
	}
}

// GetSuggestions implements upstream command, attachment and path matching. The Editor decides which contexts trigger queries automatically.
//
// options.Force is upstream's {force: true}: Tab outside a slash-command-name context forces naked path completion even when the prefix does not look path-like yet. The signal is ctx; the file search that honors it runs through FileSearchTask.
func (p *CombinedProvider) GetSuggestions(ctx context.Context, lines []string, cursorLine, cursorCol int, options AutocompleteSuggestionOptions) *AutocompleteSuggestions {
	return p.getSuggestions(ctx, lines, cursorLine, cursorCol, options.Force)
}

// ShouldTriggerFileCompletion reports whether Tab may force file completion at the cursor: it may not while the text before the cursor is a slash-command name (a trimmed leading "/" with no space), where the command list answers instead (upstream autocomplete.ts shouldTriggerFileCompletion). cursorCol is a byte offset into the line.
func (p *CombinedProvider) ShouldTriggerFileCompletion(lines []string, cursorLine, cursorCol int) bool {
	var line string
	if cursorLine >= 0 && cursorLine < len(lines) {
		line = lines[cursorLine]
	}
	before := line[:max(0, min(cursorCol, len(line)))]
	trimmed := widthx.JSTrim(before)
	return !strings.HasPrefix(trimmed, "/") || strings.Contains(trimmed, " ")
}

func (p *CombinedProvider) getSuggestions(ctx context.Context, lines []string, cursorLine, cursorCol int, force bool) *AutocompleteSuggestions {
	if cursorLine < 0 || cursorLine >= len(lines) {
		return nil
	}
	line := lines[cursorLine]
	if cursorCol > len(line) {
		cursorCol = len(line)
	}
	before := line[:cursorCol]

	// Check for @ prefix first.
	atPrefix := extractAtPrefix(before)
	if atPrefix != "" {
		if p.asyncFileSearch && p.fdPath != "" {
			// The editor runs the fd subprocess off the input thread via
			// FileSearchTask so a slow tree walk can't block typing.
			return nil
		}
		raw, isQuoted := parseAtPrefix(atPrefix)
		items := p.getFileSuggestions(raw, isQuoted)
		if len(items) == 0 {
			return nil
		}
		return &AutocompleteSuggestions{Items: items, Prefix: atPrefix}
	}

	// Slash-command branch, after leading whitespace (autocomplete.ts:338-339).
	if !force && strings.HasPrefix(widthx.JSTrimStart(before), "/") {
		return p.slash.GetSuggestions(ctx, lines, cursorLine, cursorCol, AutocompleteSuggestionOptions{})
	}

	// Mirrors upstream autocomplete.ts:358 + editor.ts forceFileAutocomplete.
	pathPrefix, ok := extractPathPrefix(before, force)
	if !ok {
		return nil
	}
	raw, isQuoted := parsePathOnlyPrefix(pathPrefix)
	items := p.readdirFileSuggestions(raw, isQuoted)
	if len(items) == 0 {
		return nil
	}
	// Strip the leading "@" buildCompletionValue added so naked path
	// completions don't accidentally turn a `./` into `@./`.
	for i := range items {
		items[i].Value = strings.TrimPrefix(items[i].Value, "@")
		items[i].Description = ""
	}
	return &AutocompleteSuggestions{Items: items, Prefix: pathPrefix}
}

// SuggestionTask defers filesystem access and awaited command arguments while preserving the combined provider's branch precedence.
func (p *CombinedProvider) SuggestionTask(lines []string, line, col int, force bool) (string, func(context.Context) ([]AutocompleteItem, error), bool) {
	if line < 0 || line >= len(lines) || col < 0 {
		return "", nil, false
	}
	before := lines[line][:min(col, len(lines[line]))]
	prefix := extractAtPrefix(before)
	pathPrefix, pathMatch := extractPathPrefix(before, force)
	if prefix != "" || force || (pathMatch && !strings.HasPrefix(widthx.JSTrimStart(before), "/")) {
		lines = slices.Clone(lines)
		if prefix == "" {
			if !pathMatch {
				return "", nil, false
			}
			prefix = pathPrefix
		}
		return prefix, func(ctx context.Context) ([]AutocompleteItem, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if _, run, ok := p.FileSearchTask(lines, line, col); ok {
				return run(ctx), ctx.Err()
			}
			local := *p
			local.asyncFileSearch = false
			result := local.getSuggestions(ctx, lines, line, col, force)
			if result == nil {
				return nil, ctx.Err()
			}
			return result.Items, ctx.Err()
		}, true
	}
	return p.slash.SuggestionTask(lines, line, col, force)
}

// ApplyCompletion preserves path prefixes and surrounding text, consumes an existing closing quote, and leaves directory cursors inside quotes. Only slash commands and file attachments append a space.
func (p *CombinedProvider) ApplyCompletion(lines []string, cursorLine, cursorCol int, item AutocompleteItem, prefix string) ([]string, int, int) {
	if cursorLine < 0 || cursorLine >= len(lines) {
		return lines, cursorLine, cursorCol
	}
	line := lines[cursorLine]
	if cursorCol > len(line) {
		cursorCol = len(line)
	}
	if len(prefix) > cursorCol {
		return lines, cursorLine, cursorCol
	}
	beforePrefix, afterCursor := line[:cursorCol-len(prefix)], line[cursorCol:]
	isQuoted := strings.HasPrefix(prefix, `"`) || strings.HasPrefix(prefix, `@"`)
	if isQuoted && strings.HasSuffix(item.Value, `"`) && strings.HasPrefix(afterCursor, `"`) {
		afterCursor = afterCursor[1:]
	}
	out := slices.Clone(lines)
	if strings.HasPrefix(prefix, "/") && widthx.JSTrim(beforePrefix) == "" && !strings.Contains(prefix[1:], "/") {
		out[cursorLine] = beforePrefix + "/" + item.Value + " " + afterCursor
		return out, cursorLine, len(beforePrefix) + len(item.Value) + 2
	}
	isDirectory := strings.HasSuffix(item.Label, "/")
	suffix := ""
	if strings.HasPrefix(prefix, "@") && !isDirectory {
		suffix = " "
	}
	out[cursorLine] = beforePrefix + item.Value + suffix + afterCursor
	cursorOffset := len(item.Value)
	if isDirectory && strings.HasSuffix(item.Value, `"`) {
		cursorOffset--
	}
	return out, cursorLine, len(beforePrefix) + cursorOffset + len(suffix)
}

// extractQuotedPrefix returns the text starting at the last unclosed " in
// text, or "" if all quotes are balanced. Mirrors upstream
// extractQuotedPrefix (autocomplete.ts:74-93).
func extractQuotedPrefix(text string) string {
	q := findUnclosedQuoteStart(text)
	if q < 0 {
		return ""
	}
	// Include a preceding @ if it makes the token an @-quoted prefix.
	if q > 0 && text[q-1] == '@' && isTokenStart(text, q-1) {
		return text[q-1:]
	}
	if isTokenStart(text, q) {
		return text[q:]
	}
	return ""
}

// extractPathPrefix extracts a path-like prefix from text-before-cursor.
// Mirrors upstream extractPathPrefix (autocomplete.ts:477-503).
//
//   - quoted prefix wins if present
//   - else the token after the last delimiter
//   - in force mode (Tab), always returns the token
//   - in non-force mode, only returns when the token contains "/", starts
//     with "." or "~/", or is empty and text ends with a space
func extractPathPrefix(text string, force bool) (string, bool) {
	if q := extractQuotedPrefix(text); q != "" && !strings.HasPrefix(q, "@") {
		return q, true
	}
	lastDelim := findLastDelimiter(text)
	prefix := text
	if lastDelim >= 0 {
		prefix = text[lastDelim+1:]
	}
	prefix = stripLeadingWrappers(prefix)
	if force {
		return prefix, true
	}
	if strings.Contains(prefix, "/") ||
		strings.HasPrefix(prefix, ".") ||
		strings.HasPrefix(prefix, "~/") {
		return prefix, true
	}
	if prefix == "" && text != "" && endsAtTokenBoundary(text) {
		return prefix, true
	}
	return "", false
}

// parsePathOnlyPrefix returns the raw query path and quote state for a
// non-@ path prefix. Mirrors upstream parsePathPrefix for the non-@
// branches (autocomplete.ts:94-107).
func parsePathOnlyPrefix(prefix string) (raw string, isQuoted bool) {
	if strings.HasPrefix(prefix, `"`) {
		return prefix[1:], true
	}
	return prefix, false
}

// extractAtPrefix extracts the @-prefixed token from text-before-cursor.
// Returns "" if no @ token is found. Handles both @path and @"path with
// spaces". Mirrors upstream extractAtPrefix (autocomplete.ts:460-475).
func extractAtPrefix(text string) string {
	if quoted := extractQuotedPrefix(text); strings.HasPrefix(quoted, `@"`) {
		return quoted
	}

	// Unquoted: find the last delimiter, then check whether an @ token follows any opening wrappers.
	lastDelim := findLastDelimiter(text)
	token := text
	if lastDelim >= 0 {
		token = text[lastDelim+1:]
	}
	if token = stripLeadingWrappers(token); strings.HasPrefix(token, "@") {
		return token
	}
	return ""
}

// findLastDelimiter returns the final byte of the last path separator, or -1. Provider columns count bytes, so callers add one to find the following token.
func findLastDelimiter(text string) int {
	last := -1
	for i, r := range text {
		if (r < utf8.RuneSelf && pathDelimiters[byte(r)]) || autocompleteSeparator(r) {
			last = i + utf8.RuneLen(r) - 1
		}
	}
	return last
}

// findUnclosedQuoteStart returns the index of the opening " that is still
// unclosed, or -1 if all quotes are balanced.
// Mirrors upstream findUnclosedQuoteStart (autocomplete.ts:57).
func findUnclosedQuoteStart(text string) int {
	inQuotes := false
	quoteStart := -1
	for i := range len(text) {
		if text[i] == '"' {
			inQuotes = !inQuotes
			if inQuotes {
				quoteStart = i
			}
		}
	}
	if inQuotes {
		return quoteStart
	}
	return -1
}

// isTokenStart returns true if index is at the start of a token
// (index 0 or preceded by a delimiter, ignoring opening wrappers such as "(" or a backtick).
// Mirrors upstream isTokenStart (autocomplete.ts:70).
func isTokenStart(text string, index int) bool {
	start := index
	for start > 0 && pathWrappers[text[start-1]] != 0 {
		start--
	}
	if start == 0 {
		return true
	}
	previous, _ := utf8.DecodeLastRuneInString(text[:start])
	return (previous < utf8.RuneSelf && pathDelimiters[byte(previous)]) || autocompleteSeparator(previous)
}

// endsAtTokenBoundary reports whether text is empty or ends in a whitespace or CJK punctuation separator, upstream's tokenStartRegex.
func endsAtTokenBoundary(text string) bool {
	if text == "" {
		return true
	}
	last, _ := utf8.DecodeLastRuneInString(text)
	return autocompleteSeparator(last)
}

// parseAtPrefix extracts the raw path and quote state from an @ prefix.
// Returns (rawPrefix, isQuoted). Mirrors upstream parsePathPrefix for
// the @ cases (autocomplete.ts:93-107).
func parseAtPrefix(prefix string) (raw string, isQuoted bool) {
	if strings.HasPrefix(prefix, `@"`) {
		return prefix[2:], true
	}
	if strings.HasPrefix(prefix, "@") {
		return prefix[1:], false
	}
	return prefix, false
}

// buildCompletionValue builds the value string for an @ completion.
// Handles quoting for paths with spaces. Mirrors upstream
// buildCompletionValue (autocomplete.ts:109-122).
func buildCompletionValue(path string, isDirectory, isQuoted bool) string {
	needsQuotes := isQuoted || strings.ContainsFunc(path, autocompleteSeparator)
	if !needsQuotes {
		return "@" + path
	}
	if isDirectory {
		return `@"` + path + `"`
	}
	return `@"` + path + `"`
}

// getFileSuggestions returns fd-backed attachment suggestions, or none when fd is unavailable.
func (p *CombinedProvider) getFileSuggestions(rawQuery string, isQuoted bool) []AutocompleteItem {
	if p.fdPath == "" {
		return nil
	}
	return p.fdFileSuggestions(rawQuery, isQuoted)
}

// FileSearchTask returns a deferred, cancellable fd-backed @-file search
// for the cursor's @-prefix, or ok=false when async file search is disabled,
// fd is unavailable, or the buffer-before-cursor is not an @ query. The
// returned run closure executes fd under the caller's context so the editor
// can abort it on the next keystroke, mirroring upstream's async
// getFuzzyFileSuggestions + AbortSignal (autocomplete.ts:717).
func (p *CombinedProvider) FileSearchTask(lines []string, cursorLine, cursorCol int) (string, func(context.Context) []AutocompleteItem, bool) {
	if !p.asyncFileSearch || p.fdPath == "" {
		return "", nil, false
	}
	if cursorLine < 0 || cursorLine >= len(lines) {
		return "", nil, false
	}
	line := lines[cursorLine]
	if cursorCol > len(line) {
		cursorCol = len(line)
	}
	atPrefix := extractAtPrefix(line[:cursorCol])
	if atPrefix == "" {
		return "", nil, false
	}
	raw, isQuoted := parseAtPrefix(atPrefix)
	run := func(ctx context.Context) []AutocompleteItem {
		return p.fdFileSuggestionsCtx(ctx, raw, isQuoted)
	}
	return atPrefix, run, true
}

// fdSearchEntry is one path returned by fd. Directory paths include fd's
// trailing separator in path, matching walkDirectoryWithFd upstream.
type fdSearchEntry struct {
	path        string
	isDirectory bool
}

// fdFileSuggestions uses fd for file search. For queries without `/`,
// runs a fuzzy search across the whole project tree. For queries with
// `/`, scopes to the resolved base directory. Each fuzzy search first queries
// direct children and then the recursive tree, so recursive result limits
// cannot hide a matching child of the selected directory.
// Mirrors upstream getBaseDirSuggestions + getFuzzyFileSuggestions
// (autocomplete.ts:733-809).
func (p *CombinedProvider) fdFileSuggestions(rawQuery string, isQuoted bool) []AutocompleteItem {
	return p.fdFileSuggestionsCtx(context.Background(), rawQuery, isQuoted)
}

// fdFileSuggestionsCtx runs the fd-backed search under the caller's context.
// The interactive editor cancels this context on the next keystroke, matching
// upstream's abort-only lifetime without imposing an additional timeout.
func (p *CombinedProvider) fdFileSuggestionsCtx(ctx context.Context, rawQuery string, isQuoted bool) []AutocompleteItem {
	query := toDisplayPath(rawQuery)

	var baseDir, fdQuery, displayBase string

	if strings.Contains(query, "/") {
		lastSlash := strings.LastIndex(query, "/")
		displayBase = query[:lastSlash+1]
		fdQuery = query[lastSlash+1:]

		switch {
		case strings.HasPrefix(displayBase, "~/"):
			home, _ := os.UserHomeDir()
			baseDir = filepath.Join(home, displayBase[2:])
		case strings.HasPrefix(displayBase, "/"):
			baseDir = displayBase
		default:
			baseDir = filepath.Join(p.baseDir, displayBase)
		}

		info, err := os.Stat(baseDir)
		if err != nil || !info.IsDir() {
			// A slash in the query can identify path segments anywhere in the
			// project rather than an existing directory relative to baseDir.
			// Upstream falls back to a root --full-path search in that case.
			baseDir = p.baseDir
			fdQuery = query
			displayBase = ""
		}
	} else {
		baseDir = p.baseDir
		fdQuery = query
	}

	baseDirEntries := p.walkDirectoryWithFd(ctx, baseDir, fdQuery, 1)
	recursiveEntries := p.walkDirectoryWithFd(ctx, baseDir, fdQuery, 0)
	if ctx.Err() != nil {
		return nil
	}
	seenPaths := make(map[string]struct{}, len(baseDirEntries)+len(recursiveEntries))
	entries := make([]fdSearchEntry, 0, len(baseDirEntries)+len(recursiveEntries))
	for _, entry := range baseDirEntries {
		seenPaths[entry.path] = struct{}{}
		entries = append(entries, entry)
	}
	for _, entry := range recursiveEntries {
		if _, ok := seenPaths[entry.path]; ok {
			continue
		}
		seenPaths[entry.path] = struct{}{}
		entries = append(entries, entry)
	}

	type scoredEntry struct {
		fdSearchEntry
		score int
	}
	scoredEntries := make([]scoredEntry, 0, len(entries))
	for _, entry := range entries {
		score := 1
		if fdQuery != "" {
			score = scoreEntry(entry.path, fdQuery, entry.isDirectory)
		}
		if score > 0 {
			scoredEntries = append(scoredEntries, scoredEntry{fdSearchEntry: entry, score: score})
		}
	}

	pathCollator := collate.New(language.Und)
	slices.SortFunc(scoredEntries, func(a, b scoredEntry) int {
		if a.score != b.score {
			return b.score - a.score
		}
		if depthDiff := pathDepth(a.path) - pathDepth(b.path); depthDiff != 0 {
			return depthDiff
		}
		if lengthDiff := utf16Length(a.path) - utf16Length(b.path); lengthDiff != 0 {
			return lengthDiff
		}
		return pathCollator.CompareString(a.path, b.path)
	})
	if len(scoredEntries) > 20 {
		scoredEntries = scoredEntries[:20]
	}

	items := make([]AutocompleteItem, 0, len(scoredEntries))
	for _, entry := range scoredEntries {
		pathWithoutSlash := entry.path
		if entry.isDirectory {
			pathWithoutSlash = strings.TrimSuffix(pathWithoutSlash, "/")
		}
		displayPath := pathWithoutSlash
		if displayBase != "" {
			displayPath = displayBase + pathWithoutSlash
		}
		entryName := filepath.Base(pathWithoutSlash)
		label := entryName
		completionPath := displayPath
		if entry.isDirectory {
			label += "/"
			completionPath += "/"
		}
		items = append(items, AutocompleteItem{
			Value:       buildCompletionValue(completionPath, entry.isDirectory, isQuoted),
			Label:       label,
			Description: displayPath,
		})
	}
	return items
}

func (p *CombinedProvider) walkDirectoryWithFd(ctx context.Context, baseDir, query string, maxDepth int) []fdSearchEntry {
	if ctx.Err() != nil {
		return nil
	}
	args := []string{
		"--base-directory", baseDir,
		"--max-results", "100",
		"--type", "f",
		"--type", "d",
		"--follow",
		"--hidden",
		"--exclude", ".git",
		"--exclude", ".git/*",
		"--exclude", ".git/**",
	}
	if maxDepth > 0 {
		args = append(args, "--max-depth", "1")
	}
	if strings.Contains(toDisplayPath(query), "/") {
		args = append(args, "--full-path")
	}
	if query != "" {
		args = append(args, buildFdPathQuery(query))
	}

	cmd := exec.CommandContext(ctx, p.fdPath, args...)
	// Upstream's spawn finds fd with libuv's search on Windows.
	nodespawn.SetProgram(cmd)
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	entries := make([]fdSearchEntry, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		display := toDisplayPath(line)
		hasTrailingSlash := strings.HasSuffix(display, "/")
		normalized := strings.TrimSuffix(display, "/")
		if normalized == ".git" || strings.HasPrefix(normalized, ".git/") || strings.Contains(normalized, "/.git/") {
			continue
		}
		entries = append(entries, fdSearchEntry{path: display, isDirectory: hasTrailingSlash})
	}
	return entries
}

func pathDepth(path string) int {
	depth := 0
	inSegment := false
	for _, r := range toDisplayPath(path) {
		if r == '/' {
			inSegment = false
			continue
		}
		if !inSegment {
			depth++
			inSegment = true
		}
	}
	return depth
}

func utf16Length(s string) int {
	length := 0
	for _, r := range s {
		length++
		if r > 0xffff {
			length++
		}
	}
	return length
}

// readdirFileSuggestions lists direct path matches, including .git, with directories first and request-owned collation state.
func (p *CombinedProvider) readdirFileSuggestions(rawQuery string, isQuoted bool) []AutocompleteItem {
	query := toDisplayPath(rawQuery)

	var searchDir, searchPrefix, displayBase string

	switch {
	case query == "" || query == "./" || query == "../" || query == "~/" || query == "/":
		// Root-level: list the target directory.
		displayBase = query
		switch {
		case strings.HasPrefix(query, "~/"):
			home, _ := os.UserHomeDir()
			searchDir = home
		case query == "/":
			searchDir = "/"
		default:
			searchDir = filepath.Join(p.baseDir, query)
		}
		searchPrefix = ""
	case strings.HasSuffix(query, "/"):
		// Dir ending with /: list contents.
		displayBase = query
		switch {
		case strings.HasPrefix(query, "~/"):
			home, _ := os.UserHomeDir()
			searchDir = filepath.Join(home, query[2:])
		case strings.HasPrefix(query, "/"):
			searchDir = query
		default:
			searchDir = filepath.Join(p.baseDir, query)
		}
		searchPrefix = ""
	default:
		// Split into dir + prefix.
		dir := filepath.Dir(query)
		searchPrefix = filepath.Base(query)
		displayBase = ""
		switch {
		case dir != ".":
			displayBase = toDisplayPath(dir) + "/"
		case strings.HasPrefix(query, "./"):
			displayBase = "./"
		case strings.HasPrefix(query, "../"):
			displayBase = "../"
		}
		if strings.HasPrefix(query, "./") && !strings.HasPrefix(displayBase, "./") {
			displayBase = "./" + displayBase
		}
		switch {
		case strings.HasPrefix(query, "~/"):
			home, _ := os.UserHomeDir()
			if dir == "~" {
				searchDir = home
			} else {
				searchDir = filepath.Join(home, dir[2:])
			}
		case strings.HasPrefix(query, "/"):
			searchDir = dir
		default:
			searchDir = filepath.Join(p.baseDir, dir)
		}
	}

	dirEntries, err := os.ReadDir(searchDir)
	if err != nil {
		return nil
	}

	items := make([]AutocompleteItem, 0, len(dirEntries))
	lowerPrefix := strings.ToLower(searchPrefix)

	for _, de := range dirEntries {
		name := de.Name()
		if lowerPrefix != "" && !strings.HasPrefix(strings.ToLower(name), lowerPrefix) {
			continue
		}
		isDir := de.IsDir()
		if de.Type()&os.ModeSymlink != 0 {
			// Resolve symlinks.
			info, err := os.Stat(filepath.Join(searchDir, name))
			if err == nil {
				isDir = info.IsDir()
			}
		}

		relPath := name
		if displayBase != "" {
			relPath = displayBase + name
		}
		relPath = toDisplayPath(relPath)

		completionPath := relPath
		label := name
		if isDir {
			completionPath += "/"
			label += "/"
		}
		value := buildCompletionValue(completionPath, isDir, isQuoted)

		items = append(items, AutocompleteItem{
			Value: value,
			Label: label,
		})
	}

	// Directories first, then alphabetical. Mirrors upstream sort
	// (autocomplete.ts:607-614).
	autocompleteCollator := collate.New(language.Und)
	slices.SortFunc(items, func(a, b AutocompleteItem) int {
		aDir := strings.HasSuffix(a.Label, "/")
		bDir := strings.HasSuffix(b.Label, "/")
		if aDir && !bDir {
			return -1
		}
		if !aDir && bDir {
			return 1
		}
		return autocompleteCollator.CompareString(a.Label, b.Label)
	})
	return items
}

// toDisplayPath normalizes backslashes to forward slashes.
// Mirrors upstream toDisplayPath (autocomplete.ts:9).
func toDisplayPath(path string) string {
	return strings.ReplaceAll(path, "\\", "/")
}

// buildFdPathQuery converts a query with / separators into an fd-compatible
// regex pattern. Mirrors upstream buildFdPathQuery (autocomplete.ts:17-43).
func buildFdPathQuery(query string) string {
	normalized := toDisplayPath(query)
	if !strings.Contains(normalized, "/") {
		return normalized
	}
	hasTrailingSep := strings.HasSuffix(normalized, "/")
	trimmed := strings.Trim(normalized, "/")
	if trimmed == "" {
		return normalized
	}
	sepPattern := `[\\/]`
	segments := strings.Split(trimmed, "/")
	filtered := make([]string, 0, len(segments))
	for _, s := range segments {
		if s != "" {
			filtered = append(filtered, escapeRegex(s))
		}
	}
	if len(filtered) == 0 {
		return normalized
	}
	pattern := strings.Join(filtered, sepPattern)
	if hasTrailingSep {
		pattern += sepPattern
	}
	return pattern
}

// escapeRegex escapes regex metacharacters in a string.
// Mirrors upstream escapeRegex (autocomplete.ts:13).
func escapeRegex(s string) string {
	special := `.*+?^${}()|[]\`
	var b strings.Builder
	b.Grow(len(s))
	for _, c := range s {
		if strings.ContainsRune(special, c) {
			b.WriteByte('\\')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// scoreEntry scores a file path against the query for ranking.
// Higher = better match. Mirrors upstream scoreEntry (autocomplete.ts:621-643).
func scoreEntry(filePath, query string, isDirectory bool) int {
	fileName := filepath.Base(filePath)
	lowerFile := strings.ToLower(fileName)
	lowerQuery := strings.ToLower(query)

	score := 0
	switch {
	case lowerFile == lowerQuery:
		score = 100
	case strings.HasPrefix(lowerFile, lowerQuery):
		score = 80
	case strings.Contains(lowerFile, lowerQuery):
		score = 50
	case strings.Contains(strings.ToLower(filePath), lowerQuery):
		score = 30
	}

	if isDirectory && score > 0 {
		score += 10
	}
	return score
}
