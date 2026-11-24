//go:build windows

package atomicfile

import (
	"os"

	"golang.org/x/sys/windows"

	"github.com/MichaelKinsy/PiG/internal/fsretry"
)

// linkCount is the number of directory entries that name the open file (NumberOfLinks of GetFileInformationByHandle); 1 where it cannot be read.
func linkCount(file *os.File, _ os.FileInfo) uint64 {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		return 1
	}
	return uint64(info.NumberOfLinks)
}

// refusesNewFile is false on Windows: an access error there is also what another process's open handle gives, which the retry of the replacement
// already waits out, and no setup that works with an in-place write is known to fail the temporary file (NOT RUN: see the lane report).
func refusesNewFile(error) bool { return false }

// refusesReplace reports whether a Windows MoveFileExW error that already exhausted fsretry.RenameBudget is a busy refusal (ERROR_ACCESS_DENIED,
// ERROR_SHARING_VIOLATION, or ERROR_LOCK_VIOLATION): another process kept the file open without delete sharing for the whole budget, so WriteFile
// falls back to an in-place write (with SetInPlaceReporter and the temp file removed), as Pi writes it. Non-busy errors stay failures.
func refusesReplace(err error) bool {
	return fsretry.TransientRename(err)
}
