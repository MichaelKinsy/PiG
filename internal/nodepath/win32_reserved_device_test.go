package nodepath

import "testing"

// A device path whose name before the colon is a reserved device (\\?\COM1:, \\.\con:) keeps that name as the device and normalizes only what follows it (Node 24.19 path.win32.normalize; results from Node v24.19.0).
func TestWin32NormalizeKeepsAReservedDeviceOfADevicePath(t *testing.T) {
	for _, c := range []struct{ path, want string }{
		{`\\?\COM1:`, `\\?\COM1:\`},
		{`\\?\COM1:\a\..\b`, `\\?\COM1:\b`},
		{`\\.\con:x`, `\\?\con:\x`},
		{`\\?\lpt²:foo\bar`, `\\?\lpt²:\foo\bar`},
		{`\\?\NUL:`, `\\?\NUL:\`},
		{`\\?\COM10:x`, `\\?\COM10:x`},
		{`\\.\PHYSICALDRIVE0`, `\\.\PHYSICALDRIVE0`},
		{`\\?\C:\a\..\b`, `\\?\C:\b`},
	} {
		if got := Win32Normalize(c.path); got != c.want {
			t.Errorf("Win32Normalize(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}
