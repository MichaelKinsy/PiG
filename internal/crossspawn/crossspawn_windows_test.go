//go:build windows

package crossspawn

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

const argvHelper = "PIG_CROSSSPAWN_ARGV_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(argvHelper) == "1" {
		if err := json.NewEncoder(os.Stdout).Encode(os.Args[1:]); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if os.Getenv(commandLineHelper) == "1" {
		if err := json.NewEncoder(os.Stdout).Encode(windows.UTF16PtrToString(syscall.GetCommandLine())); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if os.Getenv(directoryHelper) == "1" {
		buffer := make([]uint16, windows.MAX_LONG_PATH)
		n, err := windows.GetCurrentDirectory(uint32(len(buffer)), &buffer[0])
		if err != nil || json.NewEncoder(os.Stdout).Encode(windows.UTF16ToString(buffer[:n])) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestWindowsShellCommandNonCMD(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ComSpec", exe)
	line := `editor "file & name" %TOKEN%`
	cmd := ShellCommand(t.Context(), line)
	cmd.Env = append(os.Environ(), argvHelper+"=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"-c", line}) {
		t.Fatalf("shell args = %q", got)
	}
}

// Drive CreateProcess/cmd.exe as well as the portable planner. The command,
// cwd, script filename and arguments contain characters with shell meaning.
// cross-spawn's escapeCommand carets every metacharacter, but cmd.exe still
// ends the command token at a caret-escaped delimiter (space, comma,
// semicolon), so Pi cannot start a shim whose name contains one. The shim
// name carries the other metacharacters; a delimiter name fails as in Pi.
func TestWindowsCommandLiteralArguments(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	const shimName, delimitedShimName = "shim&(data)!%TOKEN%^x.cmd", "shim & data.cmd"
	root := filepath.Join(t.TempDir(), "cwd & space")
	for _, dir := range []string{"bin", filepath.Join("node_modules", ".bin"), filepath.Join("node_modules", "Xbin")} {
		directory := filepath.Join(root, dir)
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "receiver.exe"), data, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, shim := range []string{shimName, delimitedShimName} {
			writeExecutable(t, filepath.Join(directory, shim), "@\"%~dp0receiver.exe\" %*\r\n")
		}
	}
	interpreter := filepath.Join(root, "bin", "receiver.exe")
	t.Setenv("PATH", filepath.Dir(interpreter)+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := filepath.Join(root, "source & data.cmd")
	writeExecutable(t, script, "#!/usr/bin/env receiver.exe\r\n@echo INJECTED>sentinel\r\n")
	args := []string{"", "plain", "space in argument", "https://example.invalid/?a=1&echo.INJECTED>sentinel", "%TOKEN%", "!TOKEN!", "(a)|b^c", `quote"value`, `trailing\`}
	for _, dir := range []string{"bin", filepath.Join("node_modules", ".bin"), filepath.Join("node_modules", "Xbin")} {
		name := filepath.Join(dir, delimitedShimName)
		t.Run(name, func(t *testing.T) {
			cmd := Command(t.Context(), root, name, args...)
			cmd.Env = append(os.Environ(), argvHelper+"=1", "TOKEN=EXPANDED")
			if output, err := cmd.Output(); err == nil || len(output) != 0 {
				t.Fatalf("cmd.exe started a command token with a delimiter: err %v; output %s", err, output)
			}
			if _, err := os.Stat(filepath.Join(root, "sentinel")); !os.IsNotExist(err) {
				t.Fatalf("shell interpreted data: sentinel stat = %v", err)
			}
		})
	}
	for _, name := range []string{interpreter, filepath.Join("bin", shimName), filepath.Join("node_modules", ".bin", shimName), filepath.Join("node_modules", "Xbin", shimName), script} {
		t.Run(name, func(t *testing.T) {
			cmd := Command(t.Context(), root, name, args...)
			cmd.Env = append(os.Environ(), argvHelper+"=1", "TOKEN=EXPANDED")
			output, err := cmd.Output()
			if err != nil {
				t.Fatalf("run: %v; output %s", err, output)
			}
			var got []string
			if err := json.Unmarshal(output, &got); err != nil {
				t.Fatalf("decode argv: %v; output %s", err, output)
			}
			want := args
			if name == script {
				want = append([]string{script}, args...)
			}
			if !slices.Equal(got, want) {
				t.Fatalf("argv = %q, want %q", got, want)
			}
			if _, err := os.Stat(filepath.Join(root, "sentinel")); !os.IsNotExist(err) {
				t.Fatalf("shell interpreted data: sentinel stat = %v", err)
			}
		})
	}
}

// Compare resolution with Pi's exact dependency on Windows: cross-spawn 7.0.6
// resolveCommand through node-which 2.0.2 and isexe 2.0.0. A PATHEXT match keeps
// the PATHEXT spelling, an unset PATHEXT has its own default order, and a
// PATHEXT with an empty entry accepts every file.
func TestWindowsResolveCommandMatchesPinnedCrossSpawn(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "cwd")
	pathDir := filepath.Join(root, "path dir")
	quotedDir := filepath.Join(root, "quoted dir")
	splitDir := filepath.Join(root, "split;dir")
	for _, file := range []string{
		filepath.Join(cwd, "tool"), filepath.Join(cwd, "tool.cmd"), filepath.Join(cwd, "tool.js"),
		filepath.Join(cwd, "both.com"), filepath.Join(cwd, "both.exe"),
		filepath.Join(cwd, "plain"), filepath.Join(cwd, "dirtool.bat"),
		filepath.Join(cwd, "sub", "stool.cmd"), filepath.Join(cwd, "a.b", "run"),
		filepath.Join(pathDir, "ptool.bat"), filepath.Join(pathDir, "tool.exe"),
		filepath.Join(quotedDir, "qtool.exe"), filepath.Join(splitDir, "stool.exe"),
	} {
		writeExecutable(t, file, "stand-in")
	}
	if err := os.MkdirAll(filepath.Join(cwd, "dirtool.cmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	rooted := strings.TrimPrefix(filepath.Join(cwd, "sub", "stool"), filepath.VolumeName(cwd))
	// The empty PATH entry hands a name with a drive and no root to node-which,
	// which joins it to nothing and stats it in the child cwd.
	t.Setenv("PATH", pathDir+`;"`+quotedDir+`";"`+splitDir+`";;`+os.Getenv("PATH"))
	drive := filepath.VolumeName(cwd)
	queries := [][2]string{
		{cwd, "tool"}, {cwd, "tool.js"}, {cwd, "both"}, {cwd, "plain"}, {cwd, filepath.Join(cwd, "plain")},
		{cwd, "dirtool"}, {cwd, filepath.Join("sub", "stool")}, {cwd, "sub/stool"}, {cwd, rooted},
		{cwd, filepath.Join("a.b", "run")}, {cwd, "ptool"}, {cwd, "qtool"}, {cwd, "stool"}, {cwd, "missing"},
		{root, "tool"}, {root, filepath.Join("cwd", "tool")},
		{cwd, drive + "tool"}, {cwd, drive + "tool.js"}, {cwd, drive + filepath.Join("sub", "stool")},
		{cwd, drive + filepath.Join("a.b", "run")}, {cwd, strings.ToLower(drive) + "both"}, {root, drive + "missing"},
	}
	data, err := json.Marshal(queries)
	if err != nil {
		t.Fatal(err)
	}
	probe := `const path = require('node:path');
const resolve = require(path.resolve(process.argv[1], 'lib/util/resolveCommand.js'));
const queries = JSON.parse(require('node:fs').readFileSync(0, 'utf8'));
process.stdout.write(JSON.stringify(queries.map(([cwd, command]) => resolve({command, args: [], options: {cwd}}) || '')));`
	for _, pathExt := range []string{os.Getenv("PATHEXT"), ".Exe;.cMd;.bAt", "", ".COM;.EXE;"} {
		t.Run(pathExt, func(t *testing.T) {
			t.Setenv("PATHEXT", pathExt)
			cmd := exec.CommandContext(t.Context(), "node", "-e", probe, filepath.Join("..", "..", "coding", "extension", "host", "subprocess", "runtime-node", "shims", "cross-spawn"))
			cmd.Stdin = strings.NewReader(string(data))
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("resolveCommand oracle: %v: %s", err, out)
			}
			var want []string
			if err := json.Unmarshal(out, &want); err != nil {
				t.Fatal(err)
			}
			for i, query := range queries {
				if got := resolveCommand(query[0], query[1]); got != want[i] {
					t.Errorf("resolveCommand(%q, %q) = %q, cross-spawn %q", query[0], query[1], got, want[i])
				}
			}
		})
	}
}
