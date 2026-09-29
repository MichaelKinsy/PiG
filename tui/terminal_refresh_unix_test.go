//go:build unix

package tui

import (
	"context"
	"sync"
	"testing"
	"time"
)

// packages/tui/src/terminal.ts:191-192 refreshes dimensions after installing resize handling.
func TestTerminalRefreshStartupSignalReachesResizeWatcher(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	resized := make(chan struct{})
	var once sync.Once
	stop := (&ProcessTerminal{}).startResizeWatcher(ctx, func() { once.Do(func() { close(resized) }) })
	defer stop()
	select {
	case <-resized:
	case <-time.After(5 * time.Second):
		t.Fatal("startup SIGWINCH did not reach the production resize watcher")
	}
}
