package crossspawn

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Pi 0.87.1 utils/child-process.ts uses cross-spawn 7.0.6. Its detectShebang runs
// before needsShell: even a .cmd file with a Node shebang goes to Node, not cmd.
// Each script has its own directory: on Windows an extensionless name with a
// .cmd sibling resolves to the sibling through PATHEXT, as in cross-spawn.
func TestWindowsPlanShebangDoesNotAddShell(t *testing.T) {
	root := t.TempDir()
	interpreter := "audit-interpreter.exe"
	writeExecutable(t, filepath.Join(root, interpreter), "native executable stand-in")
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, ext := range []string{".cmd", ".js", ""} {
		script := filepath.Join(t.TempDir(), "metadata & script"+ext)
		writeExecutable(t, script, "#!/usr/bin/env "+interpreter+"\n@echo must-not-run-as-batch\n")
		args := []string{"https://example.invalid/?a=1&echo.INJECTED>sentinel", "", `quote"and\tail\`}
		plan := planWindowsCommand("", script, args)
		if plan.cmdLine != "" || plan.name != interpreter || !slices.Equal(plan.args, append([]string{script}, args...)) {
			t.Errorf("%s: got %#v; want direct interpreter with script and literal argv", ext, plan)
		}
	}
}

func TestWindowsPlanUnresolvedCommandUsesEscapedShell(t *testing.T) {
	// parseNonShell deliberately also handles cmd builtins that have no file.
	t.Setenv("COMSPEC", "cmd.exe")
	plan := planWindowsCommand("", "audit-missing-command", []string{"one&two"})
	if plan.cmdLine != `cmd.exe /d /s /c "audit-missing-command ^"one^&two^""` {
		t.Fatalf("unresolved command plan = %#v", plan)
	}
}

func TestWindowsPlanCmdShimMatcherMatchesCrossSpawn(t *testing.T) {
	t.Setenv("COMSPEC", "cmd.exe")
	// cross-spawn's isCmdShimRegExp uses .bin, not \\.bin. Mirror the exact
	// matcher, including Xbin, so a shim never loses a required escape pass.
	for _, dir := range []string{".bin", "Xbin"} {
		script := filepath.Join(t.TempDir(), "node_modules", dir, "tool.cmd")
		writeExecutable(t, script, "@echo off\r\n")
		plan := planWindowsCommand("", script, []string{"one&two"})
		if !strings.HasSuffix(plan.cmdLine, ` ^^^"one^^^&two^^^""`) {
			t.Errorf("%s: lost second escape pass: %q", dir, plan.cmdLine)
		}
	}
}

// isCmdShimRegExp is /node_modules[\\/].bin[\\/][^\\/]+\.cmd$/i: a long s (U+017F) is not the s of node_modules in JavaScript, although Go's (?i) folds it.
func TestWindowsPlanCmdShimMatcherFoldsASCIIOnly(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{`C:\p\node_modules\.bin\tool.cmd`, true},
		{`C:\p\NODE_MODULES\.BIN\TOOL.CMD`, true},
		{`C:\p\node_moduleſ\.bin\tool.cmd`, false},
		{`C:\p\node_modules\.bin\tooſ.cmd`, true},
	} {
		if got := cmdShim.MatchString(tc.path); got != tc.want {
			t.Errorf("cmdShim(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestWindowsPlanResolvesInChildDirectory(t *testing.T) {
	root := t.TempDir()
	// In the parent this name is absent. In the child it needs two escaping
	// passes; choosing the escape depth before applying cwd loses one pass.
	name := filepath.Join("node_modules", ".bin", "audit-relative.cmd")
	writeExecutable(t, filepath.Join(root, name), "@echo off\r\n")
	plan := planWindowsCommand(root, name, []string{"one&two"})
	if !strings.HasSuffix(plan.cmdLine, ` ^^^"one^^^&two^^^""`) {
		t.Fatalf("child cwd lost second escape pass: %#v", plan)
	}
}

func TestWindowsPlanNativeExecutableHasNoShell(t *testing.T) {
	name := filepath.Join(t.TempDir(), "native & tool.exe")
	writeExecutable(t, name, "native executable stand-in")
	args := []string{"& | > < %PATH% !TOKEN!", ""}
	plan := planWindowsCommand("", name, args)
	if plan.cmdLine != "" || plan.name != name || !slices.Equal(plan.args, args) {
		t.Fatalf("native argv changed: %#v", plan)
	}
}

func writeExecutable(t testing.TB, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestReadShebangMatchesPinnedCrossSpawn(t *testing.T) {
	bodies := []string{"", "@echo off\r\n", "#!/usr/bin/env node\n", "#!/usr/bin/node --flag extra\n", "#! /usr/bin/env node\r\n", "#!/usr/bin/env  node\n", "#!/usr/bin/env\tnode\n", "#!/bin/node\u2028ignored", "#!/bin/node", "\ufeff#!/bin/node\n", "#!" + strings.Repeat("x", 200)}
	var paths []string
	var got []string
	for _, body := range bodies {
		path := filepath.Join(t.TempDir(), "script.cmd")
		writeExecutable(t, path, body)
		paths = append(paths, path)
		got = append(got, readShebang(path))
	}
	data, err := json.Marshal(paths)
	if err != nil {
		t.Fatal(err)
	}
	probe := `const fs = require('node:fs');
const read = require(require('node:path').resolve(process.argv[1], 'lib/util/readShebang.js'));
process.stdout.write(JSON.stringify(JSON.parse(fs.readFileSync(0, 'utf8')).map(read)));`
	cmd := exec.CommandContext(t.Context(), "node", "-e", probe, filepath.Join("..", "..", "coding", "extension", "host", "subprocess", "runtime-node", "shims", "cross-spawn"))
	cmd.Stdin = strings.NewReader(string(data))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("shebang oracle: %v: %s", err, out)
	}
	var want []string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("shebangs = %q, cross-spawn %q", got, want)
	}
}

func BenchmarkWindowsPlanCommand(b *testing.B) {
	root := b.TempDir()
	for _, name := range []string{"native.exe", "shim.cmd"} {
		path := filepath.Join(root, name)
		writeExecutable(b, path, "native or batch stand-in\n")
		b.Run(name, func(b *testing.B) {
			args := []string{"view", "package@1.2.3", "https://registry.invalid/?a=1&b=2"}
			b.ReportAllocs()
			for b.Loop() {
				planWindowsCommand(root, path, args)
			}
		})
	}
}

// Compare bytes with Pi's exact dependency, not an independently rewritten
// escape algorithm. Include every metacharacter, quote/backslash runs, empty
// arguments, line endings, Unicode, and both cmd parsing depths.
func TestWindowsEscapingMatchesPinnedCrossSpawn(t *testing.T) {
	inputs := []string{"", "ordinary", `C:\directory with space\`, "https://server.invalid/?x=1&echo.INJECTED>file", `\"`, `\\"`, `\\\"`}
	atoms := []string{"a", " ", "\t", "\n", "\r", "\u2028", "\u2029", "🙂", `\`, `"`, "(", ")", "[", "]", "%", "!", "^", "`", "<", ">", "&", "|", ";", ",", "*", "?"}
	for _, a := range atoms {
		for _, b := range atoms {
			for _, c := range atoms {
				inputs = append(inputs, a+b+c)
			}
		}
	}
	inputs = append(inputs, strings.Repeat(`\`, 4096)+`"`)
	data, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	probe := `const fs = require('node:fs');
const spawn = require('node:path').resolve(process.argv[1], 'package.json');
if (require(spawn).version !== '7.0.6') throw Error('review the pinned cross-spawn version');
const escape = require(require('node:path').join(require('node:path').dirname(spawn), 'lib/util/escape.js'));
const inputs = JSON.parse(fs.readFileSync(0, 'utf8'));
process.stdout.write(JSON.stringify(inputs.map(s => [escape.command(s), escape.argument(s, false), escape.argument(s, true)])));`
	cmd := exec.CommandContext(t.Context(), "node", "-e", probe, filepath.Join("..", "..", "coding", "extension", "host", "subprocess", "runtime-node", "shims", "cross-spawn"))
	cmd.Stdin = strings.NewReader(string(data))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cross-spawn oracle: %v: %s", err, out)
	}
	var want [][3]string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(inputs) {
		t.Fatal("oracle did not return every input")
	}
	for i, input := range inputs {
		got := [3]string{escapeCommand(input), escapeArgument(input, false), escapeArgument(input, true)}
		if got != want[i] {
			t.Fatalf("input (len=%d) %.100q: got %.100q, cross-spawn %.100q", len(input), input, got, want[i])
		}
	}
}

// node-which joins the command with each PATH entry and cross-spawn resolves the
// result with path.resolve(cwd, file). An empty PATH entry leaves a name with a
// drive and no root ("D:tool.exe") for path.win32.resolve, which keeps it on
// that drive's current directory. The oracle is Node's own path.win32.resolve
// (lib/path.js) with process.cwd() and the "=X:" per-drive variables replaced.
func TestWin32ResolveMatchesNodePathResolve(t *testing.T) {
	type query struct {
		Args      []string          `json:"args"`
		Cwd       string            `json:"cwd"`
		DriveCwds map[string]string `json:"driveCwds"`
	}
	work := `D:\work`
	queries := []query{
		{Args: []string{work, "D:tool.exe"}, Cwd: work},
		{Args: []string{work, "d:tool.exe"}, Cwd: work},
		{Args: []string{work, `D:sub\tool.exe`}, Cwd: work},
		{Args: []string{work, `D:..\up\tool.exe`}, Cwd: work},
		{Args: []string{work, `D:..\..\..\tool.exe`}, Cwd: work},
		{Args: []string{work, "D:"}, Cwd: work},
		{Args: []string{work, `D:\rooted\tool.exe`}, Cwd: work},
		{Args: []string{work, `\rooted\tool.exe`}, Cwd: work},
		{Args: []string{work + `\deep\dir`, "D:tool.exe"}, Cwd: work},
		{Args: []string{work, "E:tool.exe"}, Cwd: work},
		{Args: []string{work, "E:tool.exe"}, Cwd: work, DriveCwds: map[string]string{"E:": `E:\proj\x`}},
		{Args: []string{work, "e:sub\\..\\t.exe"}, Cwd: work, DriveCwds: map[string]string{"e:": `E:\proj\x`}},
		{Args: []string{work, "E:tool.exe"}, Cwd: work, DriveCwds: map[string]string{"E:": `F:\elsewhere`}},
		{Args: []string{work, "E:tool.exe"}, Cwd: `E:\process`},
		{Args: []string{work, "E:tool.exe"}, Cwd: `\\host\share\dir`},
		{Args: []string{`\\host\share\dir`, "D:tool.exe"}, Cwd: work},
		{Args: []string{`\\host\share\dir`, "D:tool.exe"}, Cwd: `D:\process`},
		{Args: []string{`\\host\share\dir`, `\\host\share\x\tool.exe`}, Cwd: work},
		{Args: []string{work, `C:\a\..\b/./tool.exe`}, Cwd: work},
		{Args: []string{work, "tool.exe"}, Cwd: work},
		{Args: []string{work, `..\tool.exe`}, Cwd: work},
	}
	data, err := json.Marshal(queries)
	if err != nil {
		t.Fatal(err)
	}
	probe := `const path = require('node:path').win32;
const queries = JSON.parse(require('node:fs').readFileSync(0, 'utf8'));
const out = queries.map(({args, cwd, driveCwds}) => {
  const env = {};
  for (const [device, dir] of Object.entries(driveCwds ?? {})) env['=' + device] = dir;
  process.env = new Proxy(env, {});
  process.cwd = () => cwd;
  return path.resolve(...args);
});
process.stdout.write(JSON.stringify(out));`
	cmd := exec.CommandContext(t.Context(), "node", "-e", probe)
	cmd.Stdin = strings.NewReader(string(data))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("path.win32.resolve oracle: %v: %s", err, out)
	}
	var want []string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatal(err)
	}
	for i, q := range queries {
		got := win32Resolve(func(device string) string { return q.DriveCwds[device] }, q.Cwd, q.Args...)
		if got != want[i] {
			t.Errorf("win32Resolve(cwd %q, drive cwds %v, %q) = %q, path.win32.resolve %q", q.Cwd, q.DriveCwds, q.Args, got, want[i])
		}
	}
}
