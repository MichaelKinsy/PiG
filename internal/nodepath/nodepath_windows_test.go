//go:build windows

package nodepath

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// setDriveCwd sets the process's per-drive working directory the way libuv's
// uv_chdir does: the "=<drive>:" environment variable that Node's
// path.win32.resolve reads and that GetFullPathName reads for a drive-relative
// path. Go's os.Setenv rejects a name that contains "=".
func setDriveCwd(t *testing.T, device, dir string) {
	t.Helper()
	name, err := windows.UTF16PtrFromString("=" + device)
	if err != nil {
		t.Fatal(err)
	}
	value, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	previous, had := os.LookupEnv("=" + device)
	if err := windows.SetEnvironmentVariable(name, value); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !had {
			_ = windows.SetEnvironmentVariable(name, nil)
			return
		}
		if restore, err := windows.UTF16PtrFromString(previous); err == nil {
			_ = windows.SetEnvironmentVariable(name, restore)
		}
	})
}

// TestResolveReadsThePerDriveWorkingDirectoryOfTheProcess runs the host Resolve
// against the real process state on Windows: C:relative and D:relative resolve
// on the "=C:" / "=D:" variables the process holds, and agree with the
// operating system's own GetFullPathName for names it does not rewrite.
func TestResolveReadsThePerDriveWorkingDirectoryOfTheProcess(t *testing.T) {
	process, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	processDrive := filepath.VolumeName(process)
	if len(processDrive) != 2 {
		t.Skipf("the process working directory %q is not on a drive", process)
	}
	other := "Z:"
	if processDrive == other {
		other = "Y:"
	}

	if got, err := Resolve(other + "rel"); err != nil || got != other+`\rel` {
		t.Errorf("Resolve(%q) without a per-drive directory = %q, %v; want %q", other+"rel", got, err, other+`\rel`)
	}

	setDriveCwd(t, other, other+`\perdrive\dir`)
	want := other + `\perdrive\dir\rel`
	got, err := Resolve(other + "rel")
	if err != nil || got != want {
		t.Errorf("Resolve(%q) with =%s set = %q, %v; want %q", other+"rel", other, got, err, want)
	}
	if full, err := syscall.FullPath(other + "rel"); err != nil || full != got {
		t.Errorf("GetFullPathName(%q) = %q, %v; Resolve gave %q", other+"rel", full, err, got)
	}
	if got, err := Resolve(other+"rel", "x"); err != nil || got != other+`\perdrive\dir\rel\x` {
		t.Errorf("Resolve(%q, x) = %q, %v", other+"rel", got, err)
	}
	if got, err := Resolve("base", other+"rel"); err != nil || got != other+`\perdrive\dir\base\rel` {
		t.Errorf("a plain relative base joins the drive-relative tail under the per-drive directory: %q, %v", got, err)
	}

	setDriveCwd(t, other, processDrive+`\elsewhere`)
	if got, err := Resolve(other + "rel"); err != nil || got != other+`\rel` {
		t.Errorf("a per-drive directory on another drive is ignored: %q, %v", got, err)
	}

	setDriveCwd(t, processDrive, process)
	relative := processDrive + "rel"
	wantProcess := filepath.Join(process, "rel")
	if got, err := Resolve(relative); err != nil || got != wantProcess {
		t.Errorf("Resolve(%q) on the process drive = %q, %v; want %q", relative, got, err, wantProcess)
	}
	if got, err := Resolve(`\rooted`); err != nil || got != processDrive+`\rooted` {
		t.Errorf("a rooted path resolves on the process drive: %q, %v", got, err)
	}
}
