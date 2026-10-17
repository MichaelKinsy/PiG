package env

// Differential test of the daemon's `listFiles` against Pi's own Rust daemon (the `ignore` crate): generated trees with
// random ignore files, work-tree markers, hidden names, links and non-UTF-8 names must list byte for byte alike under every
// combination of options.

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// ignorePatterns are gitignore lines of every syntax the crate's glob parser distinguishes.
var ignorePatterns = []string{
	"*.log", "!keep.log", "/root.txt", "build/", "/build", "out", "**/cache", "cache/**", "a/**/z", "**/deep/*", "*.tmp\\ ", "trailing  ",
	"\\#hash", "\\!bang", "#comment", "!important/", "[ab]*.txt", "[!ab]?.md", "[a-c]", "[^x]*.dat", "*.{js,ts}", "{src,lib}/*.go", "x{,y}z",
	"?.c", "a?b", "**", "/**", "**/", "foo/**/bar", "f*o", "docs/*/index.html", "!docs/keep/**", "\\*star", "dir\\/", "[", "[]a]", "{unclosed", "}",
	"é*", "[é]", "*.ö", ".hidden*", "!.keep", "node_modules", "*~", "/*.c", "sub/", "sub/**/x.txt", "**/sub/x.txt", "a/b", "/a/b/", "bar/baz/",
	"\ufeff*.bom", "a[!x]b", "[^x]b", "a[x/]b", "*.CASE", "weird name*", "name with space", "-dash", "--", "a**b", "**a", "a**", "***", "a/***/b", "*/", "/", "!", "!!x", "\\",
}

var treeNames = []string{
	"a", "b", "c", "x", "z", "A", "foo", "bar", "baz", "src", "lib", "docs", "out", "cache", "deep", "build", "important", "keep",
	"index.html", "main.go", "app.js", "app.ts", "keep.log", "debug.log", "root.txt", "a.txt", "b.txt", "c.md", "d.md", "x.dat", "y.dat",
	".hidden", ".keep", ".config", "é.txt", "ö", "x.ö", "name with space", "weird name.txt", "-dash", "file~", "m.c", "a.b", "axb", "xz", "xyz",
	"node_modules", "sub", "dir", "star", "*star", "#hash", "!bang", "trail", "trailing", "end.tmp", "bom.bom", "a.CASE", "a.case", "f.o", "foo.",
}

type generatedTree struct {
	root string
	// inner is the directory listed: root, or a directory below it.
	inner string
}

func writeGenerated(t *testing.T, random *rand.Rand, seed int) generatedTree {
	t.Helper()
	root := filepath.Join(t.TempDir(), "tree")
	mustDo(os.MkdirAll(root, 0o755))
	var directories []string
	directories = append(directories, root)
	var make func(dir string, depth int)
	make = func(dir string, depth int) {
		count := 2 + random.IntN(6)
		for range count {
			name := treeNames[random.IntN(len(treeNames))]
			// APFS refuses names that are not UTF-8 (EILSEQ), so macOS trees have none.
			if random.IntN(40) == 0 && runtime.GOOS != "darwin" {
				name = "bad\xffname"
			}
			if random.IntN(60) == 0 && runtime.GOOS != "darwin" {
				name = "cut\xe2\x82"
			}
			path := filepath.Join(dir, name)
			if _, err := os.Lstat(path); err == nil {
				continue
			}
			switch {
			case depth < 4 && random.IntN(3) == 0:
				if os.Mkdir(path, 0o755) == nil {
					directories = append(directories, path)
					make(path, depth+1)
				}
			case random.IntN(12) == 0:
				testenv.Symlink(t, treeNames[random.IntN(len(treeNames))], path)
			case random.IntN(30) == 0:
				testenv.Symlink(t, ".", path)
			case random.IntN(50) == 0:
				_ = os.WriteFile(path, nil, 0o000)
			default:
				_ = os.WriteFile(path, nil, 0o644)
			}
		}
	}
	make(root, 0)
	lines := func(max int) string {
		var text strings.Builder
		for range random.IntN(max + 1) {
			text.WriteString(ignorePatterns[random.IntN(len(ignorePatterns))])
			text.WriteString([]string{"\n", "\n", "\r\n", "\n\n"}[random.IntN(4)])
		}
		return text.String()
	}
	for _, directory := range directories {
		if random.IntN(2) == 0 {
			mustDo(os.WriteFile(filepath.Join(directory, ".gitignore"), []byte(lines(6)), 0o644))
		}
		switch random.IntN(8) {
		case 0:
			mustDo(os.MkdirAll(filepath.Join(directory, ".git", "info"), 0o755))
			mustDo(os.WriteFile(filepath.Join(directory, ".git", "info", "exclude"), []byte(lines(4)), 0o644))
		case 1:
			mustDo(os.MkdirAll(filepath.Join(directory, ".git"), 0o755))
		case 2:
			if directory != root {
				mustDo(os.WriteFile(filepath.Join(directory, ".git"), []byte("not a gitdir line\n"), 0o644))
			}
		case 3:
			mustDo(os.MkdirAll(filepath.Join(directory, ".jj"), 0o755))
		}
	}
	tree := generatedTree{root: root, inner: directories[random.IntN(len(directories))]}
	if seed%3 == 0 {
		// A work tree above the listed directory: its marker, ignore file and exclude file apply to the parents.
		mustDo(os.MkdirAll(filepath.Join(root, ".git", "info"), 0o755))
		mustDo(os.WriteFile(filepath.Join(root, ".gitignore"), []byte(lines(6)), 0o644))
	}
	if seed%5 == 0 {
		// The global excludes file, named by the configuration, with `~` in it.
		home := os.Getenv("HOME")
		mustDo(os.WriteFile(filepath.Join(home, "global-ignore"), []byte(lines(6)), 0o644))
		mustDo(os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[core]\n\texcludesfile = ~/global-ignore\n"), 0o644))
	} else {
		_ = os.Remove(filepath.Join(os.Getenv("HOME"), ".gitconfig"))
	}
	return tree
}

// TestListFilesMatchesPisDaemon lists generated trees with both daemons.
func TestListFilesMatchesPisDaemon(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("unreadable files are readable by root")
	}
	pi := oracleDaemonWithList(t)
	isolateGitConfiguration(t)
	ours := NewConnection(ConnectionOptions{Command: []string{daemonBinary(t)}})
	theirs := NewConnection(ConnectionOptions{Command: []string{pi}})
	t.Cleanup(ours.Close)
	t.Cleanup(theirs.Close)
	optionSets := []Json{
		{}, {"hidden": false}, {"gitignore": false}, {"hidden": false, "gitignore": false}, {"exclude": []string{"a", "node_modules", "é.txt"}},
		{"maxEntries": 7}, {"maxBytes": 40}, {"maxEntries": 0},
	}
	for seed := range 400 {
		random := rand.New(rand.NewPCG(uint64(seed), 77))
		tree := writeGenerated(t, random, seed)
		for _, options := range optionSets {
			request := Json{"path": tree.inner}
			maps.Copy(request, options)
			want, wantErr := theirs.Request(background, "listFiles", request, RequestOptions{})
			got, gotErr := ours.Request(background, "listFiles", request, RequestOptions{})
			if (wantErr == nil) != (gotErr == nil) || (wantErr != nil && wantErr.Error() != gotErr.Error()) {
				t.Fatalf("seed %d %v: Pi %v, PiG %v", seed, options, wantErr, gotErr)
			}
			if wantErr != nil {
				continue
			}
			if !bytes.Equal(got.Payload, want.Payload) || got.JSON["count"] != want.JSON["count"] || got.JSON["truncated"] != want.JSON["truncated"] {
				t.Fatalf("seed %d options %v under %s (list %s)\nPi  : %q %v\nPiG : %q %v", seed, options, tree.root, tree.inner, want.Payload, want.JSON, got.Payload, got.JSON)
			}
		}
	}
}

// oracleDaemonWithList is Pi's daemon, or a skip when the pinned Pi has no `listFiles`.
func oracleDaemonWithList(t *testing.T) string {
	t.Helper()
	if _, err := os.Stat(filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "env", "daemon", "src", "list.rs")); err != nil {
		t.Skipf("the pinned Pi has no listFiles (%s is absent)", "list.rs")
	}
	return oracleDaemon(t)
}

// TestListFilesIgnoreFilesMatchPisDaemon lists one fixed tree with each ignore file content of a table, so a rule of the crate's
// parser or glob dialect that the generated trees do not reach is still compared.
func TestListFilesIgnoreFilesMatchPisDaemon(t *testing.T) {
	pi := oracleDaemonWithList(t)
	isolateGitConfiguration(t)
	ours := NewConnection(ConnectionOptions{Command: []string{daemonBinary(t)}})
	theirs := NewConnection(ConnectionOptions{Command: []string{pi}})
	t.Cleanup(ours.Close)
	t.Cleanup(theirs.Close)
	root := filepath.Join(t.TempDir(), "tree")
	mustDo(os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	var names []string
	for _, name := range treeNames {
		names = append(names, name, filepath.Join("a", name), filepath.Join("a", "b", name), filepath.Join("docs", "x", name), filepath.Join("sub", "deep", name), filepath.Join("foo", "bar", "baz", name))
	}
	names = append(names, "a/b", "a/x/b", "a/x/y/b", "foo/bar", "foo/x/bar", "abc/x", "abc/x/y", "q.html", "Q.HTML", "[abc", "a?b", "a/b/c/z", "a/z", "a/q/z", "xz", "xyz", "a.tmp", "a.tmp ", "[", "]a", "a]", "}", "{", "-", "--", "a**b", "a/x/b", "a.one", "a.two", "x.k", "z.k")
	// Deeper paths first, so a name that is a directory in one place is not made a file by a shorter path.
	slices.SortStableFunc(names, func(a, b string) int { return strings.Count(b, "/") - strings.Count(a, "/") })
	for _, name := range names {
		// A name that is a file in one place and a directory in another is whichever came first.
		path := filepath.Join(root, name)
		if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
			continue
		}
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			continue
		}
		_ = os.WriteFile(path, nil, 0o644)
	}
	contents := append([]string(nil), ignorePatterns...)
	contents = append(contents,
		"\ufeff*.bom\n", "*.a\n\ufeff*.bom\n", "*.crlf\r\n", "name  \n", "name\\ \n", "!\\!x\n", "*.o\n!keep.o\n*.o\n", "[z-a]\n*.k\n", "{a,b\n*.k\n",
		"[abc\n", "x\\\n*.k\n", "*.one\n\xff\n*.two\n", "*\n!*/\n!*.go\n", "/*\n!/a/\n", "a/\n!a/b/\nb\n", "docs\n!docs/x\n", "*.log\n!/important/\n",
		"/a\n!a/b\n", "**/b/**\n!**/b/c\n", "foo/**/baz\n", "foo/**\n!foo/bar\n", "**/foo/bar\n",
	)
	for index, content := range contents {
		mustDo(os.WriteFile(filepath.Join(root, ".gitignore"), []byte(content), 0o644))
		for _, options := range []Json{{}, {"hidden": false}} {
			request := Json{"path": root}
			maps.Copy(request, options)
			want, wantErr := theirs.Request(background, "listFiles", request, RequestOptions{})
			got, gotErr := ours.Request(background, "listFiles", request, RequestOptions{})
			if wantErr != nil || gotErr != nil {
				t.Fatalf("ignore file %d %q: Pi %v, PiG %v", index, content, wantErr, gotErr)
			}
			if !bytes.Equal(got.Payload, want.Payload) {
				wantEntries, gotEntries := strings.Split(string(want.Payload), "\x00"), strings.Split(string(got.Payload), "\x00")
				t.Fatalf("ignore file %d %q options %v: Pi listed %d entries, PiG %d; first difference %s", index, content, options, len(wantEntries), len(gotEntries), firstDifference(wantEntries, gotEntries))
			}
		}
	}
}

func firstDifference(want, got []string) string {
	for index := range min(len(want), len(got)) {
		if want[index] != got[index] {
			return fmt.Sprintf("at %d: Pi %q, PiG %q", index, want[index], got[index])
		}
	}
	return fmt.Sprintf("one list ends at %d", min(len(want), len(got)))
}

// TestListFilesExcludeSourcesMatchPisDaemon compares the places an exclude rule can come from besides `.gitignore`: the
// global excludes file by each of the ways Git configures it, and `.git/info/exclude` of a linked work tree.
func TestListFilesExcludeSourcesMatchPisDaemon(t *testing.T) {
	pi := oracleDaemonWithList(t)
	for _, scenario := range []struct {
		name  string
		setup func(t *testing.T, home, root string)
	}{
		{"XDG default ignore file", func(t *testing.T, home, root string) {
			mustDo(os.MkdirAll(filepath.Join(home, "config", "git"), 0o755))
			mustDo(os.WriteFile(filepath.Join(home, "config", "git", "ignore"), []byte("*.g\n!keep.g\n"), 0o644))
		}},
		{"core.excludesfile in ~/.gitconfig", func(t *testing.T, home, root string) {
			mustDo(os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[core]\n\texcludesfile = \"~/my-excludes\"\n"), 0o644))
			mustDo(os.WriteFile(filepath.Join(home, "my-excludes"), []byte("*.g\n"), 0o644))
		}},
		{"core.excludesfile in the XDG config", func(t *testing.T, home, root string) {
			mustDo(os.MkdirAll(filepath.Join(home, "config", "git"), 0o755))
			mustDo(os.WriteFile(filepath.Join(home, "config", "git", "config"), []byte("[core]\nexcludesFile = "+filepath.Join(home, "xdg-excludes")+"\n"), 0o644))
			mustDo(os.WriteFile(filepath.Join(home, "xdg-excludes"), []byte("*.g\n"), 0o644))
		}},
		{"GIT_CONFIG_GLOBAL replaces the other configuration", func(t *testing.T, home, root string) {
			mustDo(os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[core]\nexcludesfile = "+filepath.Join(home, "ignored-excludes")+"\n"), 0o644))
			mustDo(os.WriteFile(filepath.Join(home, "ignored-excludes"), []byte("*.h\n"), 0o644))
			mustDo(os.WriteFile(filepath.Join(home, "global-config"), []byte("[core]\nexcludesfile = "+filepath.Join(home, "chosen-excludes")+"\n"), 0o644))
			mustDo(os.WriteFile(filepath.Join(home, "chosen-excludes"), []byte("*.g\n"), 0o644))
			t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "global-config"))
		}},
		{"the system configuration", func(t *testing.T, home, root string) {
			mustDo(os.WriteFile(filepath.Join(home, "system-config"), []byte("[core]\nexcludesfile = "+filepath.Join(home, "system-excludes")+"\n"), 0o644))
			mustDo(os.WriteFile(filepath.Join(home, "system-excludes"), []byte("*.g\n"), 0o644))
			t.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(home, "system-config"))
		}},
		{"a global excludes file outside a work tree is not applied", func(t *testing.T, home, root string) {
			mustDo(os.RemoveAll(filepath.Join(root, ".git")))
			mustDo(os.MkdirAll(filepath.Join(home, "config", "git"), 0o755))
			mustDo(os.WriteFile(filepath.Join(home, "config", "git", "ignore"), []byte("*.g\n"), 0o644))
		}},
		{"a linked work tree's common directory", func(t *testing.T, home, root string) {
			common := filepath.Join(home, "common.git")
			mustDo(os.MkdirAll(filepath.Join(common, "info"), 0o755))
			mustDo(os.WriteFile(filepath.Join(common, "info", "exclude"), []byte("*.g\n"), 0o644))
			linked := filepath.Join(home, "linked.git")
			mustDo(os.MkdirAll(linked, 0o755))
			mustDo(os.WriteFile(filepath.Join(linked, "commondir"), []byte(common+"\n"), 0o644))
			mustDo(os.RemoveAll(filepath.Join(root, ".git")))
			mustDo(os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+linked+"\n"), 0o644))
		}},
		{"a relative common directory", func(t *testing.T, home, root string) {
			linked := filepath.Join(home, "linked.git")
			mustDo(os.MkdirAll(linked, 0o755))
			mustDo(os.MkdirAll(filepath.Join(linked, "..", "rel-common", "info"), 0o755))
			mustDo(os.WriteFile(filepath.Join(linked, "..", "rel-common", "info", "exclude"), []byte("*.g\n"), 0o644))
			mustDo(os.WriteFile(filepath.Join(linked, "commondir"), []byte("../rel-common\n"), 0o644))
			mustDo(os.RemoveAll(filepath.Join(root, ".git")))
			mustDo(os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+linked+"\n"), 0o644))
		}},
		{"a .git file without a commondir has no exclude file", func(t *testing.T, home, root string) {
			linked := filepath.Join(home, "linked.git")
			mustDo(os.MkdirAll(filepath.Join(linked, "info"), 0o755))
			mustDo(os.WriteFile(filepath.Join(linked, "info", "exclude"), []byte("*.g\n"), 0o644))
			mustDo(os.RemoveAll(filepath.Join(root, ".git")))
			mustDo(os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+linked+"\n"), 0o644))
		}},
		{"Jujutsu marks a work tree", func(t *testing.T, home, root string) {
			mustDo(os.RemoveAll(filepath.Join(root, ".git")))
			mustDo(os.MkdirAll(filepath.Join(root, ".jj"), 0o755))
			mustDo(os.WriteFile(filepath.Join(root, ".gitignore"), []byte("*.g\n"), 0o644))
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			isolateGitConfiguration(t)
			home := os.Getenv("HOME")
			root := filepath.Join(t.TempDir(), "tree")
			mustDo(os.MkdirAll(filepath.Join(root, ".git"), 0o755))
			for _, name := range []string{"a.g", "keep.g", "b.h", "c.txt", "sub/d.g", "sub/e.h"} {
				mustDo(os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755))
				mustDo(os.WriteFile(filepath.Join(root, name), nil, 0o644))
			}
			scenario.setup(t, home, root)
			ours := NewConnection(ConnectionOptions{Command: []string{daemonBinary(t)}})
			theirs := NewConnection(ConnectionOptions{Command: []string{pi}})
			t.Cleanup(ours.Close)
			t.Cleanup(theirs.Close)
			want := must(theirs.Request(background, "listFiles", Json{"path": root}, RequestOptions{}))
			got := must(ours.Request(background, "listFiles", Json{"path": root}, RequestOptions{}))
			if !bytes.Equal(got.Payload, want.Payload) {
				t.Fatalf("Pi listed %q, PiG %q", want.Payload, got.Payload)
			}
			if !strings.Contains(t.Name(), "outside") && !strings.Contains(t.Name(), "no_exclude") && !strings.Contains(t.Name(), "Jujutsu") && strings.Contains(string(got.Payload), "a.g") {
				t.Errorf("the scenario's exclude rule had no effect: %q", got.Payload)
			}
		})
	}
}

// TestListFilesErrorsMatchPisDaemon compares the failures: a missing or non-directory root and invalid fields.
func TestListFilesErrorsMatchPisDaemon(t *testing.T) {
	pi := oracleDaemonWithList(t)
	isolateGitConfiguration(t)
	ours := NewConnection(ConnectionOptions{Command: []string{daemonBinary(t)}})
	theirs := NewConnection(ConnectionOptions{Command: []string{pi}})
	t.Cleanup(ours.Close)
	t.Cleanup(theirs.Close)
	directory := t.TempDir()
	mustDo(os.WriteFile(filepath.Join(directory, "file"), nil, 0o644))
	for name, request := range map[string]Json{
		"missing root":          {"path": filepath.Join(directory, "missing")},
		"file root":             {"path": filepath.Join(directory, "file")},
		"no path":               {},
		"path not a string":     {"path": 3},
		"negative maxEntries":   {"path": directory, "maxEntries": -1},
		"fractional maxBytes":   {"path": directory, "maxBytes": 1.5},
		"string timeoutMs":      {"path": directory, "timeoutMs": "1"},
		"exclude not an array":  {"path": directory, "exclude": "a"},
		"exclude with a number": {"path": directory, "exclude": []any{"a", 1}},
		"root below a file":     {"path": filepath.Join(directory, "file", "x")},
		"hidden not a boolean":  {"path": directory, "hidden": "no"},
		"huge maxBytes":         {"path": directory, "maxBytes": 1e15},
		"explicit nulls":        {"path": directory, "maxEntries": nil, "exclude": nil, "gitignore": nil},
	} {
		want, wantErr := theirs.Request(background, "listFiles", request, RequestOptions{})
		got, gotErr := ours.Request(background, "listFiles", request, RequestOptions{})
		if fmt.Sprint(wantErr) != fmt.Sprint(gotErr) || !bytes.Equal(want.Payload, got.Payload) {
			t.Errorf("%s: Pi %v %q, PiG %v %q", name, wantErr, want.Payload, gotErr, got.Payload)
			continue
		}
		wantRemote, wantOK := errors.AsType[*RemoteError](wantErr)
		gotRemote, gotOK := errors.AsType[*RemoteError](gotErr)
		if wantOK != gotOK || (wantOK && (wantRemote.Code != gotRemote.Code || pathOf(wantRemote) != pathOf(gotRemote))) {
			t.Errorf("%s: Pi %+v, PiG %+v", name, wantRemote, gotRemote)
		}
	}
}

// pathOf is the `path` a remote error reports, or "<none>".
func pathOf(err *RemoteError) string {
	if err.Path == nil {
		return "<none>"
	}
	return *err.Path
}
