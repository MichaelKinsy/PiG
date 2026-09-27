package subprocess

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// An extension whose process exits with status 0 stops serving while its tools
// and commands stay registered. The host reports it instead of returning
// silently.
func TestCleanExtensionExitIsReported(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the clean-exit fixture: %v", err)
	}
	dir := t.TempDir()
	source := `export default function (pi) {
  pi.registerCommand("quit", { description: "exit cleanly", handler: async () => { setTimeout(() => process.exit(0), 10); } });
}
`
	if err := os.WriteFile(filepath.Join(dir, "index.mjs"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	host := NewHost(t.TempDir())
	defer host.Shutdown("test done")
	notices := make(chan string, 4)
	host.SetCrashHandler(func(name string, delay time.Duration, disabled bool, reason string) {
		notices <- FormatCrashNotice(name, delay, disabled, reason)
	})
	ext, err := host.Load(context.Background(), ExtConfig{Name: "clean-exit", Source: filepath.Join(dir, "index.mjs"), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	// The command's reply and the exit it schedules race in the extension
	// process: on Windows a pipe write still pending at process.exit is
	// dropped, so the command may end with the transport's EOF instead of its
	// reply. Either way the host must report the exit, which is what this
	// test checks.
	if err := ext.Commands["quit"].Handler(testbudget.Context(t), ""); err != nil && !strings.Contains(err.Error(), "read failed: EOF") {
		t.Fatal(err)
	}
	select {
	case notice := <-notices:
		if !strings.Contains(notice, `"clean-exit" disabled`) || !strings.Contains(notice, "status 0") {
			t.Fatalf("notice = %q", notice)
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("a clean extension exit was not reported")
	}
}
