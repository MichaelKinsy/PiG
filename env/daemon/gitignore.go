package daemon

// Ports the gitignore matching of the `ignore` crate 0.4.33 (gitignore.rs, dir.rs) as list.rs uses it: `.gitignore`
// files, `.git/info/exclude` and the global excludes file, honored only inside git work trees (require_git), with the
// closest ignore file that matches an entry deciding it.

import (
	"bytes"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

type matchKind int

const (
	matchNone matchKind = iota
	matchIgnore
	matchWhitelist
)

// or is Match::or: the first match, whether it ignores or whitelists.
func (m matchKind) or(other matchKind) matchKind {
	if m == matchNone {
		return other
	}
	return m
}

type gitignoreGlob struct {
	re *regexp.Regexp
	// literal is a string every path the glob matches contains.
	literal   string
	whitelist bool
	onlyDir   bool
}

// gitignore is a Gitignore of the `ignore` crate: the globs of one ignore file, matched against paths relative to the
// directory the file applies to.
type gitignore struct{ globs []gitignoreGlob }

func (g *gitignore) empty() bool { return g == nil || len(g.globs) == 0 }

// matched is Gitignore::matched for a path relative to the file's directory: the last matching glob decides.
func (g *gitignore) matched(relative string, isDir bool) matchKind {
	if g.empty() {
		return matchNone
	}
	haystack, mapped := "", false
	for _, glob := range slices.Backward(g.globs) {

		if (glob.onlyDir && !isDir) || !strings.Contains(relative, glob.literal) {
			continue
		}
		if !mapped {
			haystack, mapped = byteString(relative), true
		}
		if !glob.re.MatchString(haystack) {
			continue
		}
		if glob.whitelist {
			return matchWhitelist
		}
		return matchIgnore
	}
	return matchNone
}

// addLine is GitignoreBuilder::add_line; a line that is no valid glob is dropped, as the crate drops it.
func (g *gitignore) addLine(line string) {
	if strings.HasPrefix(line, "#") {
		return
	}
	if !strings.HasSuffix(line, `\ `) {
		line = strings.TrimRightFunc(line, unicode.IsSpace)
	}
	if line == "" {
		return
	}
	glob := gitignoreGlob{}
	isAbsolute := false
	if strings.HasPrefix(line, `\!`) || strings.HasPrefix(line, `\#`) {
		line = line[1:]
	} else {
		if strings.HasPrefix(line, "!") {
			glob.whitelist = true
			line = line[1:]
		}
		if strings.HasPrefix(line, "/") {
			// A leading slash anchors the glob at the ignore file's directory: wildcards cannot match `/`.
			line = line[1:]
			isAbsolute = true
		}
	}
	if strings.HasSuffix(line, "/") {
		glob.onlyDir = true
		line = line[:len(line)-1]
		line = strings.TrimSuffix(line, `\`)
	}
	actual := line
	if !isAbsolute && !strings.Contains(line, "/") && !strings.HasPrefix(actual, "**/") && actual != "**" {
		actual = "**/" + actual
	}
	if strings.HasSuffix(actual, "/**") {
		actual += "/*"
	}
	re, literal, err := compileGlob(actual)
	if err != nil {
		return
	}
	glob.re, glob.literal = re, literal
	g.globs = append(g.globs, glob)
}

// loadGitignore is GitignoreBuilder::add of one file: the lines up to the first one that is not UTF-8, the first line
// without a leading byte order mark. A file that cannot be opened adds nothing.
func loadGitignore(into *gitignore, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for index := 0; len(data) > 0; index++ {
		var line []byte
		if newline := bytes.IndexByte(data, '\n'); newline >= 0 {
			line, data = data[:newline], data[newline+1:]
			line = bytes.TrimSuffix(line, []byte{'\r'})
		} else {
			line, data = data, nil
		}
		if !utf8.Valid(line) {
			return
		}
		text := string(line)
		if index == 0 {
			text = strings.TrimLeft(text, "\ufeff")
		}
		into.addLine(text)
	}
}

// gitignoreFile is create_gitignore for one file name: its matcher, empty when the file is absent.
func gitignoreFile(path string) *gitignore {
	result := &gitignore{}
	if _, err := os.Stat(path); err == nil {
		loadGitignore(result, path)
	}
	return result
}

// homeDir is std::env::home_dir.
func homeDir() (string, bool) {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home, true
	}
	if current, err := user.Current(); err == nil && current.HomeDir != "" {
		return current.HomeDir, true
	}
	return "", false
}

// excludesFilePattern is `(?im-u)^\s*excludesfile\s*=\s*"?\s*(\S+?)\s*"?\s*$`: a case-insensitive match of ASCII letters only, and
// \s the ASCII white space of a non-Unicode Rust regex.
var excludesFilePattern = regexp.MustCompile(`(?m)^[\t\n\v\f\r ]*[eE][xX][cC][lL][uU][dD][eE][sS][fF][iI][lL][eE][\t\n\v\f\r ]*=[\t\n\v\f\r ]*"?[\t\n\v\f\r ]*([^\t\n\v\f\r ]+?)[\t\n\v\f\r ]*"?[\t\n\v\f\r ]*$`)

// parseExcludesFile is parse_excludes_file: git's core.excludesfile from raw configuration text, `~` replaced by the home directory.
func parseExcludesFile(data []byte) (string, bool) {
	match := excludesFilePattern.FindSubmatch(data)
	if match == nil || !utf8.Valid(match[1]) {
		return "", false
	}
	path := string(match[1])
	if home, ok := homeDir(); ok {
		path = strings.ReplaceAll(path, "~", home)
	}
	return path, true
}

func readConfig(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return data
}

// configHome is $XDG_CONFIG_HOME, else $HOME/.config.
func configHome() (string, bool) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return xdg, true
	}
	if home, ok := homeDir(); ok {
		return filepath.Join(home, ".config"), true
	}
	return "", false
}

// gitconfigExcludesPath is gitconfig_excludes_path: where the global excludes file is, by GIT_CONFIG_GLOBAL, ~/.gitconfig,
// $XDG_CONFIG_HOME/git/config, the system configuration, then the default $XDG_CONFIG_HOME/git/ignore.
func gitconfigExcludesPath() (string, bool) {
	if global := os.Getenv("GIT_CONFIG_GLOBAL"); global != "" {
		if data := readConfig(global); data != nil {
			if path, ok := parseExcludesFile(data); ok {
				return path, true
			}
		}
	}
	if home, ok := homeDir(); ok {
		if data := readConfig(filepath.Join(home, ".gitconfig")); data != nil {
			if path, ok := parseExcludesFile(data); ok {
				return path, true
			}
		}
	}
	configDirectory, haveConfigDirectory := configHome()
	if haveConfigDirectory {
		if data := readConfig(filepath.Join(configDirectory, "git", "config")); data != nil {
			if path, ok := parseExcludesFile(data); ok {
				return path, true
			}
		}
	}
	system := os.Getenv("GIT_CONFIG_SYSTEM")
	if system == "" {
		system = "/etc/gitconfig"
	}
	if data := readConfig(system); data != nil {
		if path, ok := parseExcludesFile(data); ok {
			return path, true
		}
	}
	if haveConfigDirectory {
		return filepath.Join(configDirectory, "git", "ignore"), true
	}
	return "", false
}

// globalGitignore is GitignoreBuilder::build_global: the global excludes file when it is a file.
func globalGitignore() *gitignore {
	result := &gitignore{}
	path, ok := gitconfigExcludesPath()
	if !ok {
		return result
	}
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return result
	}
	loadGitignore(result, path)
	return result
}

// directoryIgnore is the Ignore of one directory (add_child_path): its `.gitignore`, its `.git/info/exclude`, and whether
// it is the top of a work tree.
type directoryIgnore struct {
	// relative is the directory's path below the listed root, "" for the root itself; for a parent of the root it is
	// the path from that parent to the root, so a listed path's relative path is the concatenation.
	relative  string
	gitignore *gitignore
	exclude   *gitignore
	hasGit    bool
}

// newDirectoryIgnore is Ignore::add_child_path for dir: `.git` (a directory, or a file of a linked work tree) or `.jj`
// marks a work tree.
func newDirectoryIgnore(dir string, relative string) *directoryIgnore {
	gitInfo, gitErr := os.Stat(filepath.Join(dir, ".git"))
	_, jjErr := os.Stat(filepath.Join(dir, ".jj"))
	ignore := &directoryIgnore{
		relative:  relative,
		gitignore: gitignoreFile(filepath.Join(dir, ".gitignore")),
		hasGit:    gitErr == nil || jjErr == nil,
	}
	ignore.exclude = &gitignore{}
	if gitDirectory, ok := resolveGitCommonDirectory(dir, gitInfo, gitErr == nil); ok {
		if _, err := os.Stat(filepath.Join(gitDirectory, "info", "exclude")); err == nil {
			loadGitignore(ignore.exclude, filepath.Join(gitDirectory, "info", "exclude"))
		}
	}
	return ignore
}

// resolveGitCommonDirectory is resolve_git_commondir: dir/.git, or for a `.git` file of a linked work tree the common
// directory its `gitdir:` points to; false when the file does not say.
func resolveGitCommonDirectory(dir string, info os.FileInfo, exists bool) (string, bool) {
	gitPath := filepath.Join(dir, ".git")
	if !exists || !info.Mode().IsRegular() {
		return gitPath, true
	}
	data, err := os.ReadFile(gitPath)
	if err != nil {
		return "", false
	}
	first, _, _ := strings.Cut(string(data), "\n")
	first = strings.TrimSuffix(first, "\r")
	gitDirectory, ok := strings.CutPrefix(first, "gitdir: ")
	if !ok {
		return "", false
	}
	commonFile, err := os.ReadFile(filepath.Join(gitDirectory, "commondir"))
	if err != nil {
		return "", false
	}
	common, _, _ := strings.Cut(string(commonFile), "\n")
	common = strings.TrimSuffix(common, "\r")
	if strings.HasPrefix(common, ".") {
		return filepath.Join(gitDirectory, common), true
	}
	return common, true
}

// ignoreChain is the matchers a listing consults: the directories from the one holding an entry up to the listed root,
// then the root's parents up to the file system root, and the global excludes file.
type ignoreChain struct {
	// directories runs from the listed root down to the current directory.
	directories []*directoryIgnore
	// parents runs from the root's nearest parent up; relative is the root's path below that parent, with a trailing slash.
	parents []*directoryIgnore
	global  *gitignore
	// parentsGit is whether a parent of the root is the top of a work tree.
	parentsGit bool
}

// anyGit is whether the directory holding the entry is inside a work tree: it or a directory above it is the top of one.
func (c *ignoreChain) anyGit() bool {
	if c.parentsGit {
		return true
	}
	for _, directory := range c.directories {
		if directory.hasGit {
			return true
		}
	}
	return false
}

// matched is Ignore::matched_ignore for the entry at path (relative to the listed root, with / separators): the
// closest `.gitignore` that matches decides, else the closest exclude file, else the global excludes file; ancestors above
// the top of a work tree are not consulted.
func (c *ignoreChain) matched(path string, isDir bool) matchKind {
	var fromGitignore, fromExclude matchKind
	anyGit := c.anyGit()
	sawGit := false
	consult := func(ignore *directoryIgnore, relative string) {
		if anyGit && !sawGit && fromGitignore == matchNone {
			fromGitignore = ignore.gitignore.matched(relative, isDir)
		}
		if anyGit && !sawGit && fromExclude == matchNone {
			fromExclude = ignore.exclude.matched(relative, isDir)
		}
		sawGit = sawGit || ignore.hasGit
	}
	for _, ignore := range slices.Backward(c.directories) {

		consult(ignore, strings.TrimPrefix(strings.TrimPrefix(path, ignore.relative), "/"))
	}
	for _, parent := range c.parents {
		consult(parent, parent.relative+path)
	}
	global := matchNone
	if anyGit {
		global = c.global.matched(path, isDir)
	}
	return fromGitignore.or(fromExclude).or(global)
}
