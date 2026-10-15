package env

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	keyOne = "AAAAC3NzaC1lZDI1NTE5AAAAIOne" // gitleaks:allow -- truncated public host key fixture, not a credential
	keyTwo = "AAAAC3NzaC1lZDI1NTE5AAAAITwo" // gitleaks:allow -- truncated public host key fixture, not a credential
)

// packages/env/src/ssh.ts:258-280 acceptHostKey: only lines for the target's alias count as trusted (:264), so a key another host trusts under the same type
// does not make this host's key "changed"; a different key of a type this alias trusts is HostKeyChangedError with the pinned message (:272-273); two
// lines of one type in one call are checked against each other (:275 trusted.set); the same key twice is stored once (:271).
func TestAcceptHostKeyChecksOnlyItsAliasAndWithinOneCall(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "known_hosts")
	target := SshTarget{Host: "h.example", KnownHostsFile: file, HostKeyAlias: "mine"}
	mustDo(os.WriteFile(file, []byte("other ssh-ed25519 "+keyTwo+"\n"), 0o600))
	mustDo(AcceptHostKey(target, []string{"mine ssh-ed25519 " + keyOne, "mine ssh-ed25519 " + keyOne}))
	content := string(must(os.ReadFile(file)))
	if content != "other ssh-ed25519 "+keyTwo+"\nmine ssh-ed25519 "+keyOne+"\n" {
		t.Fatalf("known_hosts = %q: want the other host kept, this key stored once", content)
	}
	err := AcceptHostKey(target, []string{"mine ssh-ed25519 " + keyTwo})
	var changed *HostKeyChangedError
	if !errors.As(err, &changed) || changed.Message != "The ssh-ed25519 host key of h.example changed; forget the old key first" {
		t.Fatalf("a different key of a trusted type = %v, want HostKeyChangedError with Pi's message", err)
	}
	fresh := SshTarget{Host: "g.example", KnownHostsFile: filepath.Join(t.TempDir(), "known_hosts"), HostKeyAlias: "fresh"}
	err = AcceptHostKey(fresh, []string{"fresh ssh-ed25519 " + keyOne, "fresh ssh-ed25519 " + keyTwo})
	if !errors.As(err, &changed) {
		t.Fatalf("two different keys of one type in one call = %v, want HostKeyChangedError", err)
	}
	if _, statErr := os.Stat(fresh.KnownHostsFile); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("a refused accept wrote the known-hosts file: %v", statErr)
	}
}

// ssh.ts:282-290 forgetHostKey removes every line of the alias and keeps the rest; changeKnownHosts (:228-250) creates the directory with mode 0700 and
// writes the file with mode 0600, and an emptied file is empty (not a lone newline).
func TestForgetHostKeyKeepsOtherHostsAndKnownHostsKeepsItsModes(t *testing.T) {
	t.Parallel()
	directory := filepath.Join(t.TempDir(), "nested", ".ssh")
	file := filepath.Join(directory, "known_hosts")
	target := SshTarget{Host: "h", KnownHostsFile: file, HostKeyAlias: "mine"}
	mustDo(AcceptHostKey(target, []string{"mine ssh-ed25519 " + keyOne, "mine ssh-rsa " + keyTwo}))
	if info := must(os.Stat(directory)); info.Mode().Perm() != 0o700 {
		t.Errorf("the known-hosts directory mode = %v, want 0700", info.Mode().Perm())
	}
	if info := must(os.Stat(file)); info.Mode().Perm() != 0o600 {
		t.Errorf("the known-hosts file mode = %v, want 0600", info.Mode().Perm())
	}
	mustDo(os.WriteFile(file, []byte("mine ssh-ed25519 "+keyOne+"\n   \nother ssh-ed25519 "+keyTwo+"\nmine ssh-rsa "+keyTwo+"\n"), 0o600))
	mustDo(ForgetHostKey(target))
	// A line of only blanks has no host, so it is not the alias's and is kept as it is (ssh.ts:283-288 compares the first field).
	if content := string(must(os.ReadFile(file))); content != "   \nother ssh-ed25519 "+keyTwo+"\n" {
		t.Fatalf("after forgetting the alias the file is %q, want only the other host", content)
	}
	mustDo(os.WriteFile(file, []byte("other ssh-ed25519 "+keyTwo+"\n"), 0o600))
	mustDo(ForgetHostKey(SshTarget{Host: "o", KnownHostsFile: file, HostKeyAlias: "other"}))
	if content := string(must(os.ReadFile(file))); content != "" {
		t.Fatalf("a file with no lines left is %q, want empty", content)
	}
	if strings.Contains(string(must(os.ReadFile(file))), "\n") {
		t.Fatal("an emptied known-hosts file holds a newline")
	}
}

// writeScanSsh makes a scripted ssh program that records the key a real ssh would capture in the UserKnownHostsFile it is given (ssh.ts scanHostKey
// passes the temporary file, quoted, with -o UserKnownHostsFile=...), then exits with code.
func writeScanSsh(t *testing.T, knownHostsLine string, code int) string {
	t.Helper()
	script := filepath.Join(t.TempDir(), "ssh")
	body := "#!/bin/sh\nfor arg in \"$@\"; do case \"$arg\" in UserKnownHostsFile=*) file=${arg#UserKnownHostsFile=};; esac; done\nfile=$(printf %s \"$file\" | tr -d '\"')\n"
	if knownHostsLine != "" {
		body += "printf '%s\\n' '" + knownHostsLine + "' > \"$file\"\n"
	}
	body += "echo 'Permission denied (publickey).' >&2\nexit " + strconv.Itoa(code) + "\n"
	writeProgram(t, script, body)
	return script
}

// ssh.ts scanHostKey: the key ssh records before authentication is returned even when authentication fails (the SshError is tolerated), with the
// ssh-keygen fingerprints of the captured file; no recorded key is "No host key received from <host>".
func TestScanHostKeyReturnsTheRecordedKeyEvenWhenAuthenticationFails(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen is not installed")
	}
	key := filepath.Join(t.TempDir(), "id")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v %s", err, out)
	}
	fields := strings.Fields(string(must(os.ReadFile(key + ".pub"))))
	line := "pi-env-scan " + fields[0] + " " + fields[1]
	target := SshTarget{Host: "scan.example", KnownHostsFile: filepath.Join(t.TempDir(), "known_hosts"), HostKeyAlias: "pi-env-scan", Ssh: new(writeScanSsh(t, line, 255))}
	lines, fingerprints, err := ScanHostKey(context.Background(), target)
	if err != nil {
		t.Fatalf("ScanHostKey after a failed authentication: %v", err)
	}
	if len(lines) != 1 || lines[0] != line {
		t.Fatalf("lines = %q, want the recorded key line", lines)
	}
	if len(fingerprints) != 1 || !strings.Contains(fingerprints[0], "SHA256:") {
		t.Fatalf("fingerprints = %q, want one ssh-keygen fingerprint", fingerprints)
	}
	empty := SshTarget{Host: "scan.example", KnownHostsFile: filepath.Join(t.TempDir(), "known_hosts"), HostKeyAlias: "pi-env-scan", Ssh: new(writeScanSsh(t, "", 0))}
	if _, _, err := ScanHostKey(context.Background(), empty); err == nil || err.Error() != "No host key received from scan.example" {
		t.Fatalf("ScanHostKey with no recorded key = %v", err)
	}
}

// ssh.ts:527-534 connectSsh deploys the daemon once and hands that verified path to the connection: the connection's first start does not
// check and deploy again (:492-496 `verified`), so one check of the daemon runs for ConnectSsh plus the first request.
func TestConnectSshHandsItsVerifiedDaemonToTheFirstStart(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "pi-env")
	mustDo(os.WriteFile(binary, []byte("daemon"), 0o700))
	target, calls := fakeHost(t,
		fakeRule{Match: probeCommand, Stdout: posixAnswer("Linux", "x86_64", "GNU/Linux", "/home/me", "-", "-")},
		fakeRule{Match: "echo present", Stdout: "present\n"},
		fakeRule{Match: ".pi/mobile/tools/pi-env-", Stdout: "", Exit: 1},
	)
	connection, _, err := ConnectSsh(context.Background(), SshConnectOptions{SshTarget: target, Binary: &binary})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	checks := func() int {
		count := 0
		for _, call := range calls() {
			if strings.Contains(call.Script, "echo present") {
				count++
			}
		}
		return count
	}
	if checks() != 1 {
		t.Fatalf("ConnectSsh checked the daemon %d times, want 1", checks())
	}
	// The scripted ssh answers a launch only when its stdin ends, so the start cannot be seen to finish. A start that checks the daemon again does it
	// before it launches, within moments of its request; the test gives it three seconds.
	startCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = connection.Request(startCtx, "hello", Json{}, RequestOptions{})
	if checks() != 1 {
		t.Fatalf("the first start checked the daemon again: %d checks, want the 1 of ConnectSsh", checks())
	}
}

// ssh.ts:224-250 changeKnownHosts chains the changes to one file (knownHostsChanges), so concurrent accepts each read the
// file the previous change wrote and none is lost; a failed change does not stop the next (:231 .catch).
// mutation-checked: the mutant "changeKnownHosts does not lock the file" fails it.
func TestConcurrentHostKeyChangesToOneFileAreAppliedOneAtATime(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "known_hosts")
	target := SshTarget{Host: "h.example", KnownHostsFile: file, HostKeyAlias: "mine"}
	const changes = 32
	var wg sync.WaitGroup
	failures := make([]error, changes)
	for index := range changes {
		wg.Go(func() {
			if index == changes/2 {
				// A change that fails between the others.
				failures[index] = changeKnownHosts(file, func([]string) ([]string, error) { return nil, errors.New("refused") })
				return
			}
			failures[index] = AcceptHostKey(target, []string{"mine ssh-t" + strconv.Itoa(index) + " " + keyOne})
		})
	}
	wg.Wait()
	for index, err := range failures {
		if (index == changes/2) != (err != nil) {
			t.Fatalf("change %d: %v", index, err)
		}
	}
	lines := strings.Split(strings.TrimSuffix(string(must(os.ReadFile(file))), "\n"), "\n")
	if len(lines) != changes-1 {
		t.Fatalf("known_hosts holds %d of %d accepted keys: %q", len(lines), changes-1, lines)
	}
	leftovers, _ := filepath.Glob(file + ".*.tmp")
	if len(leftovers) != 0 {
		t.Fatalf("temporary files left: %v", leftovers)
	}
}
