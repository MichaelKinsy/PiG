package nodeurl

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/nodepath"
)

// countCwdReads routes the working-directory reads of PathToFileURL through a counter for the duration of the test.
func countCwdReads(t *testing.T) *int {
	t.Helper()
	reads := 0
	saved := processEnv
	processEnv = func() nodepath.Env {
		reads++
		return nodepath.Process()
	}
	t.Cleanup(func() { processEnv = saved })
	return &reads
}

// TestPathToFileURLAbsolutePathReadsNoCwd: an absolute path resolves without process.cwd(), as Node's path.resolve does, so
// PathToFileURL makes no getcwd syscall for it (the tool cards link every path they render). The href is Node 24.19.0's
// url.pathToFileURL(path, { windows }).href, and resolving with the process state gives the same path the cwd-free resolve does.
func TestPathToFileURLAbsolutePathReadsNoCwd(t *testing.T) {
	reads := countCwdReads(t)
	for _, tc := range []struct {
		path    string
		windows bool
		want    string
	}{
		{"/a b/%#?\u00fc[x]^", false, "file:///a%20b/%25%23%3F%C3%BC%5Bx%5D%5E"},
		{"/x/../y/", false, "file:///y/"},
		{"/", false, "file:///"},
		{`C:\a b\%#`, true, "file:///C:/a%20b/%25%23"},
		{"c:/x/y/", true, "file:///c:/x/y/"},
		{`\\server\share\a b`, true, "file://server/share/a%20b"},
	} {
		if got := PathToFileURL(tc.path, tc.windows); got != tc.want {
			t.Errorf("PathToFileURL(%q, %v) = %q, want %q", tc.path, tc.windows, got, tc.want)
		}
		resolve := nodepath.PosixResolve
		if tc.windows {
			resolve = nodepath.Win32Resolve
		}
		withCwd, errCwd := resolve(nodepath.Process(), tc.path)
		without, err := resolve(nodepath.Env{}, tc.path)
		if withCwd != without || (err == nil) != (errCwd == nil) {
			t.Errorf("resolve(%q) = %q, %v without the process state; %q, %v with it", tc.path, without, err, withCwd, errCwd)
		}
	}
	if *reads != 0 {
		t.Fatalf("PathToFileURL read the working directory %d times for absolute paths, want 0", *reads)
	}
}

// TestPathToFileURLRelativePathReadsCwd: a path that is not absolute (relative, or `\x` and `C:x` on Windows, which take the drive or
// its directory from the working directory) still resolves against process.cwd().
func TestPathToFileURLRelativePathReadsCwd(t *testing.T) {
	reads := countCwdReads(t)
	for _, tc := range []struct {
		path    string
		windows bool
	}{{"a/b", false}, {`\x`, true}, {"C:x", true}, {"x", true}} {
		before := *reads
		got := PathToFileURL(tc.path, tc.windows)
		if *reads != before+1 {
			t.Errorf("PathToFileURL(%q, %v) read the working directory %d times, want 1", tc.path, tc.windows, *reads-before)
		}
		resolve := nodepath.PosixResolve
		if tc.windows {
			resolve = nodepath.Win32Resolve
		}
		resolved, err := resolve(nodepath.Process(), tc.path)
		if err != nil {
			t.Fatal(err)
		}
		if want := PathToFileURL(resolved, tc.windows); got != want {
			t.Errorf("PathToFileURL(%q, %v) = %q, want %q (the resolved path's URL)", tc.path, tc.windows, got, want)
		}
	}
}
