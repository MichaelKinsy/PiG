package env

// packages/env/src/watch.ts:14-15 and :133-153: a lost watch connection is reopened after RECONNECT_FIRST_MS (1000), doubling after each lost attempt
// up to RECONNECT_MAX_MS (30000).

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// TestRemoteWatcherDoublesItsReconnectDelayAfterEachLostAttempt starts a scripted daemon that serves the first watch and exits once the test sends it
// one more frame, then fails every later start before its handshake, so each reconnect attempt is lost. reconnectTimer records each requested delay
// and fires at once, so the sequence is read without wall-clock bounds: 1s, 2s, 4s, 8s, 16s, then 30s from the cap on.
// mutation-checked: keeping the delay constant (`delay*2` to `delay` in env/watch.go reconnect), tripling it, halving reconnectFirst, or dropping the
// reconnectMax cap fails it.
func TestRemoteWatcherDoublesItsReconnectDelayAfterEachLostAttempt(t *testing.T) {
	delays := make(chan time.Duration, 16)
	previous := reconnectTimer
	reconnectTimer = func(delay time.Duration) *time.Timer {
		// The loop keeps reconnecting until Close; extra delays past the buffer are dropped so the loop never blocks on the test.
		select {
		case delays <- delay:
		default:
		}
		return time.NewTimer(0)
	}
	t.Cleanup(func() { reconnectTimer = previous })

	targets := []durableenv.WatchTarget{{Path: "/watched"}}
	logPath := filepath.Join(t.TempDir(), "launches")
	hello := encodeFrame(frameRequest, 1, Json{"protocol": 1, "op": "hello"}, nil)
	watch := encodeFrame(frameRequest, 2, Json{"targets": []any{Json{"path": "/watched", "recursive": false, "exclude": Json{"hidden": false, "names": []any{}}}}, "op": "watch"}, nil)
	var script strings.Builder
	_, _ = fmt.Fprintf(&script, `echo >> %q; [ "$(wc -l < %q)" -gt 1 ] && exit 1; `, logPath, logPath)
	script.WriteString(syncLine + helloResult)
	// The daemon exits only after the test has seen the watcher ready and sent the stop frame: a daemon that exits right behind its ready event races the
	// connection's reader, and an exit reaped before the event is dispatched loses the watch before it was ever established.
	stop := encodeFrame(frameRequest, 3, Json{"op": "stop"}, nil)
	_, _ = fmt.Fprintf(&script, `head -c %d >/dev/null; printf '%s'; head -c %d >/dev/null; exit 0`, len(hello)+len(watch), octal(encodeFrame(frameEvent, 2, Json{"kind": "ready", "mode": "native"}, nil)), len(stop))
	connection := scriptedConnection(t, script.String(), nil)

	watcher, err := OpenRemoteWatcher(background, connection, targets, func(durableenv.WatchChange) {}, RemoteWatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { connection.Close(); _ = watcher.Close(background) }()
	connection.Start(background, "stop", Json{}, RequestOptions{}) // the daemon exits on this frame, so the established watch is lost and reconnects

	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	var got []time.Duration
	for len(got) < len(want) {
		select {
		case delay := <-delays:
			got = append(got, delay)
		case <-t.Context().Done():
			t.Fatalf("reconnect delays = %v before the test ended, want %v", got, want)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("reconnect delays = %v, want %v", got, want)
	}
}
