package shellconfig

import (
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestIsLegacyWSLBashPath(t *testing.T) {
	for path, want := range map[string]bool{
		`C:\Windows\System32\bash.exe`:      true,
		`c:/windows/sysnative/bash.exe`:     true,
		`C:\Program Files\Git\bin\bash.exe`: false,
		`/bin/bash`:                         false,
	} {
		if got := isLegacyWSLBashPath(path, ""); got != want {
			t.Errorf("isLegacyWSLBashPath(%q, \"\") = %v, want %v", path, got, want)
		}
	}
	t.Setenv("SystemRoot", "")
	cfg := ForBash(`C:\Windows\System32\bash.exe`)
	if strings.Join(cfg.Args, " ") != "-s" || cfg.CommandTransport != "stdin" {
		t.Fatalf("legacy WSL cfg = %+v", cfg)
	}
}

// Ports packages/coding-agent/test/windows-shell-config.test.ts (#9490): legacy WSL launchers are identified under the actual
// Windows directory. A SystemRoot that is not a drive path matches nothing, and an empty or unset one falls back to the
// conventional C:\Windows-style launcher paths.
func TestUpstreamLegacyWSLBashDetection(t *testing.T) {
	unset, winNT, relative := (*string)(nil), new(`D:\WinNT\`), new("WinNT")
	for _, tc := range []struct {
		systemRoot *string
		shell      string
		stdin      bool
	}{
		{winNT, "d:/WINNT/System32/BASH.EXE", true},
		{new(`D:\WinNT`), `D:\WinNT\Sysnative\bash.exe`, true},
		{unset, `E:\Windows\System32\bash.exe`, true},
		{new(`D:\WinNT`), `D:\Git\bin\bash.exe`, false},
		{new(`D:\WinNT`), `E:\Windows\System32\bash.exe`, false},
		{relative, `C:\Windows\System32\bash.exe`, false},
	} {
		root := "undefined"
		if tc.systemRoot != nil {
			root = strconv.Quote(*tc.systemRoot)
		}
		t.Run(root+" / "+strconv.Quote(tc.shell)+" to stdin="+strconv.FormatBool(tc.stdin), func(t *testing.T) {
			t.Setenv("SystemRoot", "")
			if tc.systemRoot == nil {
				if err := os.Unsetenv("SystemRoot"); err != nil {
					t.Fatal(err)
				}
			} else {
				t.Setenv("SystemRoot", *tc.systemRoot)
			}
			want := Config{Path: tc.shell, Args: []string{"-c"}}
			if tc.stdin {
				want = Config{Path: tc.shell, Args: []string{"-s"}, CommandTransport: "stdin"}
			}
			if got := ForBash(tc.shell); !reflect.DeepEqual(got, want) {
				t.Fatalf("ForBash(%q) = %+v, want %+v", tc.shell, got, want)
			}
		})
	}
}

// An empty SystemRoot is falsy in upstream's `if (systemRoot)`, so it takes the conventional-path fallback, not the
// "not a drive path" rejection.
func TestEmptySystemRootFallsBackToConventionalLauncherPaths(t *testing.T) {
	if !isLegacyWSLBashPath(`C:\Windows\System32\bash.exe`, "") {
		t.Fatal("empty SystemRoot did not fall back to the conventional launcher path")
	}
}

// Ports the `where` handling of packages/coding-agent/test/shell-path-exists.test.ts: the first line of the output, whatever
// its line ending, is the match, and on Windows it must pass the access check (not existsSync) to count.
func TestFirstMatch(t *testing.T) {
	always, never := func(string) bool { return true }, func(string) bool { return false }
	alias := `D:\Users\test\AppData\Local\Microsoft\WindowsApps\bash.exe`
	for _, tc := range []struct {
		name    string
		output  string
		windows bool
		exists  func(string) bool
		want    string
	}{
		{"a runnable Store alias passes access", alias + "\r\n", true, always, alias},
		{"the first of several lines", "C:\\a\\bash.exe\r\nC:\\b\\bash.exe\r\n", true, always, `C:\a\bash.exe`},
		{"a path that fails access is not a match", alias, true, never, ""},
		{"which output is trusted without a check", "/not/executable/bash\n", false, never, "/not/executable/bash"},
		{"empty output", " \n", true, always, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := FirstMatch(tc.output, tc.windows, tc.exists); got != tc.want {
				t.Fatalf("FirstMatch = %q, want %q", got, tc.want)
			}
		})
	}
}

// Port of Pi main e8ee38887e4aeacb0e15e8a3e367605347af3575 (#12408): find Git Bash of a Git for Windows
// installed outside Program Files through git on PATH.
func TestUpstreamGitBashDiscoveryThroughGitOnPath(t *testing.T) {
	const (
		gitPath = `G:\Programmi Aggiuntivi\Git\cmd\git.exe`
		gitBash = `G:\Programmi Aggiuntivi\Git\bin\bash.exe`
		wslBash = `C:\Windows\System32\bash.exe`
	)
	t.Setenv("SystemRoot", `C:\Windows`)

	installed := func(paths ...string) func(string) bool {
		set := make(map[string]bool, len(paths))
		for _, path := range paths {
			set[path] = true
		}
		return func(path string) bool { return set[path] }
	}
	makeLookup := func(gitShellPath *string, calls *int) (func(string) string, func(string, ...string) (string, bool)) {
		findOnPath := func(exe string) string {
			switch exe {
			case "git.exe":
				return gitPath
			case "bash.exe":
				return wslBash
			default:
				return ""
			}
		}
		run := func(name string, args ...string) (string, bool) {
			if calls != nil {
				*calls++
			}
			if name == gitPath && len(args) == 2 && args[0] == "var" && args[1] == "GIT_SHELL_PATH" {
				if gitShellPath == nil {
					return "", false
				}
				return *gitShellPath + "\n", true
			}
			t.Fatalf("unexpected run: %s %v", name, args)
			return "", false
		}
		return findOnPath, run
	}

	t.Run("prefers Git Bash of git on PATH over the WSL launcher on PATH", func(t *testing.T) {
		shellPath := "G:/Programmi Aggiuntivi/Git/usr/bin/sh.exe"
		findOnPath, run := makeLookup(&shellPath, nil)
		got, err := windowsDefault(`C:\Program Files`, `C:\Program Files (x86)`, installed(gitPath, gitBash, wslBash), findOnPath, run)
		if err != nil {
			t.Fatal(err)
		}
		want := Config{Path: gitBash, Args: []string{"-c"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("keeps Git Bash under ProgramFiles first without running git", func(t *testing.T) {
		const programFilesBash = `C:\Program Files\Git\bin\bash.exe`
		shellPath := "G:/Programmi Aggiuntivi/Git/usr/bin/sh.exe"
		var calls int
		findOnPath, run := makeLookup(&shellPath, &calls)
		got, err := windowsDefault(`C:\Program Files`, `C:\Program Files (x86)`, installed(programFilesBash, gitPath, gitBash), findOnPath, run)
		if err != nil {
			t.Fatal(err)
		}
		want := Config{Path: programFilesBash, Args: []string{"-c"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
		if calls != 0 {
			t.Fatalf("git was spawned %d times, want 0", calls)
		}
	})

	for _, tc := range []struct {
		name         string
		gitShellPath *string
	}{
		{"git without GIT_SHELL_PATH (before 2.47)", nil},
		{"a shell outside a Git for Windows layout", new("G:/Tools/sh.exe")},
		{"a Git for Windows root without its Bash launcher", new("G:/Programmi Aggiuntivi/Other/usr/bin/sh.exe")},
	} {
		t.Run("falls back to bash.exe on PATH for "+tc.name, func(t *testing.T) {
			findOnPath, run := makeLookup(tc.gitShellPath, nil)
			got, err := windowsDefault(`C:\Program Files`, `C:\Program Files (x86)`, installed(gitPath, gitBash, wslBash), findOnPath, run)
			if err != nil {
				t.Fatal(err)
			}
			want := Config{Path: wslBash, Args: []string{"-s"}, CommandTransport: "stdin"}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %+v, want %+v", got, want)
			}
		})
	}
}
