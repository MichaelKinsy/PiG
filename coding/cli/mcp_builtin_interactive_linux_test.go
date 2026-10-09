//go:build linux

package cli

import (
	"io"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// driveInteractivePrompts runs cmd on a pseudo-terminal, types each prompt, and waits until the scripted model has
// answered requests more times than before, once per prompt's share of the script (requests is the total it must see).
func driveInteractivePrompts(t *testing.T, cmd *exec.Cmd, prompts []string, answered func() int, requests int, output *strings.Builder) {
	t.Helper()
	master, slave := openPTY(t, 50, 140)
	defer func() { _ = master.Close() }()
	defer func() { _ = slave.Close() }()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})
	screen := &ptyOutput{}
	copied := make(chan struct{})
	go func() {
		_, _ = io.Copy(screen, master)
		close(copied)
	}()
	t.Cleanup(func() {
		_ = master.Close()
		<-copied
	})
	// Wait for the editor to render before typing.
	screen.waitQuiet(0, []byte("Pi"), 500*time.Millisecond, testbudget.Wait(t))
	for i, prompt := range prompts {
		if _, err := master.Write([]byte(prompt)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(200 * time.Millisecond)
		if _, err := master.Write([]byte("\r")); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(testbudget.Wait(t))
		for want := requests; answered() < want && time.Now().Before(deadline); {
			time.Sleep(50 * time.Millisecond)
		}
		if answered() < requests {
			t.Fatalf("the model saw %d of %d requests after prompt %d\n%s", answered(), requests, i, screen.since(0))
		}
	}
	// Let the run finish and the screen settle.
	time.Sleep(500 * time.Millisecond)
	output.Write(screen.since(0))
}
