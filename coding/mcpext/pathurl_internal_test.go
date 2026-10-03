package mcpext

import "testing"

// runtime.ts sends the session cwd to servers as roots/list's
// pathToFileURL(cwd).href. Expected values are Node 24.19.0's
// url.pathToFileURL(path, { windows }).href for each case.
func TestPathToFileURLMatchesNode(t *testing.T) {
	for _, tc := range []struct {
		path    string
		windows bool
		want    string
	}{
		{"/a b/c(1)!*~", false, "file:///a%20b/c(1)!*%7E"},
		{"/p%q#r?s/", false, "file:///p%25q%23r%3Fs/"},
		{"/x[1]/{y}^|`\\", false, "file:///x%5B1%5D/%7By%7D%5E%7C%60%5C"},
		{"/é/ü\t", false, "file:///%C3%A9/%C3%BC%09"},
		{"/a/./b/../c", false, "file:///a/c"},
		{"/", false, "file:///"},
		{`C:\a b\c~`, true, "file:///C:/a%20b/c%7E"},
		{`C:\x\`, true, "file:///C:/x/"},
		{`\\SERVER\share\d(1)`, true, "file://server/share/d(1)"},
		{"C:/mixed/sl#", true, "file:///C:/mixed/sl%23"},
		{`D:\é[1]`, true, "file:///D:/%C3%A9%5B1%5D"},
		{`\\?\UNC\srv\sh\f`, true, "file://srv/sh/f"},
	} {
		if got := pathToFileURLFor(tc.path, tc.windows); got != tc.want {
			t.Errorf("pathToFileURL(%q, windows=%v) = %q, want %q", tc.path, tc.windows, got, tc.want)
		}
	}
}
