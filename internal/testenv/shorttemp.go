package testenv

import (
	"os"
	"runtime"
	"testing"
	"time"
)

// ShortTempDir creates a directory for the test whose path leaves room for
// Unix-domain socket names below it: sun_path holds 104 bytes on macOS and 108
// on Linux and Windows, and t.TempDir's name holds the test's name. It is in
// /tmp outside Windows, since macOS's TMPDIR is long, and in os.TempDir on
// Windows, with a name that starts with prefix. Cleanup removes it. On Windows
// the removal retries for two seconds while another process still holds a
// file, as t.TempDir's cleanup does.
func ShortTempDir(t testing.TB, prefix string) string {
	t.Helper()
	base := "/tmp"
	if runtime.GOOS == "windows" {
		base = os.TempDir()
	}
	dir, err := os.MkdirTemp(base, prefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		err := os.RemoveAll(dir)
		for deadline := time.Now().Add(2 * time.Second); err != nil && runtime.GOOS == "windows" && time.Now().Before(deadline); {
			time.Sleep(10 * time.Millisecond)
			err = os.RemoveAll(dir)
		}
		if err != nil {
			t.Error(err)
		}
	})
	return dir
}
