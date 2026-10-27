package sdk

import (
	"errors"
	"net"
	"runtime"
	"testing"
	"time"
)

// An Extension holds one connection, run context, tool lock and event bus, so a second serve begun while the first runs overwrote the first's state and left
// the second's host waiting for a register no one sent: it hung (BenchmarkLoadInProcessProviders reused one Extension across hosts). The second serve is refused
// at once, and the first is untouched; once the first ends, the Extension serves again.
func TestExtensionRefusesASecondConcurrentServe(t *testing.T) {
	ext := New("serve-overlap")
	firstHost, firstExt := net.Pipe()
	first := make(chan error, 1)
	go func() { first <- ext.RunWithConn(firstExt) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		ext.eventMu.Lock()
		running := ext.running
		ext.eventMu.Unlock()
		if running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the first serve never started")
		}
		runtime.Gosched()
	}

	secondHost, secondExt := net.Pipe()
	defer func() { _ = secondHost.Close() }()
	if err := ext.RunWithConn(secondExt); !errors.Is(err, errExtensionAlreadyServing) {
		t.Fatalf("second concurrent RunWithConn = %v, want errExtensionAlreadyServing", err)
	}
	select {
	case err := <-first:
		t.Fatalf("the first serve ended when a second was refused: %v", err)
	default:
	}

	_ = firstHost.Close()
	select {
	case <-first:
	case <-time.After(10 * time.Second):
		t.Fatal("the first serve did not end when its host closed")
	}
	thirdHost, thirdExt := net.Pipe()
	again := make(chan error, 1)
	go func() { again <- ext.RunWithConn(thirdExt) }()
	_ = thirdHost.Close()
	select {
	case err := <-again:
		if errors.Is(err, errExtensionAlreadyServing) {
			t.Fatal("a serve after the first ended was refused")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the serve after the first ended did not end when its host closed")
	}
}
