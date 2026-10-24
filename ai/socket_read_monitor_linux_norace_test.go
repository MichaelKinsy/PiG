//go:build linux && !race

package ai

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime/trace"
	"testing"
	"time"

	xtrace "golang.org/x/exp/trace"
)

// The race detector's build reads sockets through syscall.Read (socket_read_race_linux.go), which wakes the monitor thread by design.

// socketWriterEnv marks the helper process that serves paced chunks on the listener it inherits as file descriptor 3.
const socketWriterEnv = "PIG_TEST_SOCKET_WRITER"

const socketWriterChunks = 150

// socketWriterEvent is each chunk the helper writes.
const socketWriterEvent = "data: {\"delta\":\"chunk\"}\n\n"

// TestSocketWriterHelper is the paced HTTP server process of TestStreamingTransportReadDoesNotWakeTheRuntimeMonitor, so no write system call of the measured process wakes the monitor thread.
func TestSocketWriterHelper(t *testing.T) {
	if os.Getenv(socketWriterEnv) == "" {
		t.Skip("helper process only")
	}
	listener, err := net.FileListener(os.NewFile(3, "listener"))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{ReadHeaderTimeout: time.Minute, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for range socketWriterChunks {
			time.Sleep(2 * time.Millisecond)
			_, _ = io.WriteString(w, socketWriterEvent)
			w.(http.Flusher).Flush()
		}
	})}
	_ = server.Serve(listener)
}

// streamSyscallEntries streams the helper's paced response through client and returns the system calls the process's goroutines entered while the body was read, by the site that made each call, and the body reads that returned data. The whole response must arrive: a read that lost or cut the stream would count fewer system calls for the wrong reason. How many chunks each read returns depends on how far the client lags the helper, so only the total is checked. The execution trace records one goroutine transition to the system-call state for each syscall.Syscall (every blocking call the runtime knows) and none for a raw system call, so the count is exact: it does not depend on how the host schedules the process, the helper or the load around them.
func streamSyscallEntries(t *testing.T, client *http.Client, url string) (entries int, sites map[string]int, dataReads int) {
	t.Helper()
	response, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var recorded bytes.Buffer
	if err := trace.Start(&recorded); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 4096)
	received := 0
	var readErr error
	for readErr == nil {
		var n int
		n, readErr = response.Body.Read(buffer)
		received += n
		if n > 0 {
			dataReads++
		}
	}
	trace.Stop()
	if want := socketWriterChunks * len(socketWriterEvent); !errors.Is(readErr, io.EOF) || received != want {
		t.Fatalf("read %d bytes ending in %v, want the whole %d-byte stream ending in EOF", received, readErr, want)
	}
	reader, err := xtrace.NewReader(&recorded)
	if err != nil {
		t.Fatal(err)
	}
	sites = map[string]int{}
	for {
		event, err := reader.ReadEvent()
		if errors.Is(err, io.EOF) {
			return entries, sites, dataReads
		}
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind() != xtrace.EventStateTransition {
			continue
		}
		transition := event.StateTransition()
		if transition.Resource.Kind != xtrace.ResourceGoroutine {
			continue
		}
		// A goroutine already in a system call when the trace starts is reported leaving an undetermined state; only entries count.
		if from, to := transition.Goroutine(); to == xtrace.GoSyscall && from != xtrace.GoUndetermined {
			entries++
			sites[stackSite(transition.Stack)]++
		}
	}
}

// A socket read that enters a blocking system call while the process is idle wakes the runtime's monitor thread, which then sleeps and wakes every 20 µs until the process is idle again: several context switches per streamed chunk. The runtime wakes the monitor only from a goroutine's system-call entry or exit (proc.go reentersyscall's entersyscallWakeSysmon, exitsyscallTryGetP and exitsyscallNoP) and when the world restarts (startTheWorldWithSema), so a read that enters no system call cannot wake it. The provider transport's raw socket read enters none, while net/http's default read enters one for each read(2); the trace counts both exactly, where the context switches the monitor costs depend on the host's scheduling and the load on it.
func TestStreamingTransportReadDoesNotWakeTheRuntimeMonitor(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	file, err := listener.(*net.TCPListener).File()
	_ = listener.Close()
	if err != nil {
		t.Fatal(err)
	}
	server := exec.Command(os.Args[0], "-test.run=^TestSocketWriterHelper$")
	server.Env = append(os.Environ(), socketWriterEnv+"=1")
	server.ExtraFiles = []*os.File{file}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	t.Cleanup(func() { _ = server.Process.Kill(); _ = server.Wait() })
	url := "http://" + listener.Addr().String() + "/"
	// The provider transport streams first and alone. The trace counts every goroutine of the process, and net/http's default transport leaves a readLoop goroutine on its keep-alive connection that enters a system call when it next reads or closes it (a read right after the body's EOF, a close at teardown); run first, that goroutine could enter its system call during the provider's window and be counted against it. The provider transport's own connection has only raw reads, which the trace does not record, so whatever it leaves behind adds nothing to the default reads' count.
	raw, sites, rawReads := streamSyscallEntries(t, streamingHTTPClient(), url)
	standard, _, standardReads := streamSyscallEntries(t, &http.Client{Transport: &http.Transport{}}, url)
	t.Logf("system calls entered while streaming: net/http default reads %d over %d returning reads, provider transport %d over %d returning reads", standard, standardReads, raw, rawReads)
	if standard == 0 {
		t.Fatal("net/http's default reads entered no system call: the trace is not recording them, so the provider transport's count proves nothing")
	}
	if raw != 0 {
		t.Fatalf("provider transport entered %d system calls while streaming %d chunks, want none (each can wake the runtime's monitor thread); by site:%s", raw, socketWriterChunks, wakeReasons(sites))
	}
}
