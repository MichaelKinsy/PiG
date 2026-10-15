package codingagent

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// These tests run on every host. The Windows canonical-path walk in canonical_path_win32.go is checked against Node's own fs.realpathSync walk and path.win32.resolve (testdata/realpath-win32-oracle.mjs) over a fake Windows filesystem, and against results derived by hand from the same functions.

type win32FakeEntry struct {
	T        string `json:"t"`
	Target   string `json:"target,omitempty"`
	Dangling bool   `json:"dangling,omitempty"`
}

// A call is {"resolve": [...]} or {"realpath": "..."}; the map keeps an empty argument list distinct from a realpath call.
type win32OracleCall map[string]any

type win32OracleResult struct {
	OK   bool   `json:"ok"`
	Path string `json:"path"`
}

func win32FakeKey(path string) string {
	key := strings.ToLower(strings.ReplaceAll(path, "/", `\`))
	if len(key) > 1 && strings.HasSuffix(key, `\`) && (len(key) != 3 || key[1] != ':') {
		key = strings.TrimRight(key, `\`)
	}
	return key
}

func win32OracleRun(t *testing.T, cwd string, env map[string]string, entries map[string]win32FakeEntry, calls []win32OracleCall) []win32OracleResult {
	t.Helper()
	input, err := json.Marshal(map[string]any{"cwd": cwd, "env": env, "entries": entries, "calls": calls})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/realpath-win32-oracle.mjs")
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node oracle: %v\n%s", err, stderr.String())
	}
	var results []win32OracleResult
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatalf("oracle output %q: %v", out, err)
	}
	if len(results) != len(calls) {
		t.Fatalf("oracle returned %d results for %d calls", len(results), len(calls))
	}
	return results
}

func win32FakeFS(entries map[string]win32FakeEntry) nodeRealpathFS {
	byKey := make(map[string]win32FakeEntry, len(entries))
	for path, entry := range entries {
		byKey[win32FakeKey(path)] = entry
	}
	return nodeRealpathFS{
		lstat: func(path string) (nodeEntryKind, string, error) {
			entry, ok := byKey[win32FakeKey(path)]
			switch {
			case !ok:
				return nodeEntryPlain, "", os.ErrNotExist
			case entry.T == "link":
				return nodeEntryLink, entry.Target, nil
			case entry.T == "mount":
				return nodeEntryMount, "", nil
			}
			return nodeEntryPlain, "", nil
		},
		stat: func(path string) error {
			if entry, ok := byKey[win32FakeKey(path)]; !ok || entry.Dangling {
				return os.ErrNotExist
			}
			return nil
		},
	}
}

func win32FakeProcess(cwd string, env map[string]string) win32Process {
	return win32Process{cwd: func() string { return cwd }, getenv: func(name string) string { return env[name] }}
}

// path.win32.resolve is the primitive Node applies to every link target: resolve(previous, target).
func TestWin32ResolveMatchesNode(t *testing.T) {
	const cwd = `C:\work\cwd`
	env := map[string]string{"=D:": `D:\dcwd`, "=e:": `E:\ecwd`}
	cases := [][]string{
		{`C:\a\`, `\dir`},
		{`C:\a\b\`, `/dir/x`},
		{`\\server\share\a\`, `\dir`},
		{`\\?\C:\a\`, `\dir`},
		{`\\?\Volume{1234}\a\`, `\dir`},
		{`\\.\PHYSICALDRIVE0\x`},
		{`C:\a\`, `D:foo`},
		{`C:\a\`, `D:`},
		{`C:\a\`, `E:foo\..\bar`},
		{`C:\a\`, `C:foo`},
		{`C:\a`, `..\..\..\b`},
		{`C:\`, `..\x`},
		{`a\b`, `..\c`},
		{`C:`},
		{`d:x`, `e:y`},
		{``, `.`},
		{},
		{`C:\a\b`, `.`},
		{`\\srv`},
		{`\`},
		{`/`},
		{`//srv/share/x/../y`},
		{`C:\a\.\b\..\..\..`},
		{`C:/a//b\\c\`},
		{`\rooted`},
		{`\\?\C:\x\..\..`},
		{`..\..\..\x`},
		{`a\..\..\..\..\b\..\..\..\c`},
		{`..`, `..`, `..`, `.\x`},
		{`x\..\..\y`},
	}
	// A relative process directory leaves the result relative, which keeps every leading ".." segment.
	for _, cwd := range []string{cwd, `w\r`} {
		calls := make([]win32OracleCall, len(cases))
		for i, args := range cases {
			calls[i] = win32OracleCall{"resolve": append([]string{}, args...)}
		}
		oracle := win32OracleRun(t, cwd, env, map[string]win32FakeEntry{}, calls)
		proc := win32FakeProcess(cwd, env)
		for i, args := range cases {
			if got := win32Resolve(proc, args...); got != oracle[i].Path {
				t.Errorf("cwd %q: win32Resolve(%q)=%q; Node path.win32.resolve=%q", cwd, args, got, oracle[i].Path)
			}
		}
	}
}

func win32WalkFixture() map[string]win32FakeEntry {
	dir := win32FakeEntry{T: "dir"}
	file := win32FakeEntry{T: "file"}
	link := func(target string) win32FakeEntry { return win32FakeEntry{T: "link", Target: target} }
	return map[string]win32FakeEntry{
		`C:\`: dir, `D:\`: dir, `X:\`: dir, `Y:\`: dir,

		// A rooted target names the device of the path that reached the link, then a link on each drive.
		`Y:\outer`:          link(`\inner`),
		`X:\inner`:          link(`X:\wrong.txt`),
		`X:\wrong.txt`:      file,
		`Y:\inner`:          link(`Y:\right.txt`),
		`Y:\right.txt`:      file,
		`Y:\chain`:          link(`\inner2`),
		`X:\inner2`:         link(`\wrong2`),
		`Y:\inner2`:         link(`\right2`),
		`Y:\right2`:         link(`Y:\final.txt`),
		`Y:\final.txt`:      file,
		`X:\wrong2`:         file,
		`C:\d`:              dir,
		`C:\d\rooted`:       link(`\target`),
		`C:\d\fwd`:          link(`/target`),
		`C:\d\chain`:        link(`C:\d\rooted`),
		`C:\d\rel`:          link(`..\target`),
		`C:\d\dang`:         {T: "link", Target: `\nowhere`, Dangling: true},
		`C:\d\dr`:           link(`D:foo`),
		`C:\target`:         dir,
		`C:\target\marker`:  file,
		`D:\dcwd`:           dir,
		`D:\dcwd\foo`:       dir,
		`D:\dcwd\foo\f`:     file,
		`C:\m`:              {T: "mount"},
		`C:\m\x`:            file,
		`C:\m\rooted`:       link(`\target`),
		`\\srv\share\`:      dir,
		`\\srv\share\l`:     link(`\dir`),
		`\\srv\share\dir`:   dir,
		`\\srv\share\dir\f`: file,
		`\\?\C:\`:           dir,
		`\\?\C:\l`:          link(`\dir`),
		`\\?\dir`:           dir,
		`\\?\dir\f`:         file,
	}
}

func TestNodeRealpathWin32MatchesNode(t *testing.T) {
	const cwd = `C:\work\cwd`
	env := map[string]string{"=D:": `D:\dcwd`}
	entries := win32WalkFixture()
	cases := []struct{ name, path, want string }{
		// Sol P1: EvalSymlinks would follow \inner on the process drive and end at a path with a volume.
		{"rooted link then absolute link on the link's drive", `Y:\outer`, `Y:\right.txt`},
		{"rooted link then rooted link", `Y:\chain`, `Y:\final.txt`},
		{"rooted link", `C:\d\rooted\marker`, `C:\target\marker`},
		{"rooted link with forward slash", `C:\d\fwd\marker`, `C:\target\marker`},
		{"rooted link keeps the input case of the device", `c:\D\ROOTED\marker`, `c:\target\marker`},
		{"mixed separators", `C:/d/rooted/marker`, `C:\target\marker`},
		{"trailing separator", `C:\d\rooted\`, `C:\target`},
		{"absolute link to a rooted link", `C:\d\chain\marker`, `C:\target\marker`},
		{"relative link", `C:\d\rel\marker`, `C:\target\marker`},
		{"no link", `C:\target\marker`, `C:\target\marker`},
		{"dangling rooted link fails", `C:\d\dang\x`, ``},
		{"missing component fails", `C:\d\missing\x`, ``},
		{"drive-relative target uses the drive's current directory", `C:\d\dr\f`, `D:\dcwd\foo\f`},
		{"volume mount stays in the path", `C:\m\x`, `C:\m\x`},
		{"rooted link behind a volume mount", `C:\m\rooted\marker`, `C:\target\marker`},
		{"UNC root", `\\srv\share\l\f`, `\\srv\share\dir\f`},
		{"extended-length prefix resolves a rooted target under the device", `\\?\C:\l\f`, `\\?\dir\f`},
	}
	calls := make([]win32OracleCall, len(cases))
	for i, tc := range cases {
		calls[i] = win32OracleCall{"realpath": tc.path}
	}
	oracle := win32OracleRun(t, cwd, env, entries, calls)
	fsys := win32FakeFS(entries)
	proc := win32FakeProcess(cwd, env)
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := oracle[i]
			if want.OK != (tc.want != "") || want.OK && want.Path != tc.want {
				t.Fatalf("fixture expectation %q disagrees with Node realpathSync (ok=%v path=%q)", tc.want, want.OK, want.Path)
			}
			got, err := nodeRealpathWin32(tc.path, fsys, proc)
			if (err == nil) != want.OK || err == nil && got != want.Path {
				t.Fatalf("nodeRealpathWin32(%q)=%q, %v; Node ok=%v path=%q", tc.path, got, err, want.OK, want.Path)
			}
		})
	}
}

// A non-link component keeps the case and the 8.3 short name the caller spelled: Node's JS realpathSync does not ask the filesystem for the stored spelling as realpathSync.native does, so the context-file ancestor walk sees `R` and `r` as two directories (resource-loader.ts findShadowedContextFile compares canonicalizePath results as strings).
func TestNodeRealpathWin32KeepsComponentSpelling(t *testing.T) {
	const cwd = `C:\work\cwd`
	entries := win32WalkFixture()
	entries[`C:\target\Long Name`] = win32FakeEntry{T: "dir"}
	entries[`C:\target\Long Name\marker`] = win32FakeEntry{T: "file"}
	paths := []string{`C:\TARGET\Marker`, `c:\Target\MARKER`, `C:\target\LONG~1\marker`}
	// The fake filesystem has no short names, so the third path is missing in Node as well; the first two exist under another case.
	calls := make([]win32OracleCall, len(paths))
	for i, p := range paths {
		calls[i] = win32OracleCall{"realpath": p}
	}
	oracle := win32OracleRun(t, cwd, nil, entries, calls)
	fsys := win32FakeFS(entries)
	proc := win32FakeProcess(cwd, nil)
	for i, p := range paths {
		got, err := nodeRealpathWin32(p, fsys, proc)
		if (err == nil) != oracle[i].OK || err == nil && got != oracle[i].Path {
			t.Errorf("nodeRealpathWin32(%q)=%q, %v; Node ok=%v path=%q", p, got, err, oracle[i].OK, oracle[i].Path)
		}
	}
	if !oracle[0].OK || oracle[0].Path != paths[0] || !oracle[1].OK || oracle[1].Path != paths[1] {
		t.Errorf("Node realpathSync did not keep the spelling: %+v", oracle[:2])
	}
}
