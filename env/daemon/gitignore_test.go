package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pi source: the `ignore` crate 0.4.33 (the version Cargo.lock locks), gitignore.rs mod tests: the table of patterns and paths. The paths are
// relative to the ignore file, as gitignore.matched receives them from the chain; the cases of the crate that test its
// root-prefix stripping (ig23, ig25, ig26, ig28, ig33-ig37, ignot14) are not here, because the chain computes relative paths itself.
var gitignoreCases = []struct {
	name       string
	patterns   string
	path       string
	isDir      bool
	wantIgnore bool
}{
	{"ig1", "months", "months", false, true},
	{"ig2", "*.lock", "Cargo.lock", false, true},
	{"ig3", "*.rs", "src/main.rs", false, true},
	{"ig4", "src/*.rs", "src/main.rs", false, true},
	{"ig5", "/*.c", "cat-file.c", false, true},
	{"ig6", "/src/*.rs", "src/main.rs", false, true},
	{"ig7", "!src/main.rs\n*.rs", "src/main.rs", false, true},
	{"ig8", "foo/", "foo", true, true},
	{"ig9", "**/foo", "foo", false, true},
	{"ig10", "**/foo", "src/foo", false, true},
	{"ig11", "**/foo/**", "src/foo/bar", false, true},
	{"ig12", "**/foo/**", "wat/src/foo/bar/baz", false, true},
	{"ig13", "**/foo/bar", "foo/bar", false, true},
	{"ig14", "**/foo/bar", "src/foo/bar", false, true},
	{"ig15", "abc/**", "abc/x", false, true},
	{"ig16", "abc/**", "abc/x/y", false, true},
	{"ig17", "abc/**", "abc/x/y/z", false, true},
	{"ig18", "a/**/b", "a/b", false, true},
	{"ig19", "a/**/b", "a/x/b", false, true},
	{"ig20", "a/**/b", "a/x/y/b", false, true},
	{"ig21", `\!xy`, "!xy", false, true},
	{"ig22", `\#foo`, "#foo", false, true},
	{"ig24", "target", "grep/target", false, true},
	{"ig27", "foo/", "xyz/foo", true, true},
	{"ig29", "node_modules/ ", "node_modules", true, true},
	{"ig30", "**/", "foo/bar", true, true},
	{"ig31", "path1/*", "path1/foo", false, true},
	{"ig32", ".a/b", ".a/b", false, true},
	{"ig38", "\\[", "[", false, true},
	{"ig39", "\\?", "?", false, true},
	{"ig40", "\\*", "*", false, true},
	{"ig41", "\\a", "a", false, true},
	{"ig42", "s*.rs", "sfoo.rs", false, true},
	{"ig43", "**", "foo.rs", false, true},
	{"ig44", "**/**/*", "a/foo.rs", false, true},
	{"ignot1", "amonths", "months", false, false},
	{"ignot2", "monthsa", "months", false, false},
	{"ignot3", "/src/*.rs", "src/grep/src/main.rs", false, false},
	{"ignot4", "/*.c", "mozilla-sha1/sha1.c", false, false},
	{"ignot5", "/src/*.rs", "src/grep/src/main.rs", false, false},
	{"ignot6", "*.rs\n!src/main.rs", "src/main.rs", false, false},
	{"ignot7", "foo/", "foo", false, false},
	{"ignot8", "**/foo/**", "wat/src/afoo/bar/baz", false, false},
	{"ignot9", "**/foo/**", "wat/src/fooa/bar/baz", false, false},
	{"ignot10", "**/foo/bar", "foo/src/bar", false, false},
	{"ignot11", "#foo", "#foo", false, false},
	{"ignot12", "\n\n\n", "foo", false, false},
	{"ignot13", "foo/**", "foo", true, false},
	{"ignot15", "!/bar", "foo/bar", false, false},
	{"ignot16", "*\n!**/", "foo", true, false},
	{"ignot17", "src/*.rs", "src/grep/src/main.rs", false, false},
	{"ignot18", "path1/*", "path2/path1/foo", false, false},
	{"ignot19", "s*.rs", "src/foo.rs", false, false},
	{"cs1", "*.html", "foo.html", false, true},
	{"cs2", "*.html", "foo.HTML", false, false},
	{"cs3", "*.html", "foo.htm", false, false},
	{"cs4", "*.html", "foo.HTM", false, false},
}

func TestGitignoreMatchesTheCratesTable(t *testing.T) {
	for _, test := range gitignoreCases {
		ignore := &gitignore{}
		for line := range strings.SplitSeq(strings.TrimSuffix(test.patterns, "\n"), "\n") {
			ignore.addLine(line)
		}
		if got := ignore.matched(test.path, test.isDir) == matchIgnore; got != test.wantIgnore {
			t.Errorf("%s: patterns %q, path %q (dir %v): ignored = %v, want %v", test.name, test.patterns, test.path, test.isDir, got, test.wantIgnore)
		}
	}
}

// Pi source: gitignore.rs parse_excludes_file1-5 and expand_tilde.
func TestParseExcludesFileReadsGitsExcludesFile(t *testing.T) {
	t.Setenv("HOME", "/home/someone")
	t.Setenv("USERPROFILE", "/home/someone") // os.UserHomeDir reads this on Windows
	for _, test := range []struct {
		name, config, want string
		found              bool
	}{
		{"plain", "[core]\nexcludesFile = /foo/bar", "/foo/bar", true},
		{"tilde", "[core]\nexcludesFile = ~/foo/bar", "/home/someone/foo/bar", true},
		{"another key", "[core]\nexcludeFile = /foo/bar", "", false},
		{"quoted", "[core]\nexcludesFile = \"~/foo/bar\"", "/home/someone/foo/bar", true},
		{"quoted with a space inside", "[core]\nexcludesFile = \" \"~/foo/bar \" \"", "", false},
		{"first of two", "excludesfile = /first\n[core]\n  EXCLUDESFILE=/second\n", "/first", true},
		{"indented, CRLF", "[core]\r\n\texcludesFile =  /with/crlf  \r\n", "/with/crlf", true},
	} {
		got, found := parseExcludesFile([]byte(test.config))
		if got != test.want || found != test.found {
			t.Errorf("%s: parseExcludesFile = %q, %v; want %q, %v", test.name, got, found, test.want, test.found)
		}
	}
}

// Pi source: gitignore.rs regression_106 and add (a leading byte order mark on the first line only, lines up to the first one
// that is not UTF-8) and add_line (a comment, trailing space unless escaped, a pattern that is no glob).
func TestGitignoreFileParsing(t *testing.T) {
	directory := t.TempDir()
	write := func(name string, content []byte) *gitignore {
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
		return gitignoreFile(path)
	}
	for _, test := range []struct {
		name    string
		content string
		path    string
		want    matchKind
	}{
		{"a space is no pattern", " ", " ", matchNone},
		{"a byte order mark before the first line", "\ufeff*.bom\n", "x.bom", matchIgnore},
		{"a byte order mark on a later line is part of it", "*.a\n\ufeff*.bom\n", "x.bom", matchNone},
		{"a CRLF line ends without the CR", "*.crlf\r\n", "x.crlf", matchIgnore},
		{"trailing spaces are dropped", "name  \n", "name", matchIgnore},
		{"an escaped trailing space is kept", "name\\ \n", "name ", matchIgnore},
		{"a comment", "#x\n", "#x", matchNone},
		{"an escaped hash", "\\#x\n", "#x", matchIgnore},
		{"a negated escaped bang", "!\\!x\n", "!x", matchWhitelist},
		{"the last matching line decides", "*.o\n!keep.o\n*.o\n", "keep.o", matchIgnore},
		{"an invalid range drops the line", "[z-a]\n*.k\n", "z", matchNone},
		{"an unclosed alternate drops the line", "{a,b\n*.k\n", "x.k", matchIgnore},
		{"an unclosed class is a literal bracket", "[abc\n", "[abc", matchIgnore},
		{"a dangling escape drops the line", "x\\\n*.k\n", "x", matchNone},
	} {
		ignore := write("file", []byte(test.content))
		if got := ignore.matched(test.path, false); got != test.want {
			t.Errorf("%s: %q matched against %q = %v, want %v", test.name, test.content, test.path, got, test.want)
		}
	}
	// A line that is not UTF-8 ends the file: the lines before it stay, the lines after are lost.
	ignore := write("binary", []byte("*.one\n\xff\n*.two\n"))
	if ignore.matched("a.one", false) != matchIgnore || ignore.matched("a.two", false) != matchNone {
		t.Errorf("a file with an invalid line: .one %v, .two %v; want ignore, none", ignore.matched("a.one", false), ignore.matched("a.two", false))
	}
	if ignore := gitignoreFile(filepath.Join(directory, "missing")); !ignore.empty() {
		t.Errorf("a missing file matched %v", ignore.globs)
	}
}
