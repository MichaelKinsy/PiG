package extensionconformance

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	testRoot, err := os.MkdirTemp("", "pig-extension-conformance-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create isolated test home:", err)
		os.Exit(2)
	}
	sourceRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		fmt.Fprintln(os.Stderr, "resolve source SDK roots:", err)
		_ = os.RemoveAll(testRoot)
		os.Exit(2)
	}
	for key, value := range map[string]string{
		"PIG_HOME":        filepath.Join(testRoot, "pig"),
		"PIG_SDK_GO_ROOT": filepath.Join(sourceRoot, "extensions", "sdk"),
		"PIG_SDK_PY_ROOT": filepath.Join(sourceRoot, "extensions", "sdk-py"),
		"PIG_SDK_RS_ROOT": filepath.Join(sourceRoot, "extensions", "sdk-rs"),
	} {
		if err := os.Setenv(key, value); err != nil {
			fmt.Fprintf(os.Stderr, "set isolated %s: %v\n", key, err)
			_ = os.RemoveAll(testRoot)
			os.Exit(2)
		}
	}
	code := m.Run()
	if err := os.RemoveAll(testRoot); err != nil {
		fmt.Fprintln(os.Stderr, "remove isolated test home:", err)
		if code == 0 {
			code = 2
		}
	}
	os.Exit(code)
}
