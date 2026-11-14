//go:build linux

package cli

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// customModelFooterControl matches the control sequences of a painted frame, so the footer row reads as the text a terminal shows.
var customModelFooterControl = regexp.MustCompile("\x1b\\[[0-9;:?<>=]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|\x1b[78=>]")

// customModelFooterRow is the end of the footer's stats row of the model nosuch: the context window after the share of it in use, the
// auto-compaction mark, the padding, then the model, with its provider in front when more than one provider is available.
var customModelFooterRow = regexp.MustCompile(`%/([0-9.]+[kM]?)(?: \(auto\))? +(?:\(configured\) )?nosuch`)

// TestInteractiveFooterOfACustomModelIDShowsTheProvidersWindow is the interactive caller of TestStartupCustomModelIDIsACopyOfTheProvidersModel: pig
// started on a terminal with `--model <models.json provider>/<unknown id>` paints the footer of the copy of the provider's model. Pi 1.5.2, started
// the same way on a models.json provider whose model has a 48000 window, paints `0.0%/48k`; a session model with no window painted `0.0%/0`.
func TestInteractiveFooterOfACustomModelIDShowsTheProvidersWindow(t *testing.T) {
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	home := shortTempDir(t)
	agentDir := filepath.Join(home, "agent")
	seedFirstRunDone(t, agentDir)
	config := `{"providers":{"configured":{"baseUrl":"http://127.0.0.1:1/v1","apiKey":"unused","api":"openai-completions","models":[` +
		`{"id":"configured-1","name":"Configured One","input":["text"],"contextWindow":48000,"maxTokens":3000}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	master, slave := openPTY(t, 40, 140)
	t.Cleanup(func() { _ = master.Close() })
	cmd := exec.Command(binary, "--no-session", "--model", "configured/nosuch")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+home, "PI_HOME="+home, "PIG_CODING_AGENT_DIR="+agentDir, "PI_CODING_AGENT_DIR="+agentDir,
		"PIG_OFFLINE=1", "TERM=xterm-256color")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pig: %v", err)
	}
	_ = slave.Close()
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})
	output := &ptyOutput{}
	go func() { _, _ = io.Copy(output, master) }()

	screen := func() []byte { return customModelFooterControl.ReplaceAll(output.since(0), nil) }
	deadline := time.Now().Add(testbudget.Wait(t))
	for {
		if row := customModelFooterRow.FindSubmatch(screen()); row != nil {
			if got := string(row[1]); got != "48k" {
				t.Fatalf("footer of configured/nosuch shows the window %q, want \"48k\" (the 48000 window of the provider's model); row %q", got, row[0])
			}
			return
		}
		select {
		case <-exited:
			t.Fatalf("pig exited before it painted the footer; screen tail: %q", tail(screen(), 1500))
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("pig painted no footer row for the model nosuch; screen tail: %q", tail(screen(), 1500))
		}
		time.Sleep(50 * time.Millisecond)
	}
}
