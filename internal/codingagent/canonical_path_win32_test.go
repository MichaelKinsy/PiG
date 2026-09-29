package codingagent

import (
	"bytes"
	"encoding/json"
	"errors"
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
	cases := []struct {
		name, path, want string
		viaNode          bool
	}{
		// Sol P1: EvalSymlinks would follow \inner on the process drive and end at a path with a volume.
		{"rooted link then absolute link on the link's drive", `Y:\outer`, `Y:\right.txt`, true},
		{"rooted link then rooted link", `Y:\chain`, `Y:\final.txt`, true},
		{"rooted link", `C:\d\rooted\marker`, `C:\target\marker`, true},
		{"rooted link with forward slash", `C:\d\fwd\marker`, `C:\target\marker`, true},
		{"rooted link keeps the input case of the device", `c:\D\ROOTED\marker`, `c:\target\marker`, true},
		{"mixed separators", `C:/d/rooted/marker`, `C:\target\marker`, true},
		{"trailing separator", `C:\d\rooted\`, `C:\target`, true},
		{"absolute link to a rooted link", `C:\d\chain\marker`, `C:\target\marker`, true},
		{"relative link", `C:\d\rel\marker`, `C:\target\marker`, false},
		{"no link", `C:\target\marker`, `C:\target\marker`, false},
		{"dangling rooted link fails", `C:\d\dang\x`, ``, true},
		{"missing component fails", `C:\d\missing\x`, ``, false},
		{"drive-relative target uses the drive's current directory", `C:\d\dr\f`, `D:\dcwd\foo\f`, true},
		{"volume mount stays in the path", `C:\m\x`, `C:\m\x`, true},
		{"rooted link behind a volume mount", `C:\m\rooted\marker`, `C:\target\marker`, true},
		{"UNC root", `\\srv\share\l\f`, `\\srv\share\dir\f`, true},
		{"extended-length prefix resolves a rooted target under the device", `\\?\C:\l\f`, `\\?\dir\f`, true},
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
			got, viaNode, err := nodeRealpathWin32(tc.path, fsys, proc)
			if (err == nil) != want.OK || err == nil && got != want.Path {
				t.Fatalf("nodeRealpathWin32(%q)=%q, %v; Node ok=%v path=%q", tc.path, got, err, want.OK, want.Path)
			}
			if viaNode != tc.viaNode {
				t.Errorf("viaNode=%v; want %v", viaNode, tc.viaNode)
			}
		})
	}
}

func TestWin32TargetNeedsNodeResolution(t *testing.T) {
	for target, want := range map[string]bool{
		``:                 false,
		`\dir`:             true,
		`/dir`:             true,
		`\`:                true,
		`D:foo`:            true,
		`D:`:               true,
		`D:\foo`:           false,
		`D:/foo`:           false,
		`\\server\share\x`: false,
		`\\?\Volume{1}\x`:  false,
		`foo\bar`:          false,
		`..\foo`:           false,
		`C`:                false,
	} {
		if got := win32TargetNeedsNodeResolution(target); got != want {
			t.Errorf("win32TargetNeedsNodeResolution(%q)=%v; want %v", target, got, want)
		}
	}
}

// Sol P1 at the decision point: EvalSymlinks follows the rooted \inner on the process drive X: and its last absolute link gives it a volume. The result must come from the Node walk, which resolves \inner on Y:.
func TestEvalCanonicalPathWithPrefersWalkAfterRootedLink(t *testing.T) {
	entries := win32WalkFixture()
	fsys := win32FakeFS(entries)
	proc := win32FakeProcess(`X:\`, nil)
	walk := func(path string) (string, bool, error) { return nodeRealpathWin32(path, fsys, proc) }
	evalCalls := 0
	eval := func(string) (string, error) {
		evalCalls++
		return `X:\wrong.txt`, nil
	}
	got, err := evalCanonicalPathWith(`Y:\outer`, walk, eval)
	if err != nil || got != `Y:\right.txt` {
		t.Fatalf("evalCanonicalPathWith=%q, %v; want Y:\\right.txt", got, err)
	}
	if evalCalls != 0 {
		t.Errorf("filepath.EvalSymlinks was consulted %d times after the walk followed a rooted link", evalCalls)
	}
	// A dangling rooted link is an error in Node, so canonicalizePath keeps the raw path; the standard library must not rescue it.
	got, err = evalCanonicalPathWith(`C:\d\dang\x`, walk, eval)
	if err == nil {
		t.Fatalf("dangling rooted link resolved to %q", got)
	}
	// An ordinary path keeps the standard library's resolution and its error.
	sentinel := errors.New("eval failed")
	got, err = evalCanonicalPathWith(`C:\d\rel\marker`, walk, func(string) (string, error) { return `C:\Case\Normalized`, sentinel })
	if got != `C:\Case\Normalized` || !errors.Is(err, sentinel) {
		t.Fatalf("ordinary path=%q, %v; want the standard library's result", got, err)
	}
}
