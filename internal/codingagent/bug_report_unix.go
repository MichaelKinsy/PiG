//go:build unix

package codingagent

import "golang.org/x/sys/unix"

// bugReportOSRelease returns the kernel release and version, as Node's
// os.release() and os.version() do.
func bugReportOSRelease() (release, version string) {
	var name unix.Utsname
	if err := unix.Uname(&name); err != nil {
		return "", ""
	}
	return unix.ByteSliceToString(name.Release[:]), unix.ByteSliceToString(name.Version[:])
}
