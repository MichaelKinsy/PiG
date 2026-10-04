package linkerexec

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	dataDir = "/data/data/com.termux"
	prefix  = dataDir + "/files/usr"
	linker  = "/system/bin/linker64"
)

// files is an app data directory by path: ELF, a script's text, or any other text.
type files map[string]string

const elf = "\x7fELF\x02\x01\x01"

func (f files) starter() Starter {
	return Starter{
		Linker:  linker,
		DataDir: dataDir,
		Prefix:  prefix,
		ReadHead: func(path string) ([]byte, error) {
			content, ok := f[path]
			if !ok {
				return nil, os.ErrNotExist
			}
			return []byte(content), nil
		},
		LookPath: func(name string) (string, error) {
			if path := prefix + "/bin/" + name; f[path] != "" {
				return path, nil
			}
			return "", exec.ErrNotFound
		},
	}
}

func TestResolveStartsAnELFThroughTheLinker(t *testing.T) {
	f := files{prefix + "/bin/git": elf}
	program, args, err := f.starter().Resolve(prefix+"/bin/git", []string{"git", "status"})
	if err != nil {
		t.Fatal(err)
	}
	if program != linker || !slices.Equal(args, []string{linker, prefix + "/bin/git", "status"}) {
		t.Fatalf("Resolve = %q %q", program, args)
	}
}

func TestResolveLeavesProgramsOutsideTheDataDirAlone(t *testing.T) {
	f := files{}
	for _, path := range []string{"/system/bin/sh", "/apex/com.android.runtime/bin/linker64", dataDir + "x/bin/tool", "/data/data/com.termux.api/files/usr/bin/x"} {
		program, args, err := f.starter().Resolve(path, []string{"p", "a"})
		if err != nil || program != path || !slices.Equal(args, []string{"p", "a"}) {
			t.Errorf("Resolve(%s) = %q %q, %v", path, program, args, err)
		}
	}
}

func TestResolveDoesNothingWithoutALinker(t *testing.T) {
	var s Starter
	program, args, err := s.Resolve(prefix+"/bin/git", []string{"git"})
	if err != nil || program != prefix+"/bin/git" || !slices.Equal(args, []string{"git"}) {
		t.Fatalf("Resolve = %q %q, %v", program, args, err)
	}
}

// A script starts through its interpreter, and the /bin and /usr/bin directories Android lacks map to $PREFIX/bin.
func TestResolveStartsAScriptThroughItsMappedInterpreter(t *testing.T) {
	for name, tc := range map[string]struct {
		script string
		want   []string
	}{
		"/bin/sh":            {"#!/bin/sh\necho\n", []string{linker, prefix + "/bin/sh", prefix + "/bin/tool", "x"}},
		"/usr/bin/bash":      {"#!/usr/bin/bash\n", []string{linker, prefix + "/bin/bash", prefix + "/bin/tool", "x"}},
		"one optional arg":   {"#!/bin/sh -e -u\n", []string{linker, prefix + "/bin/sh", "-e -u", prefix + "/bin/tool", "x"}},
		"tab separator":      {"#!/bin/sh\t-e\n", []string{linker, prefix + "/bin/sh", "-e", prefix + "/bin/tool", "x"}},
		"termux interpreter": {"#!" + prefix + "/bin/python\n", []string{linker, prefix + "/bin/python", prefix + "/bin/tool", "x"}},
	} {
		f := files{
			prefix + "/bin/tool":   tc.script,
			prefix + "/bin/sh":     elf,
			prefix + "/bin/bash":   elf,
			prefix + "/bin/python": elf,
		}
		program, args, err := f.starter().Resolve(prefix+"/bin/tool", []string{"tool", "x"})
		if err != nil || program != linker || !slices.Equal(args, tc.want) {
			t.Errorf("%s: Resolve = %q %q, %v; want %q", name, program, args, err, tc.want)
		}
	}
}

// env would exec its command with a libc execve that only termux-exec's preload redirects, so PiG finds the command itself.
func TestResolveRunsEnvCommandsDirectly(t *testing.T) {
	f := files{
		prefix + "/bin/script":  "#!/usr/bin/env python3\n",
		prefix + "/bin/python3": elf,
		prefix + "/bin/env":     elf,
	}
	_, args, err := f.starter().Resolve(prefix+"/bin/script", []string{"script", "a"})
	if want := []string{linker, prefix + "/bin/python3", prefix + "/bin/script", "a"}; err != nil || !slices.Equal(args, want) {
		t.Fatalf("args = %q, %v; want %q", args, err, want)
	}

	for name, line := range map[string]string{
		"env option":        "#!/usr/bin/env -S python3 -u\n",
		"env assignment":    "#!/usr/bin/env A=b\n",
		"command not found": "#!/usr/bin/env missing\n",
	} {
		f[prefix+"/bin/script"] = line
		_, args, err := f.starter().Resolve(prefix+"/bin/script", []string{"script"})
		if err != nil || len(args) < 2 || args[1] != prefix+"/bin/env" {
			t.Errorf("%s: args = %q, %v; want env through the linker", name, args, err)
		}
	}
}

func TestResolveRunsAFileWithoutMagicThroughTheSystemShell(t *testing.T) {
	f := files{prefix + "/bin/plain": "echo hi\n"}
	program, args, err := f.starter().Resolve(prefix+"/bin/plain", []string{"plain", "a"})
	if err != nil || program != "/system/bin/sh" || !slices.Equal(args, []string{"/system/bin/sh", prefix + "/bin/plain", "a"}) {
		t.Fatalf("Resolve = %q %q, %v", program, args, err)
	}
}

func TestResolveFollowsInterpretersToABoundedDepth(t *testing.T) {
	f := files{prefix + "/bin/a": "#!" + prefix + "/bin/b\n", prefix + "/bin/b": "#!" + prefix + "/bin/c\n", prefix + "/bin/c": elf}
	_, args, err := f.starter().Resolve(prefix+"/bin/a", []string{"a"})
	if want := []string{linker, prefix + "/bin/c", prefix + "/bin/b", prefix + "/bin/a"}; err != nil || !slices.Equal(args, want) {
		t.Fatalf("chain: args = %q, %v; want %q", args, err, want)
	}
	f[prefix+"/bin/c"] = "#!" + prefix + "/bin/a\n"
	if _, _, err := f.starter().Resolve(prefix+"/bin/a", []string{"a"}); err == nil || !strings.Contains(err.Error(), "interpreters") {
		t.Fatalf("a loop of interpreters: err = %v", err)
	}
}

func TestResolveReportsAnUnreadableProgram(t *testing.T) {
	if _, _, err := (files{}).starter().Resolve(prefix+"/bin/gone", []string{"gone"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want not exist", err)
	}
}

func TestParseShebang(t *testing.T) {
	for line, want := range map[string][2]string{
		"#!/bin/sh\n":                {"/bin/sh", ""},
		"#!  /bin/sh  \n":            {"/bin/sh", ""},
		"#!/usr/bin/env  node  \n":   {"/usr/bin/env", "node"},
		"#!/bin/sh -e\x00junk":       {"/bin/sh", "-e"},
		"#!/bin/sh":                  {"/bin/sh", ""},
		"#!/usr/bin/env -S a b c\nx": {"/usr/bin/env", "-S a b c"},
	} {
		interpreter, arg := parseShebang([]byte(line))
		if interpreter != want[0] || arg != want[1] {
			t.Errorf("parseShebang(%q) = %q, %q; want %q, %q", line, interpreter, arg, want[0], want[1])
		}
	}
}

func TestPrepareRewritesTheCommandAndKeepsItsOptions(t *testing.T) {
	f := files{prefix + "/bin/git": elf}
	cmd := exec.Command(prefix+"/bin/git", "log")
	cmd.Dir = "/sdcard"
	cmd.Env = []string{"A=1"}
	f.starter().Prepare(cmd)
	if cmd.Path != linker || !slices.Equal(cmd.Args, []string{linker, prefix + "/bin/git", "log"}) || cmd.Dir != "/sdcard" || !slices.Equal(cmd.Env, []string{"A=1"}) || cmd.Err != nil {
		t.Fatalf("cmd = %q %q dir=%q env=%q err=%v", cmd.Path, cmd.Args, cmd.Dir, cmd.Env, cmd.Err)
	}
}

func TestPrepareResolvesARelativePathAgainstTheCommandDirectory(t *testing.T) {
	f := files{prefix + "/bin/tool": elf}
	cmd := &exec.Cmd{Path: "./tool", Args: []string{"tool"}, Dir: prefix + "/bin"}
	f.starter().Prepare(cmd)
	if cmd.Path != linker || cmd.Args[1] != prefix+"/bin/tool" {
		t.Fatalf("cmd = %q %q", cmd.Path, cmd.Args)
	}
}

func TestPrepareSearchesTheCommandsOwnPATH(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "runner"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := files{prefix + "/bin/script": "#!/usr/bin/env runner\n", prefix + "/bin/env": elf}
	s := f.starter()
	s.LookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	cmd := &exec.Cmd{Path: prefix + "/bin/script", Args: []string{"script"}, Env: []string{"PATH=" + dir}}
	s.Prepare(cmd)
	// The command is found in the command's PATH, not the process's; it lies outside the data directory, so it starts directly.
	if want := []string{filepath.Join(dir, "runner"), prefix + "/bin/script"}; !slices.Equal(cmd.Args, want) {
		t.Fatalf("args = %q, want %q", cmd.Args, want)
	}
}

func TestPrepareKeepsAnExistingError(t *testing.T) {
	cmd := &exec.Cmd{Path: prefix + "/bin/git", Args: []string{"git"}, Err: exec.ErrNotFound}
	(files{prefix + "/bin/git": elf}).starter().Prepare(cmd)
	if cmd.Path != prefix+"/bin/git" || !errors.Is(cmd.Err, exec.ErrNotFound) {
		t.Fatalf("cmd = %q err=%v", cmd.Path, cmd.Err)
	}
}

func TestStarterForActsOnlyWhenTheLinkerStartedThisProcess(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	selfExe := func(path string, err error) func() (string, error) {
		return func() (string, error) { return path, err }
	}
	termux := map[string]string{"PREFIX": prefix}

	s := starterFor(env(termux), selfExe("/system/bin/linker64", nil))
	if !s.Active() || s.Linker != "/system/bin/linker64" || s.DataDir != dataDir || s.Prefix != prefix {
		t.Fatalf("linker-started: %+v", s)
	}
	if got := starterFor(env(termux), selfExe("/apex/com.android.runtime/bin/linker64", nil)); got.Linker != "/apex/com.android.runtime/bin/linker64" {
		t.Fatalf("apex linker: %+v", got)
	}
	if got := starterFor(env(termux), selfExe(prefix+"/bin/pig", nil)); got.Active() {
		t.Fatalf("directly started process rewrites starts: %+v", got)
	}
	if got := starterFor(env(termux), selfExe("", errors.New("no /proc"))); got.Active() {
		t.Fatalf("unreadable /proc/self/exe: %+v", got)
	}
	if got := starterFor(env(nil), selfExe("/system/bin/linker64", nil)); got.Active() {
		t.Fatalf("no PREFIX or data directory: %+v", got)
	}
	override := starterFor(env(map[string]string{"PREFIX": prefix, "TERMUX_APP__DATA_DIR": "/data/user/10/com.termux"}), selfExe("/system/bin/linker64", nil))
	if override.DataDir != "/data/user/10/com.termux" {
		t.Fatalf("TERMUX_APP__DATA_DIR ignored: %+v", override)
	}
}

// Under the linker /proc/self/exe is the linker, so the program is what termux-exec recorded or argv[0].
func TestProgramOfLinkedProcess(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for name, tc := range map[string]struct {
		env  map[string]string
		args []string
		want string
		fail bool
	}{
		"recorded":                     {map[string]string{"TERMUX_EXEC__PROC_SELF_EXE": prefix + "/bin/pig"}, []string{"pig"}, prefix + "/bin/pig", false},
		"absolute argv0":               {nil, []string{prefix + "/bin/pig", "x"}, prefix + "/bin/pig", false},
		"relative recorded is ignored": {map[string]string{"TERMUX_EXEC__PROC_SELF_EXE": "pig"}, []string{prefix + "/bin/pig"}, prefix + "/bin/pig", false},
		"unknown":                      {nil, []string{"pig"}, "", true},
		"no args":                      {nil, nil, "", true},
	} {
		got, err := programOfLinkedProcess(env(tc.env), tc.args)
		if (err != nil) != tc.fail || got != tc.want {
			t.Errorf("%s: = %q, %v; want %q, fail=%v", name, got, err, tc.want, tc.fail)
		}
	}
}

// A fake linker that, like the real one, runs its first argument with the rest, proves the rewritten command line starts real programs and a script chain.
func TestPrepareStartsRealProgramsThroughAFakeLinker(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "com.termux")
	pfx := filepath.Join(data, "files", "usr")
	bin := filepath.Join(pfx, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeLinker := filepath.Join(root, "linker64")
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(fakeLinker, "#!/bin/sh\nexec \"$@\"\n")
	sh, err := os.ReadFile("/bin/sh")
	if err != nil || !strings.HasPrefix(string(sh), "\x7fELF") {
		t.Skip("/bin/sh is not an ELF file")
	}
	write(filepath.Join(bin, "sh"), string(sh))
	write(filepath.Join(bin, "greet"), "#!/bin/sh\necho \"greet $1 $2\"\n")
	write(filepath.Join(bin, "wrapper"), "#!/usr/bin/env greet\n")
	s := Starter{Linker: fakeLinker, DataDir: data, Prefix: pfx, ReadHead: readHead, LookPath: pathLookup(bin)}

	for name, program := range map[string]string{"script": "greet", "script through env": "wrapper"} {
		cmd := exec.Command(filepath.Join(bin, program), "a", "b")
		s.Prepare(cmd)
		if cmd.Path != fakeLinker {
			t.Fatalf("%s: cmd.Path = %q, want the linker", name, cmd.Path)
		}
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want := "greet a b\n"
		if program == "wrapper" {
			want = "greet " + filepath.Join(bin, "wrapper") + " a\n"
		}
		if string(out) != want {
			t.Errorf("%s: output %q, want %q", name, out, want)
		}
	}
}

// Termux 0.118.3 sets TERMUX_APP__DATA_DIR to /data/user/0/com.termux while $PREFIX and every program path use
// /data/data/com.termux. termux-exec treats a path under either directory as an app data file; so does PiG.
func TestStarterForTreatsTheLegacyDataDirAsTheDataDir(t *testing.T) {
	env := map[string]string{"PREFIX": prefix, "TERMUX_APP__DATA_DIR": "/data/user/0/com.termux"}
	s := starterFor(func(k string) string { return env[k] }, func() (string, error) { return "/system/bin/linker64", nil })
	if s.DataDir != "/data/user/0/com.termux" || s.LegacyDataDir != dataDir {
		t.Fatalf("starter = %+v, want DataDir /data/user/0/com.termux and LegacyDataDir %s", s, dataDir)
	}
	s.ReadHead = files{prefix + "/bin/bash": elf}.starter().ReadHead
	program, args, err := s.Resolve(prefix+"/bin/bash", []string{"bash", "-c", "true"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/system/bin/linker64", prefix + "/bin/bash", "-c", "true"}; program != "/system/bin/linker64" || !slices.Equal(args, want) {
		t.Fatalf("Resolve = %q %q, want the linker starting bash: %q", program, args, want)
	}
	explicit := map[string]string{"PREFIX": prefix, "TERMUX_APP__DATA_DIR": "/data/user/0/com.termux", "TERMUX_APP__LEGACY_DATA_DIR": "/data/data/com.termux.legacy"}
	if got := starterFor(func(k string) string { return explicit[k] }, func() (string, error) { return "/system/bin/linker64", nil }); got.LegacyDataDir != "/data/data/com.termux.legacy" {
		t.Fatalf("TERMUX_APP__LEGACY_DATA_DIR ignored: %+v", got)
	}
}

// A child environment that repeats PATH (os.Environ() plus an override) uses the last entry, as execve does.
func TestEnvironmentPathUsesTheLastEntry(t *testing.T) {
	if got, ok := environmentPath([]string{"PATH=/first", "HOME=/h", "PATH=/last"}); !ok || got != "/last" {
		t.Fatalf("environmentPath = %q, %v; want /last", got, ok)
	}
	if _, ok := environmentPath([]string{"HOME=/h"}); ok {
		t.Fatal("environmentPath found a PATH in an environment without one")
	}
}
