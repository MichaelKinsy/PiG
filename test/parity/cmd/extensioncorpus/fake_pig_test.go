package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// fakePigOutputEnv makes a copy of this test binary a pig that prints the
// variable's value as its validation output and exits 1.
const fakePigOutputEnv = "PIG_TEST_FAKE_PIG_OUTPUT"

func TestMain(m *testing.M) {
	if output := os.Getenv(fakePigOutputEnv); output != "" {
		fmt.Println(output)
		os.Exit(1)
	}
	os.Exit(m.Run())
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
