package env

// The detection, deployment and launch of SshConnection against a scripted ssh, so Windows, Termux, Git Bash and
// Darwin hosts are covered without such a host (Pi's own tests reach them only through CI machines). The test binary
// is the scripted ssh: it answers the commands of a scenario file and records what it was asked and fed.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

const fakeSSHEnv = "PI_ENV_FAKE_SSH"

type fakeRule struct {
	// Match is a substring of the command, or of the decoded script when the command is an encoded PowerShell one.
	Match  string `json:"match"`
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	Exit   int    `json:"exit"`
}

type fakeScenario struct {
	Log   string     `json:"log"`
	Rules []fakeRule `json:"rules"`
}

type fakeCall struct {
	Args   []string `json:"args"`
	Script string   `json:"script"`
	Stdin  string   `json:"stdin"`
}

// decodeEncodedCommand returns the script of `powershell ... -EncodedCommand <base64>`, or the command itself.
func decodeEncodedCommand(command string) string {
	const marker = "-EncodedCommand "
	_, after, ok := strings.Cut(command, marker)
	if !strings.HasPrefix(command, "powershell ") || !ok {
		return command
	}
	raw, err := base64.StdEncoding.DecodeString(after)
	if err != nil || len(raw)%2 != 0 {
		return command
	}
	units := make([]uint16, len(raw)/2)
	for i := range units {
		units[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
	}
	return string(utf16.Decode(units))
}

func runFakeSSH(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	var scenario fakeScenario
	data, err := os.ReadFile(os.Getenv(fakeSSHEnv))
	if err != nil || json.Unmarshal(data, &scenario) != nil {
		_, _ = fmt.Fprintln(stderr, "fake ssh: no scenario")
		return 99
	}
	input, _ := io.ReadAll(stdin)
	script := decodeEncodedCommand(args[len(args)-1])
	log, _ := os.OpenFile(scenario.Log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	encoded, _ := json.Marshal(fakeCall{Args: args, Script: script, Stdin: base64.StdEncoding.EncodeToString(input)})
	_, _ = log.Write(append(encoded, '\n'))
	_ = log.Close()
	for _, rule := range scenario.Rules {
		if strings.Contains(script, rule.Match) {
			_, _ = io.WriteString(stdout, rule.Stdout)
			_, _ = io.WriteString(stderr, rule.Stderr)
			return rule.Exit
		}
	}
	_, _ = fmt.Fprintln(stderr, "fake ssh: unexpected command: "+script)
	return 98
}

// fakeHost starts a scenario and returns the target that reaches it and a reader of the calls so far.
func fakeHost(t *testing.T, rules ...fakeRule) (SshTarget, func() []fakeCall) {
	t.Helper()
	directory := t.TempDir()
	scenario := fakeScenario{Log: filepath.Join(directory, "calls.jsonl"), Rules: rules}
	mustDo(os.WriteFile(filepath.Join(directory, "scenario.json"), must(json.Marshal(scenario)), 0o600))
	t.Setenv(fakeSSHEnv, filepath.Join(directory, "scenario.json"))
	target := SshTarget{
		Host: "fake-host", Ssh: new(must(os.Executable())), KnownHostsFile: filepath.Join(directory, "known_hosts"), HostKeyAlias: "pi-env-fake",
	}
	return target, func() []fakeCall {
		var calls []fakeCall
		data, _ := os.ReadFile(scenario.Log)
		for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
			if line == "" {
				continue
			}
			var call fakeCall
			mustDo(json.Unmarshal([]byte(line), &call))
			calls = append(calls, call)
		}
		return calls
	}
}

// posixAnswer is what the POSIX probe prints: a banner, then the probe lines.
func posixAnswer(system, machine, os, home, tmpdir, preload string) string {
	return fmt.Sprintf("Welcome!\nPI-ENV-PROBE\n%s\n%s\n%s\n%s\n%s\n%s\n", system, machine, os, home, tmpdir, preload)
}

// windowsAnswer is what PowerShell prints for the Windows probe, with CRLF line ends.
func windowsAnswer(arch, home string) string {
	return fmt.Sprintf("PI-ENV-PROBE\r\nWindows\r\n%s\r\n-\r\n%s\r\n-\r\n-\r\n", arch, home)
}

const probeCommand = "uname -s"
const windowsProbeScript = "OSArchitecture"

func TestDetectPlatformOnScriptedHosts(t *testing.T) {
	ctx := context.Background()
	type expectation struct {
		name  string
		rules []fakeRule
		want  RemotePlatform
	}
	for _, c := range []expectation{
		{
			name:  "Linux x86-64",
			rules: []fakeRule{{Match: probeCommand, Stdout: posixAnswer("Linux", "x86_64", "GNU/Linux", "/home/me", "-", "-")}},
			want:  RemotePlatform{Platform: "linux", Arch: "x64", Home: "/home/me"},
		},
		{
			name:  "Darwin arm64",
			rules: []fakeRule{{Match: probeCommand, Stdout: posixAnswer("Darwin", "arm64", "Darwin", "/Users/me", "/var/folders/x", "-")}},
			want:  RemotePlatform{Platform: "darwin", Arch: "arm64", Home: "/Users/me"},
		},
		{
			name:  "Termux without TMPDIR and termux-exec",
			rules: []fakeRule{{Match: probeCommand, Stdout: posixAnswer("Linux", "aarch64", "Android", "/data/data/com.termux/files/home", "-", "-")}},
			want: RemotePlatform{Platform: "android", Arch: "arm64", Home: "/data/data/com.termux/files/home", Warnings: []string{
				"TMPDIR is not set; Termux's sshd normally sets it to $PREFIX/tmp.",
				"termux-exec is not loaded (LD_PRELOAD); scripts with #!/usr/bin/env shebangs will fail.",
				"Android may suspend Termux; run termux-wake-lock on the device to keep the connection alive.",
			}},
		},
		{
			name:  "Termux with TMPDIR and termux-exec",
			rules: []fakeRule{{Match: probeCommand, Stdout: posixAnswer("Linux", "aarch64", "Android", "/data/home", "/data/tmp", "/lib/libtermux-exec.so")}},
			want: RemotePlatform{Platform: "android", Arch: "arm64", Home: "/data/home", Warnings: []string{
				"Android may suspend Termux; run termux-wake-lock on the device to keep the connection alive.",
			}},
		},
		{
			// cmd.exe has no sh: the POSIX probe fails, then PowerShell answers; cmd.exe expands %OS%.
			name: "Windows with cmd.exe as the default shell",
			rules: []fakeRule{
				{Match: windowsProbeScript, Stdout: windowsAnswer("Arm64", `C:\Users\me`)},
				{Match: probeCommand, Stdout: "'sh' is not recognized as an internal or external command\r\n", Exit: 1},
				{Match: "echo %OS%", Stdout: "Windows_NT\r\n"},
			},
			want: RemotePlatform{Platform: "windows", Arch: "arm64", Home: `C:\Users\me`, Shell: "cmd"},
		},
		{
			name: "Windows with PowerShell as the default shell",
			rules: []fakeRule{
				{Match: windowsProbeScript, Stdout: windowsAnswer("X64", `C:\Users\me`)},
				{Match: probeCommand, Stdout: "garbage\n"},
				{Match: "echo %OS%", Stdout: "%OS%\r\n"},
			},
			want: RemotePlatform{Platform: "windows", Arch: "x64", Home: `C:\Users\me`, Shell: "powershell"},
		},
		{
			// Git Bash answers the POSIX probe with an MSYS system name and POSIX-spelled home: Windows' own is asked.
			name: "Windows with Git Bash as the default shell",
			rules: []fakeRule{
				{Match: windowsProbeScript, Stdout: windowsAnswer("X64", `C:\Users\me`)},
				{Match: probeCommand, Stdout: posixAnswer("MINGW64_NT-10.0-26100", "x86_64", "Msys", "/c/Users/me", "-", "-")},
				{Match: "echo %OS%", Stdout: "%OS%\n"},
			},
			want: RemotePlatform{Platform: "windows", Arch: "x64", Home: `C:\Users\me`, Shell: "powershell"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			target, _ := fakeHost(t, c.rules...)
			got, err := DetectPlatform(ctx, target)
			if err != nil {
				t.Fatal(err)
			}
			if got.Platform != c.want.Platform || got.Arch != c.want.Arch || got.Home != c.want.Home || got.Shell != c.want.Shell ||
				strings.Join(got.Warnings, "|") != strings.Join(c.want.Warnings, "|") {
				t.Fatalf("detected %+v, want %+v", got, c.want)
			}
		})
	}

	for _, c := range []struct {
		name    string
		answer  string
		message string
	}{
		{"an architecture without a daemon", posixAnswer("Linux", "riscv64", "GNU/Linux", "/h", "-", "-"), "Unsupported remote architecture: riscv64"},
		{"a system without a daemon", posixAnswer("FreeBSD", "x86_64", "FreeBSD", "/h", "-", "-"), "Unsupported remote system: FreeBSD"},
	} {
		t.Run("refuses "+c.name, func(t *testing.T) {
			target, _ := fakeHost(t, fakeRule{Match: probeCommand, Stdout: c.answer})
			if _, err := DetectPlatform(ctx, target); err == nil || err.Error() != c.message {
				t.Fatalf("error %v, want %q", err, c.message)
			}
		})
	}

	t.Run("reports an untrusted and a changed host key without trying PowerShell", func(t *testing.T) {
		target, calls := fakeHost(t, fakeRule{Match: probeCommand, Stderr: "Host key verification failed.\n", Exit: 255})
		if _, err := DetectPlatform(ctx, target); err == nil || !strings.Contains(err.Error(), "not trusted yet") {
			t.Fatalf("untrusted: %v", err)
		}
		if len(calls()) != 1 {
			t.Fatalf("%d ssh calls, want 1", len(calls()))
		}
		changed, _ := fakeHost(t, fakeRule{Match: probeCommand, Stderr: "@@@ WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED! @@@\n", Exit: 255})
		if _, err := DetectPlatform(ctx, changed); err == nil || !strings.Contains(err.Error(), "changed; remove the old key") {
			t.Fatalf("changed: %v", err)
		}
	})
}

func TestDeployDaemonOnScriptedHosts(t *testing.T) {
	ctx := context.Background()
	binary := filepath.Join(t.TempDir(), "pi-env")
	content := make([]byte, 100_003)
	for index := range content {
		content[index] = byte(index * 31)
	}
	mustDo(os.WriteFile(binary, content, 0o700))
	sum := sha256.Sum256(content)
	digest := hex.EncodeToString(sum[:])

	t.Run("Windows: uploads base64 lines up to an end marker and verifies the hash", func(t *testing.T) {
		target, calls := fakeHost(t,
			fakeRule{Match: "Test-Path -LiteralPath", Stdout: "missing\r\n"},
			fakeRule{Match: "PI-ENV-END", Stdout: "deployed\r\n"},
		)
		remote := RemotePlatform{Platform: "windows", Arch: "x64", Home: `C:\Users\me`, Shell: "cmd"}
		file, err := DeployDaemon(ctx, target, remote, new(binary))
		if err != nil {
			t.Fatal(err)
		}
		if want := `C:\Users\me\.pi\mobile\tools\pi-env-` + digest[:32] + ".exe"; file != want {
			t.Fatalf("file %q, want %q", file, want)
		}
		recorded := calls()
		if len(recorded) != 2 {
			t.Fatalf("%d calls, want a check and an upload", len(recorded))
		}
		upload := recorded[1]
		for _, want := range []string{"'" + digest + "'", "Move-Item -Force", "'PI-ENV-END'", "pi-env upload is corrupt", "pi-env-*.exe", `C:\Users\me\.pi\mobile\tools\pi-env-` + digest[:32] + ".exe"} {
			if !strings.Contains(upload.Script, want) {
				t.Errorf("the upload script lacks %q:\n%s", want, upload.Script)
			}
		}
		stdin := string(must(base64.StdEncoding.DecodeString(upload.Stdin)))
		lines := strings.Split(stdin, "\n")
		if lines[len(lines)-2] != "PI-ENV-END" || lines[len(lines)-1] != "" {
			t.Fatalf("the upload does not end with the marker line: %q", lines[max(0, len(lines)-3):])
		}
		for index, line := range lines[:len(lines)-2] {
			if len(line) > 76 || (len(line) < 76 && index != len(lines)-3) {
				t.Fatalf("line %d has %d characters; the upload is wrapped at 76", index, len(line))
			}
		}
		decoded := must(base64.StdEncoding.DecodeString(strings.Join(lines[:len(lines)-2], "")))
		if string(decoded) != string(content) {
			t.Fatal("the uploaded lines do not decode to the binary")
		}
	})

	t.Run("Windows: a present daemon is not uploaded", func(t *testing.T) {
		target, calls := fakeHost(t, fakeRule{Match: "Test-Path -LiteralPath", Stdout: "present\r\n"})
		remote := RemotePlatform{Platform: "windows", Arch: "x64", Home: `C:\Users\me`, Shell: "powershell"}
		if _, err := DeployDaemon(ctx, target, remote, new(binary)); err != nil {
			t.Fatal(err)
		}
		if len(calls()) != 1 {
			t.Fatalf("%d calls, want only the check", len(calls()))
		}
	})

	t.Run("POSIX: uploads the binary over stdin after a failed check", func(t *testing.T) {
		target, calls := fakeHost(t,
			fakeRule{Match: `echo present`, Stdout: "missing\n"},
			fakeRule{Match: "echo deployed", Stdout: "deployed\n"},
		)
		remote := RemotePlatform{Platform: "linux", Arch: "x64", Home: "/home/it's me"}
		file, err := DeployDaemon(ctx, target, remote, new(binary))
		if err != nil {
			t.Fatal(err)
		}
		if want := "/home/it's me/.pi/mobile/tools/pi-env-" + digest[:32]; file != want {
			t.Fatalf("file %q, want %q", file, want)
		}
		recorded := calls()
		if len(recorded) != 2 {
			t.Fatalf("%d calls, want a check and an upload", len(recorded))
		}
		if !strings.HasPrefix(recorded[1].Script, "sh -c '") || !strings.Contains(recorded[1].Script, `'\''`) || !strings.Contains(recorded[1].Script, "mkdir -p") ||
			!strings.Contains(recorded[1].Script, digest) {
			t.Fatalf("the upload command is not the quoted script:\n%s", recorded[1].Script)
		}
		if string(must(base64.StdEncoding.DecodeString(recorded[1].Stdin))) != string(content) {
			t.Fatal("the binary was not fed to the remote command")
		}
	})

	t.Run("POSIX: a host without a hash tool is refused", func(t *testing.T) {
		target, _ := fakeHost(t, fakeRule{Match: `echo present`, Stdout: "nohash\n"})
		_, err := DeployDaemon(ctx, target, RemotePlatform{Platform: "linux", Arch: "x64", Home: "/h"}, new(binary))
		if err == nil || err.Error() != "fake-host has no sha256sum, shasum or openssl to verify pi-env" {
			t.Fatalf("error %v", err)
		}
	})

	t.Run("a missing local binary names the system and path", func(t *testing.T) {
		target, _ := fakeHost(t)
		_, err := DeployDaemon(ctx, target, RemotePlatform{Platform: "darwin", Arch: "arm64", Home: "/h"}, new(filepath.Join(t.TempDir(), "absent")))
		if err == nil || !strings.HasPrefix(err.Error(), "No pi-env daemon for darwin-arm64 at ") {
			t.Fatalf("error %v", err)
		}
	})
}

func TestLaunchCommand(t *testing.T) {
	for _, c := range []struct {
		name       string
		remote     RemotePlatform
		file       string
		loginShell bool
		want       string
	}{
		{"POSIX quotes the path", RemotePlatform{Platform: "linux"}, "/home/it's/pi-env", false, `'/home/it'\''s/pi-env'`},
		{"POSIX through the login shell", RemotePlatform{Platform: "darwin"}, "/h/pi-env", true, `exec "$SHELL" -lc 'exec "$0" "$@"' '/h/pi-env'`},
		{"cmd.exe quotes only a path with a space", RemotePlatform{Platform: "windows", Shell: "cmd"}, `C:\Users\a b\pi-env.exe`, false, `"C:\Users\a b\pi-env.exe"`},
		{"cmd.exe keeps a plain path bare", RemotePlatform{Platform: "windows", Shell: "cmd"}, `C:\Users\me\pi-env.exe`, false, `C:\Users\me\pi-env.exe`},
		{"PowerShell needs the call operator", RemotePlatform{Platform: "windows", Shell: "powershell"}, `C:\Users\it's\pi-env.exe`, false, `& 'C:\Users\it''s\pi-env.exe'`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := launchCommand(c.remote, c.file, c.loginShell); got != c.want {
				t.Fatalf("launch command %q, want %q", got, c.want)
			}
		})
	}
}

func TestPowerShellCommandIsUTF16Base64(t *testing.T) {
	// `powershell -EncodedCommand` takes UTF-16LE base64 of the script; "'é'" is 27 00 e9 00 27 00.
	if got, want := powershell("'é'"), "powershell -NoProfile -NonInteractive -EncodedCommand JwDpACcA"; got != want {
		t.Fatalf("encoded command %q, want %q", got, want)
	}
	if decodeEncodedCommand(powershell("'x'; 😀")) != "'x'; 😀" {
		t.Fatal("a script with a surrogate pair does not survive the encoding")
	}
}
