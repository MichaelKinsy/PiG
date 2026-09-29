//go:build unix

package codingagent

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

// TestSuspendSignalOrderChild is the Go endpoint for the real-signal differential probe. Only SIGTSTP is replaced; SIGINT/SIGCONT travel through os/signal and the production interrupt handler.
func TestSuspendSignalOrderChild(t *testing.T) {
	if os.Getenv("PIG_SUSPEND_SIGNAL_CHILD") != "1" {
		return
	}
	runSuspendSignalOrderChild(t)
	os.Exit(0)
}

func runSuspendSignalOrderChild(t *testing.T) {
	t.Helper()
	mode := &InteractiveMode{}
	signals := make(chan os.Signal, 2) // One slot for each signal kind exercised by this probe.
	signal.Notify(signals, syscall.SIGINT, syscall.SIGCONT)
	defer signal.Stop(signals)
	var onContinue func() error
	quit := make(chan struct{})
	go func() { _, _ = bufio.NewReader(os.Stdin).ReadString('\n'); close(quit) }()
	if err := suspendTerminal(context.Background(), "unix", nil, suspendOperations{
		keepAlive:       func(interval time.Duration) func() { return time.NewTicker(interval).Stop },
		ignoreInterrupt: mode.ignoreSuspendInterrupt,
		continued:       func(continued func() error, _ func()) func() { onContinue = continued; return func() {} },
		stop:            func() { fmt.Println("stopped") },
		start:           func() error { fmt.Println("resumed"); return nil },
		requestRender:   func() { fmt.Println("render:true") },
		kill:            func(int) error { fmt.Println("armed"); return nil },
	}); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case received := <-signals:
			if received == syscall.SIGINT {
				mode.handleInterruptSignal()
				fmt.Println("ignored")
			} else {
				if err := onContinue(); err != nil {
					t.Fatal(err)
				}
			}
		case <-quit:
			return
		}
	}
}

// Pi interactive-mode.ts:4289-4297 installs/removes a userspace listener. It does not set SIG_IGN: a pending SIGINT can terminate the process if delivered after the SIGCONT callback removes that listener.
func TestSuspendKeepsSignalDeliveryEnabled(t *testing.T) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT)
	defer signal.Stop(ch)
	mode := &InteractiveMode{}
	restore := mode.ignoreSuspendInterrupt()
	defer restore()
	if signal.Ignored(syscall.SIGINT) {
		t.Fatal("suspend used kernel SIG_IGN instead of the live listener state")
	}
	if !mode.suspended.Load() {
		t.Fatal("temporary listener inactive")
	}
	restore()
	if mode.suspended.Load() {
		t.Fatal("temporary listener retained after continue")
	}
}
