package subprocess

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const nodePreflightHelperFIFOEnv = "PIG_TEST_NODE_PREFLIGHT_FIFO"

func TestMain(m *testing.M) {
	if fifoPath := os.Getenv(nodePreflightHelperFIFOEnv); fifoPath != "" {
		fifo, err := os.OpenFile(fifoPath, os.O_WRONLY, 0)
		if err != nil {
			fmt.Fprintln(os.Stderr, "open Node preflight test FIFO:", err)
			os.Exit(2)
		}
		if _, err := fmt.Fprintln(fifo, os.Getpid()); err != nil {
			fmt.Fprintln(os.Stderr, "signal Node preflight test parent:", err)
			os.Exit(2)
		}
		_ = fifo.Close()
		for {
			time.Sleep(time.Hour)
		}
	}
	testRoot, err := os.MkdirTemp("", "pig-subprocess-tests-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create isolated test home:", err)
		os.Exit(2)
	}
	for key, value := range map[string]string{
		"PIG_HOME": filepath.Join(testRoot, "pig"),
	} {
		if err := os.Setenv(key, value); err != nil {
			fmt.Fprintf(os.Stderr, "set isolated %s: %v\n", key, err)
			_ = os.RemoveAll(testRoot)
			os.Exit(2)
		}
	}
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		fmt.Fprintln(os.Stderr, "resolve source SDK roots:", err)
		_ = os.RemoveAll(testRoot)
		os.Exit(2)
	}
	for key, path := range map[string]string{
		"PIG_SDK_GO_ROOT": filepath.Join(root, "extensions", "sdk"),
		"PIG_SDK_PY_ROOT": filepath.Join(root, "extensions", "sdk-py"),
		"PIG_SDK_RS_ROOT": filepath.Join(root, "extensions", "sdk-rs"),
	} {
		if err := os.Setenv(key, path); err != nil {
			fmt.Fprintf(os.Stderr, "set %s: %v\n", key, err)
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
