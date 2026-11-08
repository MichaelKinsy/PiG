// Package workspace ports apps/p2/plugins/workspace (server and worker parts): read-only, bounded views of a
// conversation's working directory, resolved inside that conversation's execution environment by its worker.
package workspace

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/workspace/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// Ports apps/p2/plugins/workspace/environment.ts.

const (
	listLimit      = 2000
	statusLimit    = 5000
	statusBytes    = 1024 * 1024
	diffBytes      = 512 * 1024
	untrackedBytes = 256 * 1024
	gitTimeout     = 10.0 // seconds
	searchBytes    = 256 * 1024
	searchLimit    = 500
	logBytes       = 512 * 1024
	filesLimit     = 50_000
	filesBytes     = 4 * 1024 * 1024
	walkDirs       = 5000
	// walkDirectoryEntries are the entries the walk reads from one directory; beyond it the directory is cut in its own
	// (unsorted) order.
	walkDirectoryEntries = 2 * filesLimit
	// watchDirectories is the most directories the working tree's watch covers. Every change rescans the whole watched
	// tree (and on Linux each directory is one watch), so a larger tree, such as a home folder, gets only its root's own
	// entries watched.
	watchDirectories = 2000
)

// watchExcluded are the folders the working tree's watch skips at any depth.
var watchExcluded = []string{".git", "node_modules"}

// Workspace is a read-only, bounded view of one execution environment's working directory. Every file operation and git
// command goes through the environment, never this process's filesystem, so a remote environment works unchanged.
// Canonical confinement filters accidental escapes (symlinked directories); it is not a security boundary against a
// hostile environment, which owners and read/write guests can already command.
type Workspace struct {
	Env durableenv.ExecutionEnv
	// denied are the canonical directories the panel must never expose, such as pi-mobile's private data.
	denied []string

	mu       sync.Mutex
	root     string
	filters  *filterFuture
	watchers int
}

// NewWorkspace is the view of env's working directory without the denied directories.
func NewWorkspace(env durableenv.ExecutionEnv, denied []string) *Workspace {
	return &Workspace{Env: env, denied: denied}
}

func (w *Workspace) canonicalRoot(ctx context.Context) (string, error) {
	w.mu.Lock()
	root := w.root
	w.mu.Unlock()
	if root != "" {
		return root, nil
	}
	canonical, err := w.Env.CanonicalPath(ctx, w.Env.Cwd())
	if err != nil {
		return "", fmt.Errorf("Working directory unavailable: %s", fileCode(err))
	}
	root = strings.TrimRight(canonical, "/")
	if root == "" {
		root = "/"
	}
	w.mu.Lock()
	w.root = root
	w.mu.Unlock()
	return root, nil
}

// fileCode is the code of a FileError ("unknown" for other errors).
func fileCode(err error) durableenv.FileErrorCode {
	var failure *durableenv.FileError
	if errors.As(err, &failure) {
		return failure.Code
	}
	return durableenv.FileErrorUnknown
}

func inside(base, value string) bool {
	if value == base {
		return true
	}
	if base == "/" {
		return strings.HasPrefix(value, "/")
	}
	return strings.HasPrefix(value, base+"/")
}

// resolve is the absolute environment path of a validated relative path, confined to the root.
func (w *Workspace) resolve(ctx context.Context, path string) (string, error) {
	absolute, err := w.absolute(ctx, path)
	if err != nil {
		return "", err
	}
	return absolute, w.confine(ctx, absolute)
}

// absolute is the absolute environment path of a validated relative path, not yet checked to stay inside the root.
func (w *Workspace) absolute(ctx context.Context, path string) (string, error) {
	relative, ok := contract.WorkspacePath(path)
	if !ok {
		return "", errors.New("Invalid workspace path")
	}
	root, err := w.canonicalRoot(ctx)
	if err != nil {
		return "", err
	}
	if relative == "" {
		return root, nil
	}
	if root == "/" {
		root = ""
	}
	return root + "/" + relative, nil
}

func (w *Workspace) confine(ctx context.Context, absolute string) error {
	root, err := w.canonicalRoot(ctx)
	if err != nil {
		return err
	}
	canonical, err := w.Env.CanonicalPath(ctx, absolute)
	if err != nil {
		if fileCode(err) == durableenv.FileErrorNotFound {
			return errors.New("File not found")
		}
		return errors.New("Cannot open path")
	}
	if !inside(root, canonical) {
		return errors.New("Path leaves the working directory")
	}
	for _, denied := range w.denied {
		if inside(denied, canonical) {
			return errors.New("Path is private")
		}
	}
	return nil
}

// openConfined opens path while checking that it stays inside the root, in parallel: a remote environment pays one round
// trip for both. Nothing opened is used before the check passed; an opened handle of a refused path is closed. The open
// error is returned as it is, for the caller to name.
func openConfined[T interface{ Close(context.Context) error }](w *Workspace, ctx context.Context, path string, open func(absolute string) (T, error)) (absolute string, opened T, openErr error, err error) {
	absolute, err = w.absolute(ctx, path)
	if err != nil {
		return absolute, opened, nil, err
	}
	var confined error
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		confined = w.confine(ctx, absolute)
	}()
	opened, openErr = open(absolute)
	group.Wait()
	if confined != nil {
		if openErr == nil {
			_ = opened.Close(ctx)
		}
		var zero T
		return absolute, zero, nil, confined
	}
	return absolute, opened, openErr, nil
}

// Query answers every query but `place`, which the worker answers (where it opened this workspace).
func (w *Workspace) Query(ctx context.Context, query *delta.JsonObject) (*delta.JsonObject, error) {
	text := func(key string) string { value, _ := query.Value(key).(string); return value }
	flag := func(key string) bool { value, _ := query.Value(key).(bool); return value }
	switch text("kind") {
	case "list":
		return w.list(ctx, text("path"))
	case "file":
		if commit := text("commit"); commit != "" {
			return w.fileAt(ctx, commit, text("path"))
		}
		return w.file(ctx, text("path"))
	case "log":
		skip, _ := query.Value("skip").(float64)
		limit, _ := query.Value("limit").(float64)
		return w.log(ctx, int(skip), int(limit))
	case "commit":
		return w.commit(ctx, text("commit"))
	case "status":
		return w.status(ctx)
	case "files":
		return w.files(ctx)
	case "search":
		return w.search(ctx, query)
	case "diff":
		lines := 3
		if value, ok := query.Value("context").(float64); ok {
			lines = int(value)
		}
		if commit := text("commit"); commit != "" {
			return w.commitDiff(ctx, commit, text("path"), flag("full"), lines)
		}
		return w.diff(ctx, text("path"), flag("full"), lines)
	}
	return nil, fmt.Errorf("Unknown workspace query %q", text("kind"))
}

func (w *Workspace) list(ctx context.Context, path string) (*delta.JsonObject, error) {
	// Git's filter overrides are needed for check-ignore below: fetch them while the folder is read.
	filters := w.filterOverrides(ctx)
	absolute, opened, openErr, err := openConfined(w, ctx, path, func(directory string) (durableenv.DirReader, error) {
		return w.Env.OpenDirReader(ctx, directory)
	})
	if err != nil {
		return nil, err
	}
	if openErr != nil {
		if fileCode(openErr) == durableenv.FileErrorNotDirectory {
			return nil, errors.New("Not a directory")
		}
		return nil, errors.New("Cannot list directory")
	}
	type entry struct {
		name    string
		kind    durableenv.FileKind
		size    int64
		mtimeMs float64
		ignored bool
	}
	var entries []*entry
	truncated := false
	err = func() error {
		defer func() { _ = opened.Close(ctx) }()
		for {
			page, err := opened.Next(ctx, min(500, listLimit+1-len(entries)))
			if err != nil {
				return errors.New("Cannot list directory")
			}
			for _, item := range page.Entries {
				if path == "" && item.Name == ".git" {
					continue
				}
				entries = append(entries, &entry{name: item.Name, kind: item.Kind, size: item.Size, mtimeMs: item.MtimeMs})
			}
			if len(entries) > listLimit {
				truncated = true
				entries = entries[:listLimit]
				return nil
			}
			if page.Done {
				return nil
			}
		}
	}()
	if err != nil {
		return nil, err
	}
	sort.SliceStable(entries, func(a, b int) bool {
		left, right := entries[a].kind == durableenv.FileKindDirectory, entries[b].kind == durableenv.FileKindDirectory
		if left != right {
			return left
		}
		return p2runtime.LocaleCompare(entries[a].name, entries[b].name) < 0
	})
	// Ignored entries, by .gitignore; tracked files and non-repositories report none. Run from this directory, in batches
	// of at most 24,000 characters (Windows limits a command line to 32,767) started together: one round trip for any
	// directory. Names containing newlines remain unannotated because output is line-based.
	var batches [][]string
	length := 0
	for _, item := range entries {
		if strings.Contains(item.name, "\n") {
			continue
		}
		units := p2runtime.JSLength(item.name)
		if len(batches) == 0 || length+units+3 > 24_000 {
			batches = append(batches, []string{item.name})
			length = units + 3
		} else {
			batches[len(batches)-1] = append(batches[len(batches)-1], item.name)
			length += units + 3
		}
	}
	runs := make([]*gitRun, len(batches))
	var group sync.WaitGroup
	for index, names := range batches {
		group.Add(1)
		go func() {
			defer group.Done()
			run, err := w.gitWith(ctx, filters, append([]string{"check-ignore", "--"}, names...), statusBytes, absolute)
			if err == nil {
				runs[index] = run
			}
		}()
	}
	group.Wait()
	ignored := map[string]bool{}
	for _, run := range runs {
		if run != nil && run.exitCode <= 1 {
			for _, line := range strings.Split(run.stdout, "\n") {
				if line != "" {
					ignored[line] = true
				}
			}
		}
	}
	list := make([]any, 0, len(entries))
	for _, item := range entries {
		out := delta.JsonObjectOf("name", item.name, "kind", string(item.kind), "size", float64(item.size), "mtimeMs", item.mtimeMs)
		if ignored[item.name] {
			out.Set("ignored", true)
		}
		list = append(list, out)
	}
	return delta.JsonObjectOf("kind", "list", "path", path, "entries", list, "truncated", truncated), nil
}

func (w *Workspace) file(ctx context.Context, path string) (*delta.JsonObject, error) {
	_, opened, openErr, err := openConfined(w, ctx, path, func(absolute string) (durableenv.BinaryReader, error) {
		return w.Env.OpenBinaryReader(ctx, absolute, &durableenv.OpenBinaryReaderOptions{NoFollow: true})
	})
	if err != nil {
		return nil, err
	}
	if openErr != nil {
		if fileCode(openErr) == durableenv.FileErrorIsDirectory {
			return nil, errors.New("Is a directory")
		}
		return nil, errors.New("Not a regular file")
	}
	defer func() { _ = opened.Close(ctx) }()
	// Size and content together: the read stops at the limit or the end of the file either way.
	info, infoErr := opened.Info(ctx)
	read, readErr := opened.Read(ctx, 0, contract.WorkspaceTextLimit)
	if infoErr != nil || readErr != nil {
		return nil, errors.New("Cannot read file")
	}
	truncated := info.Size > contract.WorkspaceTextLimit
	text, ok := decodeText(read, truncated)
	if !ok {
		return delta.JsonObjectOf("kind", "file", "path", path, "size", float64(info.Size), "binary", true, "truncated", truncated), nil
	}
	return delta.JsonObjectOf("kind", "file", "path", path, "size", float64(info.Size), "binary", false, "text", text, "truncated", truncated), nil
}

// ReadRange is the range of a read: Start and End (inclusive) from a known start, or Suffix, the last bytes.
type ReadRange struct {
	Start, End, Suffix *int64
}

// Read is up to one chunk of bytes as `{ size, start, data }` (base64). `end` is inclusive; `suffix` reads the last bytes.
func (w *Workspace) Read(ctx context.Context, path string, bounds ReadRange) (*delta.JsonObject, error) {
	_, opened, openErr, err := openConfined(w, ctx, path, func(absolute string) (durableenv.BinaryReader, error) {
		return w.Env.OpenBinaryReader(ctx, absolute, &durableenv.OpenBinaryReaderOptions{NoFollow: true})
	})
	if err != nil {
		return nil, err
	}
	if openErr != nil {
		return nil, errors.New("Not a regular file")
	}
	defer func() { _ = opened.Close(ctx) }()
	info, err := opened.Info(ctx)
	if err != nil {
		return nil, errors.New("Cannot read file")
	}
	size := info.Size
	var start int64
	if bounds.Suffix != nil {
		start = max(0, size-*bounds.Suffix)
	} else if bounds.Start != nil {
		start = *bounds.Start
	}
	end := size - 1
	if bounds.End != nil {
		end = min(*bounds.End, end)
	}
	end = min(end, start+contract.WorkspaceReadChunk-1)
	if start >= size || end < start {
		return delta.JsonObjectOf("size", float64(size), "start", float64(start), "data", ""), nil
	}
	read, err := opened.Read(ctx, start, end-start+1)
	if err != nil {
		return nil, errors.New("Cannot read file")
	}
	// The file may have grown since its size was read: serve the announced range only.
	if int64(len(read)) > end-start+1 {
		read = read[:end-start+1]
	}
	return delta.JsonObjectOf("size", float64(size), "start", float64(start), "data", base64.StdEncoding.EncodeToString(read)), nil
}

// status is the changes below the root, with paths relative to it: git reports them relative to the repository's top
// level, which is above the root when the root is a folder inside a repository (`~/repo/apps/web`).
func (w *Workspace) status(ctx context.Context) (*delta.JsonObject, error) {
	located, err := w.git(ctx, []string{"rev-parse", "--show-prefix"}, 8192, "")
	if err != nil {
		return nil, err
	}
	prefix := ""
	if located.exitCode == 0 {
		prefix = p2runtime.JSTrim(located.stdout)
	}
	below := func(path string) string {
		if prefix != "" && strings.HasPrefix(path, prefix) {
			return path[len(prefix):]
		}
		return path
	}
	run, err := w.git(ctx, []string{
		"status", "--porcelain=v2", "-z", "--branch",
		// List files inside new folders; `normal` reports only the folder, which has no diff.
		"--untracked-files=all", "--ignore-submodules=dirty", "--no-ahead-behind", "--", ".",
	}, statusBytes, "")
	if err != nil {
		return nil, err
	}
	if run.exitCode != 0 {
		out := delta.JsonObjectOf("kind", "status", "repo", false)
		if reason := firstLine(run.stderr); reason != "" {
			out.Set("reason", reason)
		}
		return out, nil
	}
	records := strings.Split(run.stdout, "\x00")
	var changes []any
	var branch, head string
	// Only complete records count: output stopped at the cap may end mid-record.
	complete := records
	if run.truncated {
		complete = records[:len(records)-1]
	}
	change := func(status, path string) *delta.JsonObject {
		return delta.JsonObjectOf("path", path, "status", status)
	}
	for index := 0; index < len(complete); index++ {
		record := complete[index]
		switch {
		case strings.HasPrefix(record, "# branch.head "):
			branch = record[14:]
		case strings.HasPrefix(record, "# branch.oid "):
			head = record[13:]
		case strings.HasPrefix(record, "1 "):
			changes = append(changes, change(slice(record, 2, 4), below(field(record, 8))))
		case strings.HasPrefix(record, "2 "):
			next := ""
			if index+1 < len(complete) {
				next = complete[index+1]
			}
			entry := change(slice(record, 2, 4), below(field(record, 9)))
			entry.Set("from", below(next))
			changes = append(changes, entry)
			index++
		case strings.HasPrefix(record, "u "):
			changes = append(changes, change(slice(record, 2, 4), below(field(record, 10))))
		case strings.HasPrefix(record, "? "):
			changes = append(changes, change("??", below(record[2:])))
		}
		if len(changes) >= statusLimit {
			break
		}
	}
	out := delta.JsonObjectOf("kind", "status", "repo", true)
	if branch != "" && branch != "(detached)" {
		out.Set("branch", branch)
	}
	if head != "" && head != "(initial)" {
		out.Set("head", head)
	}
	if changes == nil {
		changes = []any{}
	}
	out.Set("changes", changes)
	out.Set("truncated", run.truncated || len(changes) >= statusLimit)
	return out, nil
}

// slice is JavaScript's `text.slice(from, to)` on ASCII-prefixed records.
func slice(text string, from, to int) string {
	if from > len(text) {
		return ""
	}
	return text[from:min(to, len(text))]
}

// files is every file below the root, for the name filter. In a repository: tracked and untracked files without ignored
// ones, as Quick Open skips them. Elsewhere (or without git): a walk of the directory, skipping `.git` and
// `node_modules` folders and not following symlinks. Private directories inside the root are left out either way.
//
// The walk is breadth first with each directory's entries in name order (UTF-16 code units; byte order for names
// without astral characters, as pi-env's `listDir` and `listFiles`), so a truncated walk keeps the same files on every
// filesystem: shallow directories, and earlier names among siblings. Directory order (`OpenDirReader`) is the
// filesystem's hash or creation order and would keep an arbitrary subset.
func (w *Workspace) files(ctx context.Context) (*delta.JsonObject, error) {
	root, err := w.canonicalRoot(ctx)
	if err != nil {
		return nil, err
	}
	base := root
	if base == "/" {
		base = ""
	}
	privateDirectory := func(absolute string) bool {
		for _, denied := range w.denied {
			if absolute == denied || strings.HasPrefix(absolute, denied+"/") {
				return true
			}
		}
		return false
	}
	if run, err := w.git(ctx, []string{"ls-files", "-z", "--cached", "--others", "--exclude-standard"}, filesBytes, ""); err == nil && (run.exitCode == 0 || run.truncated) {
		// The text after the last NUL is empty, or a record cut at the output cap. Unmerged files repeat.
		records := strings.Split(run.stdout, "\x00")
		records = records[:len(records)-1]
		seen := map[string]bool{}
		paths := make([]any, 0, len(records))
		for _, path := range records {
			if seen[path] || privateDirectory(base+"/"+path) {
				continue
			}
			seen[path] = true
			paths = append(paths, path)
		}
		truncated := run.truncated || len(paths) > filesLimit
		if len(paths) > filesLimit {
			paths = paths[:filesLimit]
		}
		return delta.JsonObjectOf("kind", "files", "paths", paths, "truncated", truncated), nil
	}
	paths := []any{}
	queue := []string{""}
	truncated := false
	for index := 0; index < len(queue) && !truncated; index++ {
		if index >= walkDirs {
			truncated = true
			break
		}
		relative := queue[index]
		absolute := root
		if relative != "" {
			absolute = base + "/" + relative
		}
		if privateDirectory(absolute) {
			continue
		}
		opened, err := w.Env.OpenDirReader(ctx, absolute)
		if err != nil {
			continue
		}
		var entries []durableenv.FileInfo
		for {
			page, err := opened.Next(ctx, 500)
			if err != nil {
				break
			}
			entries = append(entries, page.Entries...)
			if len(entries) > walkDirectoryEntries {
				truncated = true
				entries = entries[:walkDirectoryEntries]
				break
			}
			if page.Done {
				break
			}
		}
		_ = opened.Close(ctx)
		sort.SliceStable(entries, func(a, b int) bool { return utf16Less(entries[a].Name, entries[b].Name) })
		for _, entry := range entries {
			path := entry.Name
			if relative != "" {
				path = relative + "/" + entry.Name
			}
			if entry.Kind != durableenv.FileKindDirectory {
				paths = append(paths, path)
			} else if entry.Name != ".git" && entry.Name != "node_modules" {
				queue = append(queue, path)
			}
			if len(paths) > filesLimit {
				truncated = true
				paths = paths[:filesLimit]
				break
			}
		}
	}
	return delta.JsonObjectOf("kind", "files", "paths", paths, "truncated", truncated), nil
}

// utf16Less is JavaScript's `a < b` on strings: UTF-16 code unit order.
func utf16Less(a, b string) bool {
	left, right := []rune(a), []rune(b)
	for index := 0; index < len(left) && index < len(right); index++ {
		if left[index] == right[index] {
			continue
		}
		return utf16Key(left[index]) < utf16Key(right[index])
	}
	return len(left) < len(right)
}

// utf16Key orders a code point by its first UTF-16 code unit (astral characters sort by their high surrogate).
func utf16Key(character rune) rune {
	if character >= 0x10000 {
		return 0xd800 + (character-0x10000)>>10
	}
	return character
}

var globEscape = strings.NewReplacer("*", `\*`, "?", `\?`, "[", `\[`, `\`, `\\`)

// search is a literal, smart-case (case-insensitive unless the text has capitals) search of text files below `path`,
// tracked and untracked, skipping ignored files. Outside a repository it searches the directory itself. Include/exclude
// globs relative to `path` become case-insensitive glob pathspecs; a pattern also matches everything below a folder it
// matches. Includes are alternatives; excludes apply to all.
func (w *Workspace) search(ctx context.Context, query *delta.JsonObject) (*delta.JsonObject, error) {
	text, _ := query.Value("text").(string)
	path, _ := query.Value("path").(string)
	if path != "" {
		if _, err := w.resolve(ctx, path); err != nil {
			return nil, err
		}
	}
	includeText, _ := query.Value("include").(string)
	excludeText, _ := query.Value("exclude").(string)
	include, includeOK := contract.WorkspaceGlobs(includeText)
	exclude, excludeOK := contract.WorkspaceGlobs(excludeText)
	if !includeOK || !excludeOK {
		return nil, errors.New("Invalid file pattern")
	}
	// Every pathspec carries explicit magic, so the scope path itself stays literal.
	base := ""
	if path != "" {
		base = globEscape.Replace(path) + "/"
	}
	specs := func(patterns []string, magic string) []string {
		var out []string
		for _, pattern := range patterns {
			out = append(out, ":("+magic+")"+base+pattern, ":("+magic+")"+base+pattern+"/**")
		}
		return out
	}
	var pathspecs []string
	if len(include) > 0 {
		pathspecs = specs(include, "glob,icase")
	} else if path != "" {
		pathspecs = []string{":(literal)" + path}
	}
	pathspecs = append(pathspecs, specs(exclude, "glob,icase,exclude")...)
	args := []string{"grep", "-n", "-z", "-I", "-F", "--no-color", "--no-textconv", "--exclude-standard", "-m", "20"}
	if text == strings.ToLower(text) {
		args = append(args, "-i")
	}
	args = append(args, "-e", text, "--")
	args = append(args, pathspecs...)
	grep := func(mode string) (*gitRun, error) {
		run, err := w.git(ctx, append([]string{"grep", mode}, args[1:]...), searchBytes, "")
		if err != nil {
			return nil, err
		}
		// Git before 2.38 has no grep -m. Keep the byte/time limits and cap each file below instead.
		if run.exitCode == 129 && strings.Contains(run.stderr, "unknown switch `m'") {
			for index, argument := range args {
				if argument == "-m" {
					args = append(args[:index], args[index+2:]...)
					break
				}
			}
			return w.git(ctx, append([]string{"grep", mode}, args[1:]...), searchBytes, "")
		}
		return run, nil
	}
	run, err := grep("--untracked")
	if err != nil {
		return nil, err
	}
	// Not a repository: search the directory itself.
	if run.exitCode == 128 {
		if run, err = grep("--no-index"); err != nil {
			return nil, err
		}
	}
	if run.exitCode > 1 {
		if reason := firstLine(run.stderr); reason != "" {
			return nil, errors.New(reason)
		}
		return nil, errors.New("Search failed")
	}
	matches := []any{}
	perFile := map[string]int{}
	// Records are path NUL line NUL text LF; a cut record at the output cap is dropped.
	position := 0
	for len(matches) < searchLimit {
		pathEnd := indexFrom(run.stdout, "\x00", position)
		lineEnd := -1
		if pathEnd >= 0 {
			lineEnd = indexFrom(run.stdout, "\x00", pathEnd+1)
		}
		textEnd := -1
		if lineEnd >= 0 {
			textEnd = indexFrom(run.stdout, "\n", lineEnd+1)
		}
		if textEnd < 0 {
			break
		}
		matchPath := run.stdout[position:pathEnd]
		if perFile[matchPath] < 20 {
			line, _ := strconv.ParseFloat(run.stdout[pathEnd+1:lineEnd], 64)
			matches = append(matches, delta.JsonObjectOf("path", matchPath, "line", line, "text", truncateUnits(run.stdout[lineEnd+1:textEnd], 300)))
			perFile[matchPath]++
		}
		position = textEnd + 1
	}
	return delta.JsonObjectOf("kind", "search", "text", text, "path", path, "matches", matches,
		"truncated", run.truncated || len(matches) >= searchLimit), nil
}

func indexFrom(text, separator string, from int) int {
	if from > len(text) {
		return -1
	}
	found := strings.Index(text[from:], separator)
	if found < 0 {
		return -1
	}
	return from + found
}

// truncateUnits is `text.slice(0, units)` (UTF-16 code units; a split character is dropped).
func truncateUnits(text string, units int) string {
	count := 0
	for index, character := range text {
		width := 1
		if character >= 0x10000 {
			width = 2
		}
		if count+width > units {
			return text[:index]
		}
		count += width
	}
	return text
}

// log is the current branch's commits, newest first, with unpushed ones marked when there is an upstream.
func (w *Workspace) log(ctx context.Context, skip, limit int) (*delta.JsonObject, error) {
	empty := func() *delta.JsonObject {
		return delta.JsonObjectOf("kind", "log", "commits", []any{}, "more", false)
	}
	run, err := w.git(ctx, []string{"log", "-z", "--format=%H%x1f%h%x1f%an%x1f%at%x1f%s", fmt.Sprintf("--skip=%d", skip), "-n", strconv.Itoa(limit + 1), "HEAD", "--"}, logBytes, "")
	if err != nil {
		return nil, err
	}
	if run.exitCode != 0 {
		return empty(), nil
	}
	var records []string
	for _, record := range strings.Split(run.stdout, "\x00") {
		if record != "" {
			records = append(records, record)
		}
	}
	if run.truncated && len(records) > 0 {
		records = records[:len(records)-1]
	}
	upstream, err := w.git(ctx, []string{"rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"}, 4096, "")
	if err != nil {
		return nil, err
	}
	var unpushed map[string]bool
	if upstream.exitCode == 0 {
		ahead, err := w.git(ctx, []string{"rev-list", "-n", "5000", "HEAD", "--not", "@{upstream}", "--"}, logBytes, "")
		if err != nil {
			return nil, err
		}
		if ahead.exitCode == 0 {
			unpushed = map[string]bool{}
			for _, line := range strings.Split(ahead.stdout, "\n") {
				if line != "" {
					unpushed[line] = true
				}
			}
		}
	}
	commits := []any{}
	for _, record := range records[:min(limit, len(records))] {
		parts := append(strings.Split(record, "\x1f"), "", "", "", "", "")
		time, _ := strconv.ParseFloat(parts[3], 64)
		commit := delta.JsonObjectOf("hash", parts[0], "short", parts[1], "author", parts[2], "time", time*1000, "subject", parts[4])
		if unpushed != nil {
			commit.Set("unpushed", unpushed[parts[0]])
		}
		commits = append(commits, commit)
	}
	out := delta.JsonObjectOf("kind", "log", "commits", commits, "more", len(records) > limit)
	if upstream.exitCode == 0 {
		out.Set("upstream", p2runtime.JSTrim(upstream.stdout))
	}
	return out, nil
}

// commit is one commit's message and changed files (first parent for merges, the empty tree for a root commit).
func (w *Workspace) commit(ctx context.Context, commit string) (*delta.JsonObject, error) {
	meta, err := w.git(ctx, []string{"show", "-s", "--format=%H%x1f%an%x1f%at%x1f%B", commit, "--"}, 64*1024, "")
	if err != nil {
		return nil, err
	}
	if meta.exitCode != 0 {
		return nil, errors.New("Unknown commit")
	}
	parts := strings.Split(meta.stdout, "\x1f")
	hash, author, timeText, message := commit, "", "0", []string{}
	if len(parts) > 0 {
		hash = parts[0]
	}
	if len(parts) > 1 {
		author = parts[1]
	}
	if len(parts) > 2 {
		timeText = parts[2]
	}
	if len(parts) > 3 {
		message = parts[3:]
	}
	run, err := w.git(ctx, []string{
		// Relative to the root (and only below it), as the file and diff queries read paths.
		"show", "--format=", "--name-status", "--relative", "-z", "-M", "--diff-merges=first-parent", hash, "--",
	}, statusBytes, "")
	if err != nil {
		return nil, err
	}
	if run.exitCode != 0 {
		if reason := firstLine(run.stderr); reason != "" {
			return nil, errors.New(reason)
		}
		return nil, errors.New("Cannot read commit")
	}
	tokens := strings.Split(run.stdout, "\x00")
	if run.truncated {
		tokens = tokens[:len(tokens)-1]
	}
	changes := []any{}
	next := func(index *int) string {
		token := ""
		if *index < len(tokens) {
			token = tokens[*index]
		}
		*index++
		return token
	}
	for index := 0; index < len(tokens) && len(changes) < statusLimit; {
		status := next(&index)
		if status == "" {
			continue
		}
		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			from := next(&index)
			path := next(&index)
			if path != "" {
				changes = append(changes, delta.JsonObjectOf("path", path, "status", status[:1]+".", "from", from))
			}
		} else if path := next(&index); path != "" {
			changes = append(changes, delta.JsonObjectOf("path", path, "status", status[:1]+"."))
		}
	}
	seconds, _ := strconv.ParseFloat(timeText, 64)
	return delta.JsonObjectOf("kind", "commit", "commit", hash, "author", author, "time", seconds*1000,
		"message", strings.TrimRightFunc(strings.Join(message, "\x1f"), isJSSpace), "changes", changes,
		"truncated", run.truncated || len(changes) >= statusLimit), nil
}

func isJSSpace(character rune) bool { return p2runtime.JSTrim(string(character)) == "" }

// commitDiff is a file's change in one commit.
func (w *Workspace) commitDiff(ctx context.Context, commit, path string, full bool, lines int) (*delta.JsonObject, error) {
	relative, ok := contract.WorkspacePath(path)
	if !ok || relative == "" {
		return nil, errors.New("Invalid workspace path")
	}
	run, err := w.git(ctx, []string{
		"show", "--format=", "--no-color", "--no-ext-diff", "--no-textconv", "--no-relative",
		"--src-prefix=a/", "--dst-prefix=b/", "--diff-merges=first-parent", "-M",
		"--unified=" + unified(full, lines), commit, "--", relative,
	}, diffBytes, "")
	if err != nil {
		return nil, err
	}
	if run.exitCode != 0 && !run.truncated {
		if reason := firstLine(run.stderr); reason != "" {
			return nil, errors.New(reason)
		}
		return nil, errors.New("Diff failed")
	}
	return delta.JsonObjectOf("kind", "diff", "path", path, "full", full, "patch", run.stdout, "truncated", run.truncated), nil
}

func unified(full bool, lines int) string {
	if full {
		return "1000000000"
	}
	return strconv.Itoa(lines)
}

// fileAt is a file as of one commit, read from the repository rather than the working tree.
func (w *Workspace) fileAt(ctx context.Context, commit, path string) (*delta.JsonObject, error) {
	relative, ok := contract.WorkspacePath(path)
	if !ok || relative == "" {
		return nil, errors.New("Invalid workspace path")
	}
	// `./`: relative to the root, not to the repository's top level.
	object := commit + ":./" + relative
	sized, err := w.git(ctx, []string{"cat-file", "-s", object}, 64, "")
	if err != nil {
		return nil, err
	}
	if sized.exitCode != 0 {
		return nil, errors.New("File not found in that commit")
	}
	size, _ := strconv.ParseFloat(p2runtime.JSTrim(sized.stdout), 64)
	run, err := w.git(ctx, []string{"cat-file", "blob", object}, contract.WorkspaceTextLimit, "")
	if err != nil {
		return nil, err
	}
	if run.exitCode != 0 && !run.truncated {
		return nil, errors.New("Cannot read file")
	}
	// Output arrives decoded; NUL bytes survive decoding and mark binary content.
	if strings.Contains(truncateUnits(run.stdout, 8192), "\x00") {
		return delta.JsonObjectOf("kind", "file", "path", path, "size", size, "binary", true, "truncated", false), nil
	}
	return delta.JsonObjectOf("kind", "file", "path", path, "size", size, "binary", false, "text", run.stdout, "truncated", run.truncated), nil
}

var uploadToken = regexp.MustCompile(`^[0-9a-f-]{36}$`)

// Write is one chunk of an upload into a folder: chunks append to a hidden temporary file in that folder, renamed into
// place by the final one. Existing files are never overwritten.
func (w *Workspace) Write(ctx context.Context, upload *delta.JsonObject) error {
	token, _ := upload.Value("token").(string)
	if !uploadToken.MatchString(token) {
		return errors.New("Invalid upload")
	}
	name, _ := upload.Value("name").(string)
	if _, valid := contract.WorkspacePath(name); name == "" || strings.Contains(name, "/") || !valid || strings.HasPrefix(name, ".pi-upload-") {
		return errors.New("Invalid file name")
	}
	path, _ := upload.Value("path").(string)
	directory, err := w.resolve(ctx, path)
	if err != nil {
		return err
	}
	prefix := directory
	if prefix == "/" {
		prefix = ""
	}
	target := prefix + "/" + name
	temp := prefix + "/.pi-upload-" + token
	if abort, _ := upload.Value("abort").(bool); abort {
		_ = w.Env.Remove(ctx, temp, &durableenv.RemoveOptions{Force: true})
		return nil
	}
	free := func() error {
		exists, err := w.Env.Exists(ctx, target)
		if err != nil {
			return errors.New("Cannot write here")
		}
		if exists {
			return fmt.Errorf("%s already exists", name)
		}
		return nil
	}
	encoded, _ := upload.Value("data").(string)
	bytes, _ := base64.StdEncoding.DecodeString(encoded)
	offset, _ := upload.Value("offset").(float64)
	if offset == 0 {
		if err := free(); err != nil {
			return err
		}
		err = w.Env.WriteFile(ctx, temp, bytes)
	} else {
		err = w.Env.AppendFile(ctx, temp, bytes)
	}
	if err != nil {
		return errors.New("Cannot write here")
	}
	if final, _ := upload.Value("final").(bool); !final {
		return nil
	}
	err = func() error {
		if err := free(); err != nil {
			return err
		}
		if err := w.Env.RenameFile(ctx, temp, target); err != nil {
			return errors.New("Cannot write here")
		}
		return nil
	}()
	if err != nil {
		_ = w.Env.Remove(ctx, temp, &durableenv.RemoveOptions{Force: true})
	}
	return err
}

func (w *Workspace) diff(ctx context.Context, path string, full bool, lines int) (*delta.JsonObject, error) {
	relative, ok := contract.WorkspacePath(path)
	// Deleted files have no canonical path: validate lexically and let git scope the pathspec.
	if !ok || relative == "" {
		return nil, errors.New("Invalid workspace path")
	}
	head, err := w.git(ctx, []string{"rev-parse", "--verify", "-q", "HEAD"}, 4096, "")
	if err != nil {
		return nil, err
	}
	base := p2runtime.JSTrim(head.stdout)
	if head.exitCode != 0 {
		// Unborn branch: compare with the empty tree of this repository's hash function.
		empty, err := w.git(ctx, []string{"hash-object", "-t", "tree", "--stdin"}, 4096, "")
		if err != nil {
			return nil, err
		}
		if empty.exitCode != 0 {
			if reason := firstLine(empty.stderr); reason != "" {
				return nil, errors.New(reason)
			}
			return nil, errors.New("Not a git repository")
		}
		base = p2runtime.JSTrim(empty.stdout)
	}
	run, err := w.git(ctx, []string{
		"diff", "--no-color", "--no-ext-diff", "--no-textconv", "--no-relative",
		"--src-prefix=a/", "--dst-prefix=b/", "--ignore-submodules=dirty", "-M",
		"--unified=" + unified(full, lines), base, "--", relative,
	}, diffBytes, "")
	if err != nil {
		return nil, err
	}
	if run.exitCode != 0 {
		if reason := firstLine(run.stderr); reason != "" {
			return nil, errors.New(reason)
		}
		return nil, errors.New("Diff failed")
	}
	result := func(patch string, truncated bool) *delta.JsonObject {
		return delta.JsonObjectOf("kind", "diff", "path", path, "full", full, "patch", patch, "truncated", truncated)
	}
	if run.stdout != "" || run.truncated {
		return result(run.stdout, run.truncated), nil
	}
	// Untracked files have no diff against HEAD: show their content as added.
	absolute, err := w.resolve(ctx, relative)
	if err != nil {
		return result("", false), nil
	}
	opened, err := w.Env.OpenBinaryReader(ctx, absolute, &durableenv.OpenBinaryReaderOptions{NoFollow: true})
	if err != nil {
		return result("", false), nil
	}
	defer func() { _ = opened.Close(ctx) }()
	var size int64
	if info, err := opened.Info(ctx); err == nil {
		size = info.Size
	}
	var content []byte
	var read []byte
	if read, err = opened.Read(ctx, 0, min(size, untrackedBytes)); err == nil {
		content = read
	}
	text, decoded := decodeText(content, size > untrackedBytes)
	if err != nil || !decoded {
		return result(fmt.Sprintf("diff --git a/%s b/%s\nnew file\nBinary file b/%s\n", relative, relative, relative), false), nil
	}
	var rows []string
	if strings.HasSuffix(text, "\n") {
		rows = strings.Split(text[:len(text)-1], "\n")
	} else {
		rows = strings.Split(text, "\n")
	}
	patch := []string{
		"diff --git a/" + relative + " b/" + relative,
		"new file (untracked)",
		"--- /dev/null",
		"+++ b/" + relative,
		fmt.Sprintf("@@ -0,0 +1,%d @@", len(rows)),
	}
	for _, row := range rows {
		patch = append(patch, "+"+row)
	}
	return result(strings.Join(patch, "\n")+"\n", size > untrackedBytes), nil
}

func field(record string, index int) string {
	// Space-separated fields before the path; the path itself may contain spaces.
	position := 0
	for count := 0; count < index; count++ {
		found := strings.Index(record[min(position, len(record)):], " ")
		if found < 0 {
			position = 0
		} else {
			position += found + 1
		}
	}
	return record[min(position, len(record)):]
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(p2runtime.JSTrim(text), "\n")
	return truncateUnits(line, 300)
}

// decodeText is UTF-8 text, or ok false for binary content (NUL bytes or invalid UTF-8).
func decodeText(bytes []byte, truncated bool) (string, bool) {
	if strings.IndexByte(string(bytes[:min(len(bytes), 8192)]), 0) >= 0 {
		return "", false
	}
	// `TextDecoder` removes a byte order mark.
	bytes = []byte(strings.TrimPrefix(string(bytes), "\uFEFF"))
	// A cut inside a multi-byte character is not binary; drop up to three trailing bytes.
	maxDrop := 0
	if truncated {
		maxDrop = 3
	}
	for drop := 0; drop <= maxDrop && drop <= len(bytes); drop++ {
		if candidate := bytes[:len(bytes)-drop]; utf8.Valid(candidate) {
			return string(candidate), true
		}
	}
	return "", false
}
