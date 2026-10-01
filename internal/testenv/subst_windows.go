//go:build windows

package testenv

import (
	"context"
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

// SubstDrive maps a free drive letter to dir with subst for the rest of the
// test and returns the drive, such as "Z:". A mapping belongs to the logon
// session, so another process can take a free letter before subst does;
// SubstDrive then tries the next one, from Z: down to D:.
func SubstDrive(t testing.TB, dir string) string {
	t.Helper()
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		t.Fatal(err)
	}
	var failures []byte
	for letter := 'Z'; letter >= 'D'; letter-- {
		if mask&(1<<(letter-'A')) != 0 {
			continue
		}
		drive := string(letter) + ":"
		out, err := exec.CommandContext(t.Context(), "subst", drive, dir).CombinedOutput()
		if err != nil {
			failures = append(failures, "subst "+drive+": "+err.Error()+": "+string(out)+"\n"...)
			continue
		}
		t.Cleanup(func() {
			if out, err := exec.CommandContext(context.WithoutCancel(t.Context()), "subst", drive, "/d").CombinedOutput(); err != nil {
				t.Errorf("subst %s /d: %v\n%s", drive, err, out)
			}
		})
		return drive
	}
	t.Fatalf("no free drive letter for subst %s\n%s", dir, failures)
	return ""
}
