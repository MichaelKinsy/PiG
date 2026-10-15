package env

// pi: packages/env/src/watch.ts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// scriptedWatchFrames is scriptedWatchDaemon with the frames given as they are, each a frame of request 2.
func scriptedWatchFrames(t *testing.T, target durableenv.WatchTarget, frames ...[]byte) *Connection {
	t.Helper()
	hello := encodeFrame(frameRequest, 1, Json{"protocol": 1, "op": "hello"}, nil)
	watch := encodeFrame(frameRequest, 2, Json{"targets": []any{Json{"path": target.Path, "recursive": false, "exclude": Json{"hidden": false, "names": []any{}}}}, "op": "watch"}, nil)
	var script strings.Builder
	script.WriteString(syncLine + helloResult)
	_, _ = fmt.Fprintf(&script, `head -c %d >/dev/null; `, len(hello)+len(watch))
	for _, frame := range frames {
		_, _ = fmt.Fprintf(&script, `printf '%s'; `, octal(frame))
	}
	script.WriteString(`exec sleep 30`)
	return scriptedConnection(t, script.String(), nil)
}

// watch.ts:104-125 an `error` event is delivered and then the watcher is closed (stop), so a later change is not delivered; a callback that throws does
// not stop the watching (deliver's try/catch), and a change after it is still delivered.
func TestRemoteWatcherStopsAfterAnErrorEventAndSurvivesAThrowingCallback(t *testing.T) {
	target := durableenv.WatchTarget{Path: "/watched"}
	change := func(path string) []byte {
		return encodeFrame(frameEvent, 2, Json{"kind": "change", "paths": []any{path}}, nil)
	}
	connection := scriptedWatchFrames(t, target,
		encodeFrame(frameEvent, 2, Json{"kind": "ready"}, nil),
		change("/watched/a"), change("/watched/b"),
		encodeFrame(frameEvent, 2, Json{"kind": "error", "code": "permission_denied", "message": "denied"}, nil),
		change("/watched/c"),
	)
	log := &changeLog{}
	var calls atomic.Int32
	watcher, err := OpenRemoteWatcher(background, connection, []durableenv.WatchTarget{target}, func(change durableenv.WatchChange) {
		log.add(change)
		if calls.Add(1) == 1 {
			panic("the callback throws")
		}
	}, RemoteWatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { connection.Close(); _ = watcher.Close(background) }()
	log.wait(t, 3)
	time.Sleep(200 * time.Millisecond)
	changes := log.wait(t, 3)
	if len(changes) != 3 {
		t.Fatalf("saw %d changes %v, want a, b and the error and nothing after it", len(changes), changes)
	}
	if _, ok := changes[2].(durableenv.WatchChangeError); !ok {
		t.Fatalf("third change %#v, want the error", changes[2])
	}
}

// watch.ts:126-140 a failure of the watch request after the daemon reported coverage is delivered as an error change (a lost connection is retried
// instead); one before coverage rejects open with that failure.
func TestRemoteWatcherReportsAFailedRequestBeforeAndAfterCoverage(t *testing.T) {
	target := durableenv.WatchTarget{Path: "/watched"}
	failure := encodeFrame(frameError, 2, Json{"code": "invalid", "message": "boom"}, nil)
	before := scriptedWatchFrames(t, target, failure)
	ctx, cancel := context.WithTimeout(background, 10*time.Second)
	defer cancel()
	if _, err := OpenRemoteWatcher(ctx, before, []durableenv.WatchTarget{target}, func(durableenv.WatchChange) {}, RemoteWatchOptions{}); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("open over a failing request = %v, want the request's failure", err)
	}
	after := scriptedWatchFrames(t, target, encodeFrame(frameEvent, 2, Json{"kind": "ready"}, nil), failure)
	log := &changeLog{}
	watcher, err := OpenRemoteWatcher(background, after, []durableenv.WatchTarget{target}, log.add, RemoteWatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = watcher.Close(background) }()
	changes := log.wait(t, 1)
	if reported, ok := changes[0].(durableenv.WatchChangeError); !ok || reported.Error == nil || !strings.Contains(reported.Error.Message, "boom") {
		t.Fatalf("first change %#v, want the failure as an error change", changes[0])
	}
}

// watch.ts:47-84 the watch request carries each target's path, recursive flag and exclude ({hidden: false, names: []} when none), and the poll interval,
// mode and directory budget the options give. A scripted daemon copies what it is sent into a file.
func TestRemoteWatcherSendsEachTargetsExcludeAndThePollInterval(t *testing.T) {
	captured := filepath.Join(t.TempDir(), "request")
	var script strings.Builder
	script.WriteString(syncLine + helloResult)
	_, _ = fmt.Fprintf(&script, `exec 3<&0; cat <&3 >%s & sleep 1; printf '%s'; exec sleep 30`, captured, octal(encodeFrame(frameEvent, 2, Json{"kind": "ready"}, nil)))
	connection := scriptedConnection(t, script.String(), nil)
	interval := 25
	var watcher *RemoteWatcher
	watcher, err := OpenRemoteWatcher(background, connection, []durableenv.WatchTarget{
		{Path: "/plain"},
		{Path: "/excluded", Recursive: true, Exclude: &durableenv.WatchExclude{Hidden: true, Names: []string{"node_modules", ".git"}}},
	}, func(durableenv.WatchChange) {}, RemoteWatchOptions{Mode: durableenv.WatchPolling, PollIntervalMs: &interval})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { connection.Close(); _ = watcher.Close(background) }()
	sent := string(must(os.ReadFile(captured)))
	for _, want := range []string{
		`{"exclude":{"hidden":false,"names":[]},"path":"/plain","recursive":false}`,
		`{"exclude":{"hidden":true,"names":["node_modules",".git"]},"path":"/excluded","recursive":true}`,
		`"pollIntervalMs":25`,
		`"mode":"polling"`,
	} {
		if !strings.Contains(sent, want) {
			t.Errorf("the watch request does not contain %s:\n%q", want, sent)
		}
	}
}

// watch.ts:62-68 close aborts the watch request and then `await this.#request`: when close returns, the request has finished, not merely been told to stop.
// mutation-checked: skipping the wait for the request in RemoteWatcher.Close fails it.
func TestRemoteWatcherCloseReturnsOnlyAfterTheWatchRequestHasFinished(t *testing.T) {
	env, _ := remoteEnvironment(t)
	for range 3 {
		watcher := must(env.Watch(background, []durableenv.WatchTarget{{Path: "."}}, func(durableenv.WatchChange) {})).(*RemoteWatcher)
		if err := watcher.Close(background); err != nil {
			t.Fatal(err)
		}
		watcher.mu.Lock()
		request := watcher.request
		watcher.mu.Unlock()
		select {
		case <-request:
		default:
			t.Fatal("Close returned while the watch request was still running")
		}
	}
}
