package sdk

import (
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// scriptedQueueHost is the host side of the file mutation queue calls, over a pipe: it admits every fileMutation.enter at once with a fresh ticket (a path that
// contains "unresolvable" fails, as the host fails a path it cannot resolve), answers every fileMutation.exit, and records the calls it was made in order.
type scriptedQueueHost struct {
	mu       sync.Mutex
	calls    []queueCall
	stopConn func()
}

type queueCall struct {
	method string
	args   map[string]string
}

func (h *scriptedQueueHost) recorded() []queueCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]queueCall(nil), h.calls...)
}

// startScriptedQueueHost adopts a connection to a scripted host as the running extension's, so the SDK's helper calls use it.
func startScriptedQueueHost(t *testing.T) *scriptedQueueHost {
	t.Helper()
	t.Setenv("PIG_EXT_SOCKET", "")
	return adoptScriptedQueueHost(t, "")
}

// adoptScriptedQueueHost adopts a connection to a scripted host as a running extension's, serving socket. stopConn ends it as a stopping extension's does: the
// extension is forgotten and its transport closes.
func adoptScriptedQueueHost(t *testing.T, socket string) *scriptedQueueHost {
	t.Helper()
	clientNet, hostNet := net.Pipe()
	client := newConn(clientNet)
	client.start()
	adoptHelperConn(client, socket, nil)
	var once sync.Once
	stop := func() {
		once.Do(func() {
			releaseHelperConn(client)
			_ = clientNet.Close()
			_ = hostNet.Close()
		})
	}
	t.Cleanup(stop)
	h := &scriptedQueueHost{stopConn: stop}
	go func() {
		host := newConn(hostNet)
		var tickets int
		for {
			data, err := host.readFrame()
			if err != nil {
				return
			}
			var request envelope
			if json.Unmarshal(data, &request) != nil || request.Type != msgCall || request.Call == nil {
				return
			}
			args := map[string]string{}
			_ = json.Unmarshal(request.Call.Args, &args)
			h.mu.Lock()
			h.calls = append(h.calls, queueCall{request.Call.Method, args})
			h.mu.Unlock()
			result := &callResultMsg{Result: json.RawMessage(`{}`)}
			switch request.Call.Method {
			case callFileMutationEnter:
				if strings.Contains(args["path"], "unresolvable") {
					result = &callResultMsg{Error: &errorInfo{Message: "realpath " + args["path"] + ": too many levels of symbolic links"}}
				} else {
					tickets++
					result.Result = json.RawMessage(`{"ticket":"fm-` + string(rune('0'+tickets)) + `"}`)
				}
			case callFileMutationExit:
			default:
				result = &callResultMsg{Error: &errorInfo{Message: "unexpected " + request.Call.Method}}
			}
			response, err := json.Marshal(envelope{Type: msgCallResult, ID: request.ID, CallResult: result})
			if err != nil || host.writeFrame(response) != nil {
				return
			}
		}
	}()
	return h
}

// The task runs between the host's admission and the exit, with the path sent absolute and lexically clean; the SDK keeps no queue of its own.
func TestWithFileMutationQueueRunsTheTaskBetweenEnterAndExit(t *testing.T) {
	host := startScriptedQueueHost(t)
	dir := t.TempDir()
	var during []queueCall
	value, err := WithFileMutationQueue(filepath.Join(dir, "sub", "..", "a.txt"), func() (int, error) {
		during = host.recorded()
		return 7, nil
	})
	if err != nil || value != 7 {
		t.Fatalf("returned %d, %v", value, err)
	}
	if len(during) != 1 || during[0].method != callFileMutationEnter {
		t.Fatalf("calls inside the task = %+v, want only the enter", during)
	}
	calls := host.recorded()
	if len(calls) != 2 || calls[1].method != callFileMutationExit || calls[1].args["ticket"] != "fm-1" {
		t.Fatalf("calls = %+v, want enter then exit of ticket fm-1", calls)
	}
	if want := filepath.Join(dir, "a.txt"); calls[0].args["path"] != want {
		t.Fatalf("enter path = %q, want %q", calls[0].args["path"], want)
	}
}

// Two callers on one file are both sent to the host and neither waits for the other inside the SDK: the host alone orders them. A private SDK queue would hold the
// second caller until the first finished, and the first only finishes when the second has started.
func TestWithFileMutationQueueKeepsNoQueueOfItsOwn(t *testing.T) {
	host := startScriptedQueueHost(t)
	file := filepath.Join(t.TempDir(), "a.txt")
	firstEntered, secondEntered := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = WithFileMutationQueue(file, func() (struct{}, error) {
			close(firstEntered)
			select {
			case <-secondEntered:
			case <-time.After(10 * time.Second):
				t.Error("the second caller never ran: the SDK held it behind the first")
			}
			return struct{}{}, nil
		})
	})
	<-firstEntered
	wg.Go(func() {
		_, _ = WithFileMutationQueue(file, func() (struct{}, error) {
			close(secondEntered)
			return struct{}{}, nil
		})
	})
	wg.Wait()
	if calls := host.recorded(); len(calls) != 4 {
		t.Fatalf("calls = %+v, want two enters and two exits", calls)
	}
}

// A path the host cannot resolve fails the call without running the task and without an exit; a failing task and a panicking task still exit.
func TestWithFileMutationQueueHostErrorAndTaskFailures(t *testing.T) {
	host := startScriptedQueueHost(t)
	dir := t.TempDir()
	ran := false
	_, err := WithFileMutationQueue(filepath.Join(dir, "unresolvable"), func() (struct{}, error) {
		ran = true
		return struct{}{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "too many levels of symbolic links") || ran {
		t.Fatalf("unresolvable path: err = %v, ran = %v", err, ran)
	}
	if calls := host.recorded(); len(calls) != 1 {
		t.Fatalf("calls after a failed enter = %+v, want the enter only", calls)
	}
	want := errors.New("boom")
	if _, err := WithFileMutationQueue(filepath.Join(dir, "a.txt"), func() (int, error) { return 0, want }); !errors.Is(err, want) {
		t.Fatalf("task error = %v, want the task's", err)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("a panicking task's panic did not reach the caller")
			}
		}()
		_, _ = WithFileMutationQueue(filepath.Join(dir, "b.txt"), func() (int, error) { panic("boom") })
	}()
	exits := 0
	for _, call := range host.recorded() {
		if call.method == callFileMutationExit {
			exits++
		}
	}
	if exits != 2 {
		t.Fatalf("exits = %d, want one for the failing and one for the panicking task", exits)
	}
}

// A fused extension runs in the host's process: the installed hook is the queue, and no call is made.
func TestWithFileMutationQueueUsesTheFusedHostHook(t *testing.T) {
	var got string
	SetHostHelpers(&HostHelpers{WithFileMutationQueue: func(filePath string, fn func() error) error {
		got = filePath
		return fn()
	}})
	t.Cleanup(func() { SetHostHelpers(nil) })
	file := filepath.Join(t.TempDir(), "x", "..", "a.txt")
	value, err := WithFileMutationQueue(file, func() (string, error) { return "done", nil })
	if err != nil || value != "done" {
		t.Fatalf("returned %q, %v", value, err)
	}
	if want := filepath.Clean(file); got != want {
		t.Fatalf("hook path = %q, want the absolute clean path %q", got, want)
	}
	hookErr := errors.New("resolve failed")
	SetHostHelpers(&HostHelpers{WithFileMutationQueue: func(string, func() error) error { return hookErr }})
	ran := false
	if _, err := WithFileMutationQueue(file, func() (int, error) { ran = true; return 1, nil }); !errors.Is(err, hookErr) || ran {
		t.Fatalf("hook error: err = %v, ran = %v", err, ran)
	}
}

// A packed cell runs several extensions in one process, and the free function has no Context: its turn rides the first running extension's connection. When that
// extension stops while another member's task still holds the turn, the turn is the process's, not the stopped connection's: the exit goes to a connection that
// still runs, or the file stays held for as long as the process lives (the host releases a stopped connection's turns only with the process's last one).
func TestWithFileMutationQueueExitReachesTheHostWhenTheCarryingConnectionStopped(t *testing.T) {
	t.Setenv("PIG_EXT_SOCKET", "")
	carrier := adoptScriptedQueueHost(t, "/s/e-0.sock")
	peer := adoptScriptedQueueHost(t, "/s/e-1.sock")
	file := filepath.Join(t.TempDir(), "a.txt")
	inside, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := WithFileMutationQueue(file, func() (struct{}, error) {
			close(inside)
			<-release
			return struct{}{}, nil
		})
		done <- err
	}()
	<-inside
	if calls := carrier.recorded(); len(calls) != 1 || calls[0].method != callFileMutationEnter {
		t.Fatalf("the enter did not ride the first running connection: %+v", calls)
	}
	// The member whose connection carried the enter stops while the other member's task still holds the turn.
	carrier.stopConn()
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the task never returned")
	}
	var exits []queueCall
	for _, call := range peer.recorded() {
		if call.method == callFileMutationExit {
			exits = append(exits, call)
		}
	}
	if len(exits) != 1 || exits[0].args["ticket"] != "fm-1" {
		t.Fatalf("exits the still-running extension's connection received = %+v, want one for ticket fm-1", exits)
	}
}
