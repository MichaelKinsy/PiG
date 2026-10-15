package env

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// ssh.ts:191,201 keep a line unless line.trim() is empty; trim removes U+FEFF and Unicode spaces but not U+0085 (strings.TrimSpace does the opposite).
func TestNonBlankLinesTrimsLikeJavaScript(t *testing.T) {
	t.Parallel()
	got := nonBlankLines("\ufeff\nkeep\n\u00a0 \u3000\n\u0085\n")
	if want := []string{"keep", "\u0085"}; !slices.Equal(got, want) {
		t.Errorf("nonBlankLines = %q, want %q", got, want)
	}
}

// ssh.ts:260 stores line.trim() for an accepted host key: a leading BOM goes and a trailing U+0085 stays (it is not JavaScript whitespace).
func TestAcceptHostKeyStoresTheLineTrimmedLikeJavaScript(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "known_hosts")
	target := SshTarget{Host: "h", KnownHostsFile: file, HostKeyAlias: "pi-env-test"}
	mustDo(AcceptHostKey(target, []string{"\ufeffpi-env-test ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTY=\u00a0"}))
	if content := string(must(os.ReadFile(file))); content != "pi-env-test ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTY=\n" {
		t.Fatalf("known_hosts = %q", content)
	}
}

// ssh.ts:170 reports `diagnostics.trim()`: the BOM before and the no-break space after go, U+0085 stays.
func TestSshErrorMessageTrimsTheDiagnosticsLikeJavaScript(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("scripted ssh is a POSIX shell script")
	}
	program := filepath.Join(t.TempDir(), "ssh")
	writeProgram(t, program, "#!/bin/sh\nprintf '\\357\\273\\277boom\\302\\205\\302\\240' >&2\nexit 3\n")
	target := SshTarget{Host: "h", Ssh: &program, KnownHostsFile: filepath.Join(t.TempDir(), "known_hosts"), HostKeyAlias: "pi-env-test"}
	_, err := runSsh(context.Background(), target, "true", sshRun{})
	sshErr, ok := errors.AsType[*SshError](err)
	if !ok || sshErr.Message != "ssh h failed with exit code 3: boom\u0085" {
		t.Fatalf("err = %v, want the message with boom\\u0085", err)
	}
}

// ssh.ts:312 reports `output.trim()` when the probe marker is missing.
func TestParseProbeReportsTheAnswerTrimmedLikeJavaScript(t *testing.T) {
	t.Parallel()
	_, err := parseProbe(SshTarget{Host: "h"}, "\ufeff banner\u0085\u3000\n")
	if err == nil || err.Error() != "Unexpected answer from h: banner\u0085" {
		t.Fatalf("err = %v", err)
	}
}

// ssh.ts:388 takes the last line of `output.trim()`: a U+0085 after "present" is not whitespace, so the daemon is not present.
func TestCheckDaemonTrimsTheAnswerLikeJavaScript(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("scripted ssh is a POSIX shell script")
	}
	for answer, want := range map[string]string{`present\n\357\273\277`: "present", `present\302\205\n`: "present\u0085"} {
		program := filepath.Join(t.TempDir(), "ssh")
		writeProgram(t, program, "#!/bin/sh\nprintf '"+answer+"'\n")
		target := SshTarget{Host: "h", Ssh: &program, KnownHostsFile: filepath.Join(t.TempDir(), "known_hosts"), HostKeyAlias: "pi-env-test"}
		got, err := checkDaemon(context.Background(), target, RemotePlatform{Platform: "linux"}, "/f", "d")
		if err != nil || got != want {
			t.Errorf("answer %s: checkDaemon = %q, %v; want %q", answer, got, err, want)
		}
	}
}
