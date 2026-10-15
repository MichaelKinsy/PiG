package daemon

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi test: packages/env/daemon/src/list.rs (mod tests). Pi source: list.rs list, Options::from_json, run.

type listTreeFixture struct {
	t    *testing.T
	root string
}

// newListTree makes a tree of the files named (a trailing `/` names a directory), with the global excludes file and every
// Git configuration the daemon would read pointing at nothing.
func newListTree(t *testing.T, files ...string) *listTreeFixture {
	t.Helper()
	isolated := t.TempDir()
	t.Setenv("HOME", isolated)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(isolated, "config"))
	t.Setenv("GIT_CONFIG_GLOBAL", "")
	t.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(isolated, "no-system-gitconfig"))
	tree := &listTreeFixture{t: t, root: filepath.Join(t.TempDir(), "tree")}
	if err := os.MkdirAll(tree.root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		tree.write(file, "")
	}
	return tree
}

func (f *listTreeFixture) write(path, content string) {
	f.t.Helper()
	full := filepath.Join(f.root, filepath.FromSlash(path))
	if strings.HasSuffix(path, "/") {
		if err := os.MkdirAll(full, 0o755); err != nil {
			f.t.Fatal(err)
		}
		return
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func defaultListOptions() listOptions {
	options, failure := parseListOptions(Object{})
	if failure != nil {
		panic(failure)
	}
	return *options
}

// list is the listing as `<kind> <path>` lines and whether it was truncated.
func (f *listTreeFixture) list(options listOptions) ([]string, bool) {
	f.t.Helper()
	return listOf(f.t, f.root, options)
}

func listOf(t *testing.T, root string, options listOptions) ([]string, bool) {
	t.Helper()
	result, failure := listTree(root, &options, func() bool { return false })
	if failure != nil {
		t.Fatalf("listTree(%s): %v", root, failure)
	}
	var entries []string
	for record := range strings.SplitSeq(string(result.payload), "\x00") {
		if record != "" {
			entries = append(entries, record[:1]+" "+record[1:])
		}
	}
	if uint64(len(entries)) != result.count {
		t.Fatalf("count = %d for %d records", result.count, len(entries))
	}
	return entries, result.truncated
}

func TestListFilesListsFilesAndDirectoriesDepthFirstInNameOrder(t *testing.T) {
	tree := newListTree(t, "b.txt", "a/z.txt", "a/b/c.txt", "empty/", ".hidden/x", "A.txt")
	entries, truncated := tree.list(defaultListOptions())
	want := []string{"d .hidden", "f .hidden/x", "f A.txt", "d a", "d a/b", "f a/b/c.txt", "f a/z.txt", "f b.txt", "d empty"}
	if !slices.Equal(entries, want) || truncated {
		t.Fatalf("listing = %q truncated=%v, want %q", entries, truncated, want)
	}
	options := defaultListOptions()
	options.hidden = false
	entries, _ = tree.list(options)
	for _, entry := range entries {
		if strings.Contains(entry, ".hidden") {
			t.Fatalf("hidden entry %q listed with hidden=false", entry)
		}
	}
}

func TestListFilesSkipsWhatGitIgnoresOnlyInWorkTrees(t *testing.T) {
	tree := newListTree(t, "keep.txt", "out/x.js", "sub/keep.txt", "sub/drop.log", "node_modules/m/i.js")
	tree.write(".gitignore", "out/\n*.log\n")
	tree.write("sub/.gitignore", "!keep.txt\n")
	// Not a work tree yet: everything is listed.
	entries, _ := tree.list(defaultListOptions())
	if !slices.Contains(entries, "f out/x.js") {
		t.Fatalf("a tree without .git applied its .gitignore: %q", entries)
	}
	tree.write(".git/HEAD", "ref: refs/heads/main\n")
	tree.write(".git/info/exclude", "keep.txt\n")
	options := defaultListOptions()
	options.exclude = []string{"node_modules"}
	entries, _ = tree.list(options)
	if want := []string{"f .gitignore", "d sub", "f sub/.gitignore", "f sub/keep.txt"}; !slices.Equal(entries, want) {
		t.Fatalf("listing = %q, want %q", entries, want)
	}
	options = defaultListOptions()
	options.gitignore = false
	entries, _ = tree.list(options)
	if !slices.Contains(entries, "f out/x.js") {
		t.Fatalf("gitignore=false still applied the .gitignore: %q", entries)
	}
	for _, entry := range entries {
		if strings.Contains(entry, ".git/") {
			t.Fatalf(".git entry %q listed", entry)
		}
	}
	// A subdirectory of a work tree applies the rules of its parents.
	result, failure := listTree(filepath.Join(tree.root, "sub"), func() *listOptions { o := defaultListOptions(); return &o }(), func() bool { return false })
	if failure != nil {
		t.Fatal(failure)
	}
	if string(result.payload) != "f.gitignore\x00fkeep.txt\x00" {
		t.Fatalf("sub payload = %q", result.payload)
	}
}

func TestListFilesListsSymbolicLinksWithoutFollowingThem(t *testing.T) {
	tree := newListTree(t, "dir/inner.txt")
	for link, target := range map[string]string{"link": "dir", "dangling": "missing", filepath.Join("dir", "loop"): ".."} {
		testenv.Symlink(t, target, filepath.Join(tree.root, link))
	}
	entries, _ := tree.list(defaultListOptions())
	if want := []string{"l dangling", "d dir", "f dir/inner.txt", "l dir/loop", "l link"}; !slices.Equal(entries, want) {
		t.Fatalf("listing = %q, want %q", entries, want)
	}
}

func TestListFilesStopsAtBoundsWithTruncated(t *testing.T) {
	tree := newListTree(t, "a", "b", "c")
	for _, test := range []struct {
		name      string
		options   func(*listOptions)
		want      []string
		truncated bool
	}{
		{"two entries of three", func(o *listOptions) { o.maxEntries = 2 }, []string{"f a", "f b"}, true},
		{"three entries of three", func(o *listOptions) { o.maxEntries = 3 }, []string{"f a", "f b", "f c"}, false},
		{"six bytes: each entry counts its path plus two", func(o *listOptions) { o.maxBytes = 6 }, []string{"f a", "f b"}, true},
		{"no entries", func(o *listOptions) { o.maxEntries = 0 }, nil, true},
		{"timeout zero", func(o *listOptions) { o.timeout = 0 }, nil, true},
	} {
		options := defaultListOptions()
		test.options(&options)
		entries, truncated := tree.list(options)
		if !slices.Equal(entries, test.want) || truncated != test.truncated {
			t.Errorf("%s: %q truncated=%v, want %q truncated=%v", test.name, entries, truncated, test.want, test.truncated)
		}
	}
}

func TestListFilesFailsOnMissingRootsFilesAndAborts(t *testing.T) {
	tree := newListTree(t, "file")
	options := defaultListOptions()
	for name, test := range map[string]struct {
		path, code string
		aborted    bool
	}{
		"missing root":  {filepath.Join(tree.root, "missing"), "ENOENT", false},
		"file as root":  {filepath.Join(tree.root, "file"), "ENOTDIR", false},
		"aborted first": {tree.root, "aborted", true},
	} {
		_, failure := listTree(test.path, &options, func() bool { return test.aborted })
		if failure == nil || failure.Code != test.code {
			t.Errorf("%s: failure = %v, want code %s", name, failure, test.code)
		}
	}
}

// docs/protocol.md: a cancel stops listFiles between entries, with `aborted`.
func TestListFilesStopsBetweenEntriesWhenCancelled(t *testing.T) {
	tree := newListTree(t)
	for index := range 50 {
		tree.write("d"+string(rune('a'+index%26))+string(rune('a'+index/26))+"/f", "")
	}
	options := defaultListOptions()
	for _, test := range []struct {
		name       string
		abortAfter int
		wantAbort  bool
	}{
		{"before the first entry", 0, true},
		{"after the root and ten entries", 11, true},
		{"never", 1 << 30, false},
	} {
		checks := 0
		result, failure := listTree(tree.root, &options, func() bool { checks++; return checks > test.abortAfter })
		switch {
		case test.wantAbort && (failure == nil || failure.Code != "aborted" || result != nil):
			t.Errorf("%s: result %v, failure %v, want an aborted failure and no result", test.name, result != nil, failure)
		case !test.wantAbort && (failure != nil || result.count != 100):
			t.Errorf("%s: failure %v, want 100 entries", test.name, failure)
		}
		// One check for the root and one for each entry the walk yields, not a check per directory read.
		if test.wantAbort && checks != test.abortAfter+1 {
			t.Errorf("%s: %d checks, want %d", test.name, checks, test.abortAfter+1)
		}
	}
}

func TestParseListOptionsDefaultsAndInvalidFields(t *testing.T) {
	options, failure := parseListOptions(Object{})
	if failure != nil || !options.hidden || !options.gitignore || options.maxEntries != 50_000 || options.maxBytes != 4<<20 || options.timeout != 10*time.Second {
		t.Fatalf("defaults = %+v, %v", options, failure)
	}
	options, _ = parseListOptions(Object{"maxBytes": float64(1 << 40), "hidden": "no", "gitignore": nil})
	if options.maxBytes != MaxPayload || !options.hidden || !options.gitignore {
		t.Fatalf("maxBytes = %d, hidden = %v, gitignore = %v; want the frame limit and the defaults", options.maxBytes, options.hidden, options.gitignore)
	}
	for _, request := range []Object{
		{"maxEntries": float64(-1)}, {"maxEntries": 1.5}, {"maxBytes": "1"}, {"timeoutMs": true}, {"exclude": "node_modules"}, {"exclude": []any{"a", 1.0}},
	} {
		if _, failure := parseListOptions(request); failure == nil || failure.Code != "EINVAL" || !strings.HasPrefix(failure.Message, "invalid field ") {
			t.Errorf("%v: failure = %v", request, failure)
		}
	}
	if _, _, failure := opListFiles(Object{}, newControl()); failure == nil || failure.Message != "missing field path" {
		t.Errorf("missing path: %v", failure)
	}
}

// String::from_utf8_lossy: a truncated or ill-formed sequence becomes one U+FFFD per maximal prefix.
func TestLossyUTF8ReplacesMaximalPrefixes(t *testing.T) {
	for input, want := range map[string]string{
		"plain":             "plain",
		"é":                 "é",
		"\xF0\x90\x80":      "\uFFFD",
		"a\xE2\x82b":        "a\uFFFDb",
		"\xE2\x28\xA1":      "\uFFFD(\uFFFD",
		"\xC0\xAF":          "\uFFFD\uFFFD",
		"\xF4\x90\x80\x80":  "\uFFFD\uFFFD\uFFFD\uFFFD",
		"\xED\xA0\x80":      "\uFFFD\uFFFD\uFFFD",
		"\xFF":              "\uFFFD",
		"ok\xE0\x80\x80end": "ok\uFFFD\uFFFD\uFFFDend",
	} {
		if got := lossyUTF8(input); got != want {
			t.Errorf("lossyUTF8(%q) = %q, want %q", input, got, want)
		}
	}
}

// docs/protocol.md: `hello` lists `listFiles` in `features`, and a cancel right behind a `listFiles` request stops the walk
// between entries with `aborted`; remote.test.ts "cancels a listing in the daemon" proves the same through the client.
func TestServeListFilesAnswersWithItsPayloadAndStopsOnCancel(t *testing.T) {
	tree := newListTree(t)
	for directory := range 100 {
		for file := range 100 {
			tree.write(filepath.Join("d"+strconv.Itoa(directory), "f"+strconv.Itoa(file)), "")
		}
	}
	p := startServe(t, Options{})
	p.request(1, Object{"op": "hello", "protocol": float64(1)}, nil)
	if features, _ := object(t, p.reply(1))["features"].([]any); !slices.Equal(features, []any{"listFiles"}) {
		t.Fatalf("hello features = %v, want [listFiles]", features)
	}
	p.request(2, Object{"op": "listFiles", "path": tree.root, "maxEntries": float64(3)}, nil)
	reply := p.reply(2)
	if reply.Kind != FrameResult || object(t, reply)["count"] != float64(3) || object(t, reply)["truncated"] != true || string(reply.Payload) != "dd0\x00fd0/f0\x00fd0/f1\x00" {
		t.Fatalf("listFiles reply = %+v payload %q", reply, reply.Payload)
	}
	// A cancel right behind its request is never lost: the daemon registers the request first.
	for id := uint32(10); id < 20; id++ {
		p.request(id, Object{"op": "listFiles", "path": tree.root}, nil)
		p.send(FrameCancel, id, Object{}, nil)
		if reply := p.reply(id); reply.Kind != FrameError || object(t, reply)["code"] != "aborted" {
			t.Fatalf("request %d: %+v", id, reply)
		}
	}
	p.request(3, Object{"op": "listFiles", "path": tree.root}, nil)
	if reply := p.reply(3); reply.Kind != FrameResult || object(t, reply)["count"] != float64(10_100) || object(t, reply)["truncated"] != false {
		t.Fatalf("a listing after the cancels = %+v", reply)
	}
}
