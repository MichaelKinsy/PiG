//go:build linux

package cli

// pi: packages/coding-agent/src/core/provider-composer.ts

import (
	"bytes"
	"io"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Interactive mode shows the refusal as the turn's error ("Error: No API provider registered for api: nosuchapi" in Pi)
// and sends nothing.
func TestUnknownAPINameInteractiveShowsPiTextAndSendsNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary on a pseudo-terminal")
	}
	t.Parallel()
	bin := buildPigBinaryForSignalTest(t)
	baseURL, requests := unknownAPIListener(t)
	home := newUnknownAPIHome(t, unknownAPIModels(baseURL, "nosuchapi"), "", "")
	seedFirstRunDone(t, home.agent)
	master, slave := openPTY(t, 40, 140)
	t.Cleanup(func() { _ = master.Close() })
	cmd := home.command(t.Context(), bin, []string{"TERM=xterm-256color"}, "--no-session", "--no-approve", "--model", "odd/m")
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
	output.waitQuiet(0, []byte("128k"), 500*time.Millisecond, testbudget.Wait(t))
	if !bytes.Contains(output.since(0), []byte("128k")) {
		t.Fatalf("the footer never appeared; screen tail: %q", tail(output.since(0), 1500))
	}
	mark := output.mark()
	if _, err := master.WriteString("hello"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := master.WriteString("\r"); err != nil {
		t.Fatal(err)
	}
	want := []byte("No API provider registered for api: nosuchapi")
	output.waitQuiet(mark, []byte("api: nosuchapi"), 500*time.Millisecond, testbudget.Wait(t))
	screen := output.since(mark)
	if !bytes.Contains(screen, want) {
		t.Errorf("the screen lacks %q; tail: %q", want, tail(screen, 1500))
	}
	if bytes.Contains(screen, []byte("listener says no")) {
		t.Errorf("the screen shows the endpoint's answer; tail: %q", tail(screen, 1500))
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("requests at the endpoint = %d, want 0", got)
	}
}
