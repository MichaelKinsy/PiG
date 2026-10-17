//go:build linux && !race

package ai

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The race detector's build reads sockets through syscall.Read (socket_read_race_linux.go), which wakes the monitor thread by design.

// socketWriterEnv marks the helper process that serves paced chunks on the listener it inherits as file descriptor 3.
const socketWriterEnv = "PIG_TEST_SOCKET_WRITER"

const socketWriterChunks = 150

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
			_, _ = io.WriteString(w, "data: {\"delta\":\"chunk\"}\n\n")
			w.(http.Flusher).Flush()
		}
	})}
	_ = server.Serve(listener)
}

// voluntarySwitches sums voluntary context switches over the process's threads.
func voluntarySwitches(t *testing.T) int {
	t.Helper()
	paths, err := filepath.Glob("/proc/self/task/*/status")
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			continue // the thread exited
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			if value, ok := strings.CutPrefix(scanner.Text(), "voluntary_ctxt_switches:"); ok {
				count, _ := strconv.Atoi(strings.TrimSpace(value))
				total += count
			}
		}
		_ = file.Close()
	}
	return total
}

// switchesPerChunk streams the helper's paced response through client and returns the process's voluntary context switches per body read that returned data.
func switchesPerChunk(t *testing.T, client *http.Client, url string) float64 {
	t.Helper()
	response, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	buffer := make([]byte, 4096)
	before := voluntarySwitches(t)
	chunks := 0
	for {
		n, err := response.Body.Read(buffer)
		if n > 0 {
			chunks++
		}
		if err != nil {
			break
		}
	}
	after := voluntarySwitches(t)
	if chunks < socketWriterChunks/2 {
		t.Fatalf("read %d chunks, want about %d", chunks, socketWriterChunks)
	}
	return float64(after-before) / float64(chunks)
}

// A socket read that enters a blocking system call while the process is idle wakes the runtime's monitor thread, which then sleeps and wakes every 20 µs until the process is idle again: several context switches per streamed chunk. The provider transport's raw socket read leaves the monitor asleep.
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
	standard := switchesPerChunk(t, &http.Client{Transport: &http.Transport{}}, url)
	raw := switchesPerChunk(t, streamingHTTPClient(), url)
	t.Logf("voluntary context switches per chunk: net/http default reads %.2f, provider transport %.2f", standard, raw)
	if raw > standard/2 {
		t.Fatalf("provider transport cost %.2f context switches per chunk, default net/http reads %.2f; want at most half", raw, standard)
	}
}
