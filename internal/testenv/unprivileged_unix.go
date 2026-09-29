//go:build unix

package testenv

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// unprivilegedResultEnv names the file where a rerun test records "pass", "fail", or "skip".
const unprivilegedResultEnv = "PIG_TESTENV_UNPRIVILEGED_RESULT"

func runUnprivileged(t *testing.T) bool {
	t.Helper()
	if result := os.Getenv(unprivilegedResultEnv); result != "" {
		if os.Geteuid() == 0 {
			t.Fatal("unprivileged rerun still has effective user ID 0")
		}
		t.Cleanup(func() {
			outcome := "pass"
			switch {
			case t.Skipped():
				outcome = "skip"
			case t.Failed():
				outcome = "fail"
			}
			if err := os.WriteFile(result, []byte(outcome), 0o600); err != nil {
				t.Errorf("record unprivileged result: %v", err)
			}
		})
		return false
	}
	if os.Geteuid() != 0 {
		return false
	}
	nobody, err := user.Lookup("nobody")
	if err != nil {
		t.Fatalf("root bypasses permission bits and no unprivileged user is available: %v", err)
	}
	uid, uidErr := strconv.ParseUint(nobody.Uid, 10, 31)
	gid, gidErr := strconv.ParseUint(nobody.Gid, 10, 31)
	if err := errors.Join(uidErr, gidErr); err != nil {
		t.Fatalf("parse nobody IDs %s:%s: %v", nobody.Uid, nobody.Gid, err)
	}
	dir, err := os.MkdirTemp("", "testenv-unprivileged-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("remove %s: %v", dir, err)
		}
	})
	binary := filepath.Join(dir, "test.bin")
	result := filepath.Join(dir, "result")
	home, tmp := filepath.Join(dir, "home"), filepath.Join(dir, "tmp")
	if err := setupUnprivilegedDir(dir, binary, result, home, tmp, int(uid), int(gid)); err != nil {
		t.Fatalf("prepare unprivileged rerun: %v", err)
	}
	parts := strings.Split(t.Name(), "/")
	for i, part := range parts {
		parts[i] = "^" + regexp.QuoteMeta(part) + "$"
	}
	cmd := exec.CommandContext(t.Context(), binary, "-test.run="+strings.Join(parts, "/"), "-test.count=1")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+home, "TMPDIR="+tmp, unprivilegedResultEnv+"="+result)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{}}}
	output, runErr := cmd.CombinedOutput()
	outcome, err := os.ReadFile(result)
	if err != nil || runErr != nil || string(outcome) != "pass" {
		t.Fatalf("unprivileged rerun as %s (uid %d) did not pass: outcome %q, %v, %v\n%s", nobody.Username, uid, outcome, runErr, err, output)
	}
	// Preserve the test's own stdout for callers that read it; the testing framework's final PASS line is the child's, not the test's.
	_, _ = os.Stdout.Write(bytes.TrimSuffix(output, []byte("PASS\n"))) // Best effort: stdout has no fallback channel.
	return true
}

// setupUnprivilegedDir copies the running test binary into a directory the unprivileged user can traverse, and gives that user a writable HOME, TMPDIR, and result file.
func setupUnprivilegedDir(dir, binary, result, home, tmp string, uid, gid int) error {
	if err := os.Chmod(dir, 0o755); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if err := copyExecutable(executable, binary); err != nil {
		return err
	}
	for _, path := range []string{home, tmp} {
		if err := os.Mkdir(path, 0o700); err != nil {
			return err
		}
	}
	if err := os.WriteFile(result, nil, 0o600); err != nil {
		return err
	}
	for _, path := range []string{home, tmp, result} {
		if err := os.Chown(path, uid, gid); err != nil {
			return err
		}
	}
	return nil
}

func copyExecutable(from, to string) (err error) {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, source.Close()) }()
	target, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(target, source)
	return errors.Join(copyErr, target.Close())
}
