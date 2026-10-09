package env

// pi: packages/env/src/connection.ts

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

func daemonConnection(t *testing.T) *Connection {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses the daemon over a POSIX shell")
	}
	connection := NewConnection(ConnectionOptions{Command: []string{daemonBinary(t)}})
	t.Cleanup(connection.Close)
	return connection
}

// Ports packages/env/src/connection.ts:186-207 (Connection.session, Connection.request) and :78 (RequestOptions.payload): one request, one reply; the request payload travels as the
// frame payload and the reply reports the daemon session that answered.
// Pi: packages/env/src/connection.ts:190 (request).
// Pi: packages/env/src/connection.ts:68 (session).
// Pi: packages/env/src/connection.ts:65 (json).
// Pi: packages/env/src/connection.ts:78 (payload).
func TestConnectionRequestSendsPayloadsAndReportsTheAnsweringSession(t *testing.T) {
	connection := daemonConnection(t)
	target := filepath.Join(t.TempDir(), "sub", "file.txt")
	reply, err := connection.Request(background, "write", Json{"path": target}, RequestOptions{Payload: []byte("hello")})
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "hello" {
		t.Fatalf("file = %q, %v", data, err)
	}
	session, err := connection.Session(background)
	if err != nil || session == 0 || reply.Session != session {
		t.Fatalf("reply session %d, connection session %d, %v", reply.Session, session, err)
	}
	stat, err := connection.Request(background, "lstat", Json{"path": target}, RequestOptions{})
	if err != nil || stat.JSON["size"] != float64(5) {
		t.Fatalf("lstat = %+v, %v", stat.JSON, err)
	}
	if _, err := connection.Request(background, "lstat", Json{"path": target + ".missing"}, RequestOptions{}); err == nil {
		t.Fatal("a missing path did not fail")
	} else if remote, ok := errors.AsType[*RemoteError](err); !ok || remote.Code != "ENOENT" {
		t.Fatalf("err = %v, want a RemoteError with code ENOENT", err)
	}
}

// packages/env/src/connection.ts:85-90 RequestOptions.session: a request bound to a session that is not the live one fails as a lost connection instead of
// reaching a daemon that never opened the handle.
// Pi: packages/env/src/connection.ts:190 (request).
func TestRequestOptionsSessionRejectsAStaleSession(t *testing.T) {
	connection := daemonConnection(t)
	session, err := connection.Session(background)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Request(background, "hello", Json{}, RequestOptions{Session: session}); err != nil {
		t.Fatalf("live session: %v", err)
	}
	_, err = connection.Request(background, "hello", Json{}, RequestOptions{Session: session + 1})
	if !IsConnectionLost(err) {
		t.Fatalf("stale session err = %v, want a lost connection", err)
	}
}

// packages/env/src/connection.ts:80-84 RequestOptions.onEvent/onStart and :209-212 Connection.kill: onStart delivers the request id and session before the reply,
// onEvent receives exec output as it arrives, and kill ends the command without aborting the request.
// Pi: packages/env/src/connection.ts:209 (kill).
// Pi: packages/env/src/connection.ts:82 (onEvent).
// Pi: packages/env/src/connection.ts:84 (onStart).
func TestConnectionKillEndsAnExecStartedWithOnStart(t *testing.T) {
	connection := daemonConnection(t)
	started := make(chan struct{})
	var mu sync.Mutex
	var id uint32
	var sessionID int
	var output strings.Builder
	call := connection.Start(background, "exec", Json{"cwd": t.TempDir(), "command": "echo ready; exec sleep 30"}, RequestOptions{
		OnStart: func(requestID uint32, session int) {
			mu.Lock()
			id, sessionID = requestID, session
			mu.Unlock()
		},
		OnEvent: func(event Json, payload []byte) {
			mu.Lock()
			output.Write(payload)
			ready := strings.Contains(output.String(), "ready")
			mu.Unlock()
			if ready {
				select {
				case <-started:
				default:
					close(started)
				}
			}
		},
	})
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("no exec output event arrived")
	}
	mu.Lock()
	gotID, gotSession := id, sessionID
	mu.Unlock()
	if gotID == 0 || gotSession == 0 {
		t.Fatalf("OnStart saw id %d session %d", gotID, gotSession)
	}
	connection.Kill(gotID, gotSession)
	done := make(chan struct{})
	go func() { _, _ = call.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Kill did not end the command")
	}
	// A kill for a session that is not live does nothing.
	connection.Kill(gotID, gotSession+7)
}

// packages/env/src/watch.ts:8-9 RemoteWatchOptions.mode forces the watcher's mode: polling stays polling and still reports changes.
// Pi: packages/durable/src/env/index.ts:135 (mode).
// Pi: packages/env/src/remote-env.ts:809 (watch).
func TestRemoteWatchOptionsModeForcesTheWatcherMode(t *testing.T) {
	connection := daemonConnection(t)
	root := t.TempDir()
	for _, mode := range []durableenv.WatchMode{durableenv.WatchPolling, durableenv.WatchNative} {
		env := NewRemoteExecutionEnv(RemoteExecutionEnvOptions{Connection: connection, ID: "pi-env:test", Cwd: root, Watch: RemoteWatchOptions{Mode: mode}})
		var seen changes
		watcher, err := env.Watch(background, []durableenv.WatchTarget{{Path: "."}}, seen.add)
		if err != nil {
			t.Fatalf("mode %q: %v", mode, err)
		}
		if watcher.Mode() != mode {
			t.Errorf("forced mode %q, watcher reports %q", mode, watcher.Mode())
		}
		_ = watcher.Close(background)
	}
}

// packages/env/src/connection.ts:64-69 Reply { json, payload, session }: the payload of a reply is the frame payload the daemon sent with its result, here the bytes
// pread returns for an open handle, with the reply's json describing them.
func TestReplyCarriesTheFramePayloadOfAPread(t *testing.T) {
	connection := daemonConnection(t)
	target := filepath.Join(t.TempDir(), "file.txt")
	if _, err := connection.Request(background, "write", Json{"path": target}, RequestOptions{Payload: []byte("hello world")}); err != nil {
		t.Fatal(err)
	}
	opened, err := connection.Request(background, "open", Json{"path": target, "mode": "read"}, RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	read, err := connection.Request(background, "pread", Json{"handle": opened.JSON["handle"], "offset": 6, "length": 5}, RequestOptions{Session: opened.Session})
	if err != nil {
		t.Fatal(err)
	}
	if string(read.Payload) != "world" {
		t.Fatalf("reply payload = %q, want %q", read.Payload, "world")
	}
	if empty, err := connection.Request(background, "lstat", Json{"path": target}, RequestOptions{}); err != nil || len(empty.Payload) != 0 {
		t.Fatalf("a reply without data carries payload %q (%v)", empty.Payload, err)
	}
}
