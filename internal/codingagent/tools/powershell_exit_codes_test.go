package tools

// pi: packages/coding-agent/src/core/tools/powershell.ts (v1.5.1 c9dd2c53, #11964, fixes #8842)

import (
	"errors"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/nodepath"
)

// The suffix is appended for powershell.exe only, whatever the host: the shell path decides, with Windows path rules and any letter case.
func TestPowerShellCommandAppendsTheExitSuffixForWindowsPowerShellOnly(t *testing.T) {
	const suffix = "\n;\nif ($?) { exit 0 }\nif (($LASTEXITCODE -eq 0) -and (\"$($Error[0].FullyQualifiedErrorId)\" -like \"NativeCommandError*\")) { exit 0 }\nexit 1"
	for _, tc := range []struct {
		name, shell, command, want string
	}{
		{"powershell.exe", `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, "git push 2>&1", powerShellUTF8OutputPrefix + "git push 2>&1" + suffix},
		{"upper-case file name", `C:\Windows\System32\WindowsPowerShell\v1.0\PowerShell.EXE`, "git push", powerShellUTF8OutputPrefix + "git push" + suffix},
		{"bare name", "powershell.exe", "x", powerShellUTF8OutputPrefix + "x" + suffix},
		{"pwsh.exe is unchanged", `C:\Program Files\PowerShell\7\pwsh.exe`, "git push 2>&1", powerShellUTF8OutputPrefix + "git push 2>&1"},
		{"a command ending in a backtick is unchanged", `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, "Write-Output done `", powerShellUTF8OutputPrefix + "Write-Output done `"},
		{"a backtick inside the command still gets the suffix", "powershell.exe", "Write-Output `a", powerShellUTF8OutputPrefix + "Write-Output `a" + suffix},
		{"not powershell.exe by prefix", "powershell.exe.bak", "x", powerShellUTF8OutputPrefix + "x"},
	} {
		if got := powerShellCommand(tc.command, ShellConfig{Path: tc.shell}); got != tc.want {
			t.Errorf("%s: powerShellCommand = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// createLocalPowerShellOperations resolves PowerShell before anything else runs: a missing PowerShell is the error even when the timeout is invalid
// or the call is already aborted, and the shell it resolved is the one the command runs in.
func TestLocalPowerShellOperationsResolveTheShellFirst(t *testing.T) {
	ops := NewLocalPowerShellOperations()
	calls := 0
	missing := errors.New("No PowerShell executable found.")
	ops.ResolveShell = func() (ShellConfig, error) { calls++; return ShellConfig{}, missing }
	if _, err := ops.Exec(t.Context(), "x", t.TempDir(), BashOperationsExecOptions{Timeout: new(-1.0)}); !errors.Is(err, missing) || calls != 1 {
		t.Fatalf("Exec = %v after %d resolutions, want the missing-shell error from one", err, calls)
	}
}

func findPowerShell(t *testing.T, fileName string) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		return ""
	}
	var candidate string
	if fileName == "powershell.exe" {
		root := os.Getenv("SystemRoot")
		if root == "" {
			root = `C:\Windows`
		}
		candidate = nodepath.Win32Join(root, "System32", "WindowsPowerShell", "v1.0", fileName)
	} else {
		files := os.Getenv("ProgramFiles")
		if files == "" {
			files = `C:\Program Files`
		}
		candidate = nodepath.Win32Join(files, "PowerShell", "7", fileName)
	}
	configured, err := GetPowerShellConfig()
	if err == nil && strings.EqualFold(nodepath.Win32Basename(configured.Path), fileName) {
		candidate = configured.Path
	}
	if _, err := os.Stat(candidate); err != nil {
		return ""
	}
	return candidate
}

// #8842: Windows PowerShell 5.1 turned redirected native stderr into a failed command although the native command exited 0. Both shells must report
// the exit codes PowerShell 7 reports. Only Windows CI can run this: the rows start powershell.exe and pwsh.exe, each skipped when not installed.
func TestPowerShellExitCodes(t *testing.T) {
	const stderrExit0 = `cmd /c "echo progress 1>&2 & exit 0"`
	const stderrExit7 = `cmd /c "echo progress 1>&2 & exit 7"`
	type row struct {
		command  string
		exitCode int
	}
	cases := []row{
		{stderrExit0 + " 2>&1 | Select-Object -Last 3", 0},
		{stderrExit0 + " 2>&1", 0},
		{stderrExit0 + " 2>$null", 0},
		{"$output = " + stderrExit0 + " 2>&1", 0},
		{stderrExit0, 0},
		{`Get-Item C:\pi-missing-8842; ` + stderrExit0 + " 2>&1", 0},
		{stderrExit7 + " 2>&1 | Select-Object -Last 3", 1},
		{stderrExit7, 1},
		{`cmd /c "exit 3"`, 1},
		{`Get-Item C:\pi-missing-8842`, 1},
		{stderrExit0 + ` 2>&1; Get-Item C:\pi-missing-8842`, 1},
		{`Write-Error "failed"`, 1},
		{`throw "failed"`, 1},
		{stderrExit7 + "; Write-Output done", 0},
		{"Write-Output done # comment", 0},
		{"Set-StrictMode -Version Latest; " + stderrExit0 + " 2>&1", 0},
		{"exit 5", 5},
	}
	// Unfinished commands are parse errors: nothing runs, not even the first statement.
	var unfinished []string
	for _, statement := range []string{"Write-Output done `", "Get-ChildItem |", "$true -and", "Write-Output a,", "$x =", "Write-Output (1 +", `Write-Output "unclosed`, "if ($true) {"} {
		unfinished = append(unfinished, `Write-Output ("pi-" + "ran"); `+statement)
	}
	for _, fileName := range []string{"powershell.exe", "pwsh.exe"} {
		t.Run(fileName, func(t *testing.T) {
			shell := findPowerShell(t, fileName)
			if shell == "" {
				t.Skipf("%s is not installed (these rows only run on Windows)", fileName)
			}
			ops := NewLocalPowerShellOperations()
			ops.ResolveShell = func() (ShellConfig, error) {
				return ShellConfig{Path: shell, Args: append([]string(nil), PowerShellArgs...)}, nil
			}
			cwd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			var got []row
			for _, c := range cases {
				result, err := ops.Exec(t.Context(), c.command, cwd, BashOperationsExecOptions{OnData: func([]byte) {}})
				if err != nil || result.ExitCode == nil {
					t.Fatalf("Exec(%q) = %+v, %v", c.command, result, err)
				}
				got = append(got, row{c.command, *result.ExitCode})
			}
			if !reflect.DeepEqual(got, cases) {
				t.Fatalf("exit codes = %v, want %v", got, cases)
			}
			for _, command := range unfinished {
				var output strings.Builder
				result, err := ops.Exec(t.Context(), command, cwd, BashOperationsExecOptions{OnData: func(data []byte) { output.Write(data) }})
				if err != nil || result.ExitCode == nil || *result.ExitCode != 1 || strings.Contains(output.String(), "pi-ran") {
					t.Fatalf("Exec(%q) = %+v, %v, output %q; want exit 1 and nothing run", command, result, err, output.String())
				}
			}
		})
	}
}
