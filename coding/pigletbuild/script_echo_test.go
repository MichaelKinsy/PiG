package pigletbuild

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// scriptEchoEnv makes a copy of this test binary stand in for pig: it prints
// its working directory and then each argument, one per line.
const scriptEchoEnv = "PIG_TEST_SCRIPT_ECHO"

func echoWorkingDirectoryAndArgs() int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "script echo:", err)
		return 2
	}
	fmt.Println(cwd)
	for _, arg := range os.Args[1:] {
		fmt.Println(arg)
	}
	return 0
}

// copyTestBinary copies the running test binary to path.
func copyTestBinary(t *testing.T, path string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(self)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}
