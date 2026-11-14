//go:build windows

package atomicfile

import (
	"os"

	"golang.org/x/sys/windows"
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

// refusesReplace is false on Windows: ReplaceFileW and MoveFileEx report a busy or protected target as ERROR_ACCESS_DENIED, ERROR_SHARING_VIOLATION
// or ERROR_UNABLE_TO_REMOVE_REPLACED, which fsretry retries for its budget and then returns (NOT RUN: see the lane report).
func refusesReplace(error) bool { return false }
