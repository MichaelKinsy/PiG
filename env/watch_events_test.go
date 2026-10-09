package env

// pi: packages/env/src/watch.ts

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// octal is data as a printf format of octal escapes.
func octal(data []byte) string {
	var builder strings.Builder
	for _, b := range data {
		_, _ = fmt.Fprintf(&builder, `\%03o`, b)
	}
	return builder.String()
}

// scriptedWatchDaemon answers the hello, waits until the watch request (request 2) has arrived, then sends events as
// event frames of request 2.
func scriptedWatchDaemon(t *testing.T, targets []durableenv.WatchTarget, events ...Json) *Connection {
	t.Helper()
	hello := encodeFrame(frameRequest, 1, Json{"protocol": 1, "op": "hello"}, nil)
	watch := encodeFrame(frameRequest, 2, Json{"targets": []any{Json{"path": targets[0].Path, "recursive": false, "exclude": Json{"hidden": false, "names": []any{}}}}, "op": "watch"}, nil)
	var script strings.Builder
	script.WriteString(syncLine + helloResult)
	_, _ = fmt.Fprintf(&script, `head -c %d >/dev/null; `, len(hello)+len(watch))
	for _, event := range events {
		_, _ = fmt.Fprintf(&script, `printf '%s'; `, octal(encodeFrame(frameEvent, 2, event, nil)))
	}
	script.WriteString(`exec sleep 30`)
	return scriptedConnection(t, script.String(), nil)
}

type changeLog struct {
	mu      sync.Mutex
	changes []durableenv.WatchChange
}

func (l *changeLog) add(change durableenv.WatchChange) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.changes = append(l.changes, change)
}

func (l *changeLog) wait(t *testing.T, count int) []durableenv.WatchChange {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		l.mu.Lock()
		changes := append([]durableenv.WatchChange(nil), l.changes...)
		l.mu.Unlock()
		if len(changes) >= count {
			return changes
		}
		if time.Now().After(deadline) {
			t.Fatalf("saw %v, want %d changes", changes, count)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// watch.ts open: `ready` sets the mode from its `mode` ("polling", anything else "native"); a `change` that says
// `polling` switches to polling.
// Pi source: packages/env/src/watch.ts
// mutation-checked: zeroing the results of RemoteWatcher.Mode fails it
// Pi: packages/env/src/watch.ts:8 (mode)
// watch.ts open: `ready` sets the mode from its `mode` ("polling", anything else "native"; packages/env/src/watch.ts:100); a
// `change` that says `polling` switches to polling (watch.ts:103).
func TestRemoteWatcherTakesItsModeFromTheDaemonsEvents(t *testing.T) {
	targets := []durableenv.WatchTarget{{Path: "/watched"}}
	for _, tc := range []struct {
		name   string
		events []Json
		want   durableenv.WatchMode
		paths  int
	}{
		{"ready polling", []Json{{"kind": "ready", "mode": "polling"}}, durableenv.WatchPolling, 0},
		{"ready native", []Json{{"kind": "ready", "mode": "native"}}, durableenv.WatchNative, 0},
		{"ready without a mode", []Json{{"kind": "ready"}}, durableenv.WatchNative, 0},
		{"a polling change", []Json{{"kind": "ready", "mode": "native"}, {"kind": "change", "mode": "polling", "paths": []any{"/watched"}}}, durableenv.WatchPolling, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connection := scriptedWatchDaemon(t, targets, tc.events...)
			log := &changeLog{}
			watcher, err := OpenRemoteWatcher(background, connection, targets, log.add, RemoteWatchOptions{})
			if err != nil {
				t.Fatal(err)
			}
			// The scripted daemon never answers the cancel of a watch; ending the connection settles the request.
			defer func() { connection.Close(); _ = watcher.Close(background) }()
			log.wait(t, tc.paths)
			if got := watcher.Mode(); got != tc.want {
				t.Fatalf("Mode() = %q, want %q", got, tc.want)
			}
		})
	}
}

// watch.ts open: an `error` event is delivered as FileError(code, String(event.message)) with code permission_denied
// or, for any other code, invalid.
func TestRemoteWatcherReportsTheDaemonsWatchErrors(t *testing.T) {
	targets := []durableenv.WatchTarget{{Path: "/watched"}}
	for _, tc := range []struct {
		name    string
		event   Json
		code    durableenv.FileErrorCode
		message string
	}{
		{"permission denied", Json{"kind": "error", "code": "permission_denied", "message": "denied"}, durableenv.FileErrorPermissionDenied, "denied"},
		{"another code", Json{"kind": "error", "code": "ELOOP", "message": "loop"}, durableenv.FileErrorInvalid, "loop"},
		{"no message", Json{"kind": "error", "code": "permission_denied"}, durableenv.FileErrorPermissionDenied, "undefined"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connection := scriptedWatchDaemon(t, targets, Json{"kind": "ready"}, tc.event)
			log := &changeLog{}
			watcher, err := OpenRemoteWatcher(background, connection, targets, log.add, RemoteWatchOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = watcher.Close(background) }()
			changes := log.wait(t, 1)
			failure, ok := changes[0].(durableenv.WatchChangeError)
			if !ok || failure.Error == nil || failure.Error.Code != tc.code || failure.Error.Message != tc.message {
				t.Fatalf("first change %#v, want a %s error %q", changes[0], tc.code, tc.message)
			}
		})
	}
}

// watch.ts open (packages/env/src/watch.ts:104): a `change` event with overflow === true is delivered as an overflow
// change, any other change (overflow absent, false, or not exactly true) as its paths.
// Pi source: packages/env/src/watch.ts
func TestRemoteWatcherDeliversAnOverflowChangeOnlyForOverflowTrue(t *testing.T) {
	targets := []durableenv.WatchTarget{{Path: "/watched"}}
	connection := scriptedWatchDaemon(t, targets,
		Json{"kind": "ready"},
		Json{"kind": "change", "overflow": true},
		Json{"kind": "change", "overflow": false, "paths": []any{"/watched/a"}},
		Json{"kind": "change", "paths": []any{"/watched/b"}},
	)
	log := &changeLog{}
	watcher, err := OpenRemoteWatcher(background, connection, targets, log.add, RemoteWatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { connection.Close(); _ = watcher.Close(background) }()
	changes := log.wait(t, 3)
	if _, ok := changes[0].(durableenv.WatchChangeOverflow); !ok {
		t.Fatalf("first change %#v, want overflow", changes[0])
	}
	for i, want := range []string{"/watched/a", "/watched/b"} {
		got, ok := changes[i+1].(durableenv.WatchChangePaths)
		if !ok || len(got.Paths) != 1 || got.Paths[0] != want {
			t.Fatalf("change %d = %#v, want paths [%s]", i+1, changes[i+1], want)
		}
	}
}
