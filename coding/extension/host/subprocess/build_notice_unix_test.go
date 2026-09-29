//go:build linux

package subprocess

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A terminal stderr must not select interactive notices for print, JSON, or RPC.
func TestColdBuildNoticesRespectMode(t *testing.T) {
	if _, err := exec.LookPath("script"); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("PIG_TEST_BUILD_NOTICE_CHILD") == "1" {
		h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
		h.SetMode(os.Getenv("PIG_TEST_BUILD_NOTICE_MODE"))
		root := t.TempDir()
		writeOwnerFixture(t, root, "go.mod", "module example.com/notice\n\ngo 1.26\n")
		writeOwnerFixture(t, root, "main.go", "package main\nfunc main() {}\n")
		cfg := ExtConfig{Name: "notice", Source: root, Enabled: true}
		_, _ = h.stageCell(t.Context(), isolatedCell(cfg, "notice"), nil)
		h.Shutdown("test done")
		return
	}
	for _, mode := range []string{"tui", "print", "json", "rpc"} {
		t.Run(mode, func(t *testing.T) {
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), "script", "-q", "-e", "-c", exe+" -test.run '^TestColdBuildNoticesRespectMode$'", filepath.Join(t.TempDir(), "typescript"))
			cmd.Env = append(os.Environ(), "PIG_TEST_BUILD_NOTICE_CHILD=1", "PIG_TEST_BUILD_NOTICE_MODE="+mode)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("child: %v\n%s", err, output)
			}
			got := strings.Contains(string(output), "Building extension")
			if got != (mode == "tui") {
				t.Fatalf("mode %s notice=%v\n%s", mode, got, output)
			}
		})
	}
}
