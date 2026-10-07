//go:build linux

package testenv

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

const refuseHardLinksEnv = "PIG_TESTENV_REFUSE_HARD_LINKS"

// seccompArch is the audit architecture whose linkat the filter refuses; other architectures skip.
var seccompArch = map[string]uint32{"amd64": unix.AUDIT_ARCH_X86_64, "arm64": unix.AUDIT_ARCH_AARCH64}

// RunWithHardLinksRefused reruns the calling top-level test in a child process whose kernel refuses every hard link with EACCES, as Android's SELinux policy refuses them in Termux's data directory. It returns true in the child, where the test body runs, and false in the parent once the child passed. The seccomp filter cannot be removed, so it never applies to the parent test process.
func RunWithHardLinksRefused(t *testing.T) bool {
	t.Helper()
	if os.Getenv(refuseHardLinksEnv) == "1" {
		if err := refuseHardLinks(); err != nil {
			t.Fatalf("install the hard-link seccomp filter: %v", err)
		}
		probe := filepath.Join(t.TempDir(), "probe")
		if err := os.WriteFile(probe, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(probe, probe+".link"); !errors.Is(err, unix.EACCES) {
			t.Fatalf("os.Link under the seccomp filter = %v, want EACCES", err)
		}
		return true
	}
	if _, ok := seccompArch[runtime.GOARCH]; !ok {
		t.Skipf("no hard-link seccomp filter for %s", runtime.GOARCH)
	}
	if strings.Contains(t.Name(), "/") {
		t.Fatalf("RunWithHardLinksRefused reruns a top-level test; %s is a subtest", t.Name())
	}
	cmd := exec.Command(os.Args[0], "-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.v", "-test.count=1")
	cmd.Env = append(os.Environ(), refuseHardLinksEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--- PASS: "+t.Name()+" ") {
		t.Fatalf("%s with hard links refused: %v\n%s", t.Name(), err, out)
	}
	return false
}

// refuseHardLinks makes linkat fail with EACCES in every thread of the process and its children.
func refuseHardLinks() error {
	arch := seccompArch[runtime.GOARCH]
	filter := []unix.SockFilter{
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 4}, // seccomp_data.arch
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jt: 1, K: arch},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW},
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0}, // seccomp_data.nr
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jf: 1, K: unix.SYS_LINKAT},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.EACCES)},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW},
	}
	prog := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return err
	}
	if _, _, errno := unix.Syscall(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER, unix.SECCOMP_FILTER_FLAG_TSYNC, uintptr(unsafe.Pointer(&prog))); errno != 0 { //nolint:gosec // G103: seccomp(2) reads the caller's sock_fprog, which x/sys takes as uintptr; x/sys has no seccomp wrapper.
		return errno
	}
	runtime.KeepAlive(filter)
	return nil
}
