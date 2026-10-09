//go:build linux

package experimental

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/tui"
)

func openExperimentalPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open /dev/ptmx: %v", err)
	}
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 30, Col: 100}); err != nil {
		t.Fatal(err)
	}
	return master, slave
}

func writeExperimentalTheme(t *testing.T, directory, name, dim string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "tui", "theme_dark.json"))
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	value["name"] = name
	value["colors"].(map[string]any)["dim"] = dim
	data, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func waitExperimentalThemeDim(t *testing.T, name, dim string) {
	t.Helper()
	if !reloadedExperimentalThemeDimNamed(name, dim, 30*time.Second) {
		active := tui.ActiveTheme()
		t.Fatalf("active theme = %s dim %s, want %s dim %s", active.Name, active.GetResolvedThemeColors()["dim"], name, dim)
	}
}

func reloadedExperimentalThemeDim(dim string, within time.Duration) bool {
	return reloadedExperimentalThemeDimNamed("custom-test", dim, within)
}

func reloadedExperimentalThemeDimNamed(name, dim string, within time.Duration) bool {
	deadline := time.NewTimer(within)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if active := tui.ActiveTheme(); active.Name == name && active.GetResolvedThemeColors()["dim"] == dim {
			return true
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			return false
		}
	}
}

// upstream: packages/coding-agent/src/experimental/client-tui.ts:731-741,790. RunClientTui registers the resolved theme resources, the theme controller selects the saved custom theme with its file watcher enabled, and one cancellation joins the terminal, executor, controller and watcher. The renderer draws on the terminal the runner reads from (createInteractiveTui builds one ProcessTerminal over the current stdout), so the pty receives the alternate screen.
func TestRunClientTuiOwnsTheCustomThemeWatcherOnARealTerminal(t *testing.T) {
	agentDir := setupExperimentalRemoteTest(t)
	_ = writeExperimentalTheme(t, filepath.Join(agentDir, "themes"), "custom-test", "#112233")
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"theme":"custom-test"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, running := makeExperimentalServer(t)
	master, slave := openExperimentalPTY(t)
	screen := &outputBuffer{}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		_, _ = io.Copy(screen, master)
	}()
	stdin, stdout := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = slave, slave
	restored := false
	restore := func() {
		if !restored {
			os.Stdin, os.Stdout = stdin, stdout
			restored = true
		}
	}
	t.Cleanup(func() {
		restore()
		_ = slave.Close()
		_ = master.Close()
		<-drained
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	// RunClientTui reads os.Stdin and os.Stdout, so no failure path may restore them before it has returned.
	returned := false
	t.Cleanup(func() {
		if !returned {
			cancel()
			<-finished
		}
	})
	go func() {
		finished <- RunClientTui(ctx, ClientCommand{
			Command: "client",
			Connect: &TransportAddress{Transport: "unix", Path: running.SocketPath},
		}, RunClientTuiOptions{})
	}()
	// Startup ends with the controller's second watcher selection (ApplyFromSettings) and that step has no observable completion signal. An edit written before it lands between two native watchers, so each attempt writes a distinct color and the case passes only when a reload publishes one of them. A watcher that RunClientTui never started reloads none and fails at the deadline.
	waitExperimentalThemeDim(t, "custom-test", "#112233")
	waitUntil(t, "the alternate screen on the pty", func() bool { return screen.contains("\x1b[?1049h") })
	deadline := time.After(30 * time.Second)
	for attempt := 0; ; attempt++ {
		dim := fmt.Sprintf("#4455%02x", attempt)
		writeExperimentalTheme(t, filepath.Join(agentDir, "themes"), "custom-test", dim)
		if reloadedExperimentalThemeDim(dim, 300*time.Millisecond) {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("custom theme edits were never reloaded; active dim %s", tui.ActiveTheme().GetResolvedThemeColors()["dim"])
		default:
		}
	}
	cancel()
	select {
	case err := <-finished:
		returned = true
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RunClientTui = %v, want cancellation only", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("RunClientTui did not join its shutdown")
	}
	restore()
}
