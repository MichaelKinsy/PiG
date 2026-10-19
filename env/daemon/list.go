package daemon

// Ports packages/env/daemon/src/list.rs: `listFiles`, the files and directories below a directory as `fd --hidden
// --exclude .git` lists them without following symbolic links, skipping what git ignores. The payload holds one record per
// entry: a kind byte (`f`, `d` or `l`), the path relative to the root with `/` separators, and a NUL.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	defaultListMaxEntries = 50_000
	defaultListMaxBytes   = 4 * 1024 * 1024
	defaultListTimeoutMs  = 10_000
)

type listOptions struct {
	// hidden includes names starting with `.`, and on Windows hidden files.
	hidden bool
	// gitignore skips what git ignores inside git work trees.
	gitignore  bool
	exclude    []string
	maxEntries uint64
	// maxBytes bounds the listing payload: per entry one kind byte, the path, and a NUL.
	maxBytes uint64
	timeout  time.Duration
}

// parseListOptions is Options::from_json.
func parseListOptions(request Object) (*listOptions, *Failure) {
	count := func(key string, fallback uint64) (uint64, *Failure) {
		value := request[key]
		if value == nil {
			return fallback, nil
		}
		number, ok := unsignedNumber(value)
		if !ok {
			return 0, newFailure("EINVAL", "invalid field "+key)
		}
		return number, nil
	}
	var exclude []string
	switch names := request["exclude"].(type) {
	case nil:
	case []any:
		for _, name := range names {
			text, ok := name.(string)
			if !ok {
				return nil, newFailure("EINVAL", "invalid field exclude")
			}
			exclude = append(exclude, text)
		}
	default:
		return nil, newFailure("EINVAL", "invalid field exclude")
	}
	options := &listOptions{hidden: true, gitignore: true, exclude: exclude}
	if value, ok := request["hidden"].(bool); ok {
		options.hidden = value
	}
	if value, ok := request["gitignore"].(bool); ok {
		options.gitignore = value
	}
	var failure *Failure
	if options.maxEntries, failure = count("maxEntries", defaultListMaxEntries); failure != nil {
		return nil, failure
	}
	if options.maxBytes, failure = count("maxBytes", defaultListMaxBytes); failure != nil {
		return nil, failure
	}
	options.maxBytes = min(options.maxBytes, MaxPayload)
	timeoutMs, failure := count("timeoutMs", defaultListTimeoutMs)
	if failure != nil {
		return nil, failure
	}
	options.timeout = time.Duration(min(timeoutMs, uint64(1<<62)/uint64(time.Millisecond))) * time.Millisecond
	return options, nil
}

// listing is list.rs's Listing.
type listing struct {
	payload   []byte
	count     uint64
	truncated bool
}

// opListFiles is list::run.
func opListFiles(request Object, ctl *control) (Object, []byte, *Failure) {
	root, failure := field(request, "path")
	if failure != nil {
		return nil, nil, failure
	}
	options, failure := parseListOptions(request)
	if failure != nil {
		return nil, nil, failure
	}
	result, failure := listTree(root, options, ctl.aborted.Load)
	if failure != nil {
		return nil, nil, failure
	}
	return Object{"count": float64(result.count), "truncated": result.truncated}, result.payload, nil
}

// lister is one listing in progress.
type lister struct {
	options *listOptions
	aborted func() bool
	started time.Time
	result  listing
	chain   *ignoreChain
	exclude map[string]bool
}

// listTree lists root depth first, siblings in byte order of their names. It stops with truncated at the first entry beyond a
// bound and fails with `aborted` once aborted() is true. Entries that cannot be read are skipped.
func listTree(root string, options *listOptions, aborted func() bool) (*listing, *Failure) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, ioFailure(err, "scandir", root)
	}
	if !info.IsDir() {
		return nil, newFailure("ENOTDIR", fmt.Sprintf("ENOTDIR: not a directory, scandir '%s'", root)).withPath(root)
	}
	lister := &lister{options: options, aborted: aborted, exclude: map[string]bool{}}
	for _, name := range options.exclude {
		lister.exclude[name] = true
	}
	if options.gitignore {
		lister.chain = newIgnoreChain(root)
	}
	lister.started = time.Now()
	// The first event of the walk is the root itself.
	if aborted() {
		return nil, newFailure("aborted", "aborted")
	}
	if _, failure := lister.walk(root, "", ""); failure != nil {
		return nil, failure
	}
	return &lister.result, nil
}

// newIgnoreChain is the ignore matchers of a listing: the root's, its parents' (a work tree above the root counts) and the global ones.
func newIgnoreChain(root string) *ignoreChain {
	chain := &ignoreChain{
		directories: []*directoryIgnore{newDirectoryIgnore(root, "")},
		global:      globalGitignore(),
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return chain
	}
	if canonical, err = filepath.Abs(canonical); err != nil {
		return chain
	}
	relative := ""
	for current := canonical; ; {
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		relative = filepath.Base(current) + "/" + relative
		ignore := newDirectoryIgnore(parent, relative)
		chain.parents = append(chain.parents, ignore)
		chain.parentsGit = chain.parentsGit || ignore.hasGit
		current = parent
	}
	return chain
}

// isHiddenName is is_hidden_path_only: a final path component starting with `.` that does not end in one.
func isHiddenName(name string) bool {
	return strings.HasPrefix(name, ".") && !strings.HasSuffix(name, ".")
}

// walk lists the children of dir, whose path below the root is raw (as the file system names it) and shown (with names
// decoded as UTF-8 with replacement characters). It reports whether the listing ended.
func (l *lister) walk(dir, raw, shown string) (bool, *Failure) {
	entries, err := os.ReadDir(dir)
	if err != nil && l.aborted() {
		return false, newFailure("aborted", "aborted")
	}
	for _, entry := range entries {
		name := entry.Name()
		kind := entry.Type()
		isDirectory := kind.IsDir()
		entryRaw := name
		if raw != "" {
			entryRaw = raw + "/" + name
		}
		if l.skips(filepath.Join(dir, name), name, entryRaw, isDirectory) {
			continue
		}
		if l.aborted() {
			return false, newFailure("aborted", "aborted")
		}
		var letter byte
		switch {
		case kind&os.ModeSymlink != 0:
			letter = 'l'
		case isDirectory:
			letter = 'd'
		case kind.IsRegular():
			letter = 'f'
		default:
			continue
		}
		entryShown := lossyUTF8(name)
		if shown != "" {
			entryShown = shown + "/" + entryShown
		}
		size := uint64(len(entryShown)) + 2
		if l.result.count >= l.options.maxEntries || uint64(len(l.result.payload))+size > l.options.maxBytes || time.Since(l.started) >= l.options.timeout {
			l.result.truncated = true
			return true, nil
		}
		l.result.payload = append(l.result.payload, letter)
		l.result.payload = append(l.result.payload, entryShown...)
		l.result.payload = append(l.result.payload, 0)
		l.result.count++
		if !isDirectory {
			continue
		}
		var directory *directoryIgnore
		if l.chain != nil {
			directory = newDirectoryIgnore(filepath.Join(dir, name), entryRaw)
			l.chain.directories = append(l.chain.directories, directory)
		}
		ended, failure := l.walk(filepath.Join(dir, name), entryRaw, entryShown)
		if directory != nil {
			l.chain.directories = slices.Delete(l.chain.directories, len(l.chain.directories)-1, len(l.chain.directories))
		}
		if failure != nil || ended {
			return ended, failure
		}
	}
	return false, nil
}

// skips is Walk::skip_entry with the listing's filter: what the ignore files, the hidden rule, `.git` and the excluded names leave out.
func (l *lister) skips(path, name, raw string, isDirectory bool) bool {
	if l.chain != nil {
		switch l.chain.matched(raw, isDirectory) {
		case matchIgnore:
			return true
		case matchWhitelist:
		default:
			if !l.options.hidden && (isHiddenName(name) || hiddenAttribute(path)) {
				return true
			}
		}
	} else if !l.options.hidden && (isHiddenName(name) || hiddenAttribute(path)) {
		return true
	}
	return name == ".git" || l.exclude[name]
}

// lossyUTF8 is String::from_utf8_lossy: each maximal run of bytes that begins a well-formed sequence but breaks off, and
// each other invalid byte, becomes one U+FFFD.
func lossyUTF8(text string) string {
	if utf8.ValidString(text) {
		return text
	}
	var out strings.Builder
	for i := 0; i < len(text); {
		if r, size := utf8.DecodeRuneInString(text[i:]); r != utf8.RuneError || size > 1 {
			//portlint:allow utf16units String::from_utf8_lossy works in UTF-8 bytes; no JavaScript string is involved
			out.WriteString(text[i : i+size])
			i += size
			continue
		}
		i += invalidPrefixLength(text[i:])
		out.WriteRune(utf8.RuneError)
	}
	return out.String()
}

// invalidPrefixLength is the length of the maximal prefix of a truncated or ill-formed sequence at the start of text.
func invalidPrefixLength(text string) int {
	lead := text[0]
	var lower, upper byte = 0x80, 0xBF
	var need int
	switch {
	case lead >= 0xC2 && lead <= 0xDF:
		need = 1
	case lead == 0xE0:
		need, lower = 2, 0xA0
	case lead >= 0xE1 && lead <= 0xEC, lead == 0xEE, lead == 0xEF:
		need = 2
	case lead == 0xED:
		need, upper = 2, 0x9F
	case lead == 0xF0:
		need, lower = 3, 0x90
	case lead >= 0xF1 && lead <= 0xF3:
		need = 3
	case lead == 0xF4:
		need, upper = 3, 0x8F
	default:
		return 1
	}
	length := 1
	for range need {
		if length >= len(text) || text[length] < lower || text[length] > upper {
			return length
		}
		lower, upper = 0x80, 0xBF
		length++
	}
	return length
}
