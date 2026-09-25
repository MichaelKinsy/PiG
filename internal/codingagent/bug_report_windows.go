//go:build windows

package codingagent

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// bugReportOSRelease returns the Windows version as Node's os.release()
// reports it (major.minor.build) and an empty build description.
func bugReportOSRelease() (release, version string) {
	info := windows.RtlGetVersion()
	return fmt.Sprintf("%d.%d.%d", info.MajorVersion, info.MinorVersion, info.BuildNumber), ""
}
