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

// refusesReplace reports whether a Windows replacement error is an access or sharing refusal (ERROR_ACCESS_DENIED, ERROR_SHARING_VIOLATION, or
// ERROR_LOCK_VIOLATION). Two roads reach it from replace(): (1) the pre-move descriptor carry from the existing file onto the temporary file failed
// with an access or sharing refusal (which returns immediately, matching the first carry's in-place fallback), or (2) MoveFileExW remained refused
// because another process kept the file open without delete sharing for the whole fsretry.RenameBudget. In either case WriteFile falls back to an
// in-place write (reporting the refusal via SetInPlaceReporter and removing the temporary file), as Pi writes it; non-busy errors stay failures.
func refusesReplace(err error) bool {
	return fsretry.TransientRename(err)
}
