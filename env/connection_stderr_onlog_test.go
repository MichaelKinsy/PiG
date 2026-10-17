package env

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// connection.ts:284 forwards every chunk the daemon process writes to stderr to `onLog` as text, and the connection keeps working when no onLog is
// set (`this.#options.onLog?.(...)`). The scripted daemon writes a diagnostic to stderr before it answers the hello.
// Pi: packages/env/src/connection.ts:284
func TestConnectionForwardsDaemonStderrToOnLogAndToleratesNone(t *testing.T) {
	script := `printf 'daemon diagnostic\n' >&2; ` + syncLine + helloResult + `exec sleep 30`
	var mu sync.Mutex
	var logged strings.Builder
	withLog := scriptedConnection(t, script, func(text string) {
		mu.Lock()
		defer mu.Unlock()
		logged.WriteString(text)
	})
	if _, err := withLog.Info(background); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		got := logged.String()
		mu.Unlock()
		if got == "daemon diagnostic\n" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("onLog received %q, want %q", got, "daemon diagnostic\n")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// No onLog: the same stderr output must not stop the connection from answering (a nil onLog is skipped, not called).
	silent := scriptedConnection(t, script, nil)
	if _, err := silent.Info(background); err != nil {
		t.Fatalf("connection without onLog: %v", err)
	}
}
