//go:build linux

package nativeplatform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

func TestUpstreamStalledX11ConnectionSetup(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/native-clipboard-linux.test.ts:129, both getText/getImage table rows.
	for _, method := range []string{"getText", "getImage"} {
		t.Run(method, func(t *testing.T) {
			server, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = server.Close() }()
			address := server.Addr().(*net.TCPAddr)
			if address.Port <= 6000 {
				t.Fatal("test server port does not encode an X11 display")
			}
			var connections atomic.Int32
			closed := make(chan struct{})
			go func() {
				defer close(closed)
				connection, err := server.Accept()
				if err != nil {
					return
				}
				connections.Add(1)
				timer := time.AfterFunc(300*time.Millisecond, func() { _ = connection.Close() })
				_, _ = io.Copy(io.Discard, connection)
				timer.Stop()
				_ = connection.Close()
			}()
			ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeX11ReaderFixture$")
			command.Env = append(os.Environ(), "PIG_NATIVE_X11_READER="+method, "DISPLAY=127.0.0.1:"+strconv.Itoa(address.Port-6000))
			start := time.Now()
			out, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("reader=%v %s", err, out)
			}
			if time.Since(start) >= 3500*time.Millisecond {
				t.Fatal("worker did not return after connection closure")
			}
			if string(out) != "{\"ok\":true,\"unavailable\":true}\nPASS\n" {
				t.Fatalf("reader result=%q", out)
			}
			<-closed
			if connections.Load() == 0 {
				t.Fatal("native reader did not reach the stalled server")
			}
		})
	}
}

func TestNativeX11ReaderFixture(t *testing.T) {
	method := os.Getenv("PIG_NATIVE_X11_READER")
	if method == "" {
		return
	}
	var ticks atomic.Int32
	ctx, cancel := context.WithCancel(t.Context())
	ticked := make(chan struct{})
	go func() {
		defer close(ticked)
		timer := time.NewTicker(10 * time.Millisecond)
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				ticks.Add(1)
			case <-ctx.Done():
				return
			}
		}
	}()
	start := time.Now()
	var bytes []byte
	var text *string
	var available bool
	var readErr error
	if helper := GetNativeClipboard(); helper != nil {
		if method == "getImage" {
			bytes, available, readErr = helper.GetImage(t.Context())
		} else {
			text, available, readErr = helper.GetText(t.Context())
			if text != nil {
				bytes = jsstring.ToUTF8(*text)
			}
		}
	}
	cancel()
	<-ticked
	if time.Since(start) > 200*time.Millisecond && ticks.Load() <= 5 {
		t.Fatal("native read blocked scheduler responsiveness")
	}
	children, err := os.ReadFile(fmt.Sprintf("/proc/self/task/%d/children", os.Getpid()))
	if err != nil || strings.TrimSpace(string(children)) != "" {
		t.Fatalf("child ownership=%q err=%v", children, err)
	}
	response := map[string]any{"ok": readErr == nil}
	switch {
	case readErr != nil:
		response["error"] = readErr.Error()
	case !available:
		response["unavailable"] = true
	case bytes == nil:
		response["value"] = nil
	default:
		if text != nil && len(jsstring.ToUTF16(*text)) <= 100 {
			response["value"] = *text
		}
		response["length"] = len(bytes)
		hash := sha256.Sum256(bytes)
		response["hash"] = hex.EncodeToString(hash[:])
	}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(string(data))
}

func TestClipboardShutdownCancelsStalledNativeConnection(t *testing.T) {
	server, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()
	port := server.Addr().(*net.TCPAddr).Port
	t.Setenv("DISPLAY", "127.0.0.1:"+strconv.Itoa(port-6000))
	accepted := make(chan net.Conn, 1)
	go func() {
		connection, err := server.Accept()
		if err == nil {
			accepted <- connection
		}
	}()
	waiterCtx, cancelWaiter := context.WithCancel(t.Context())
	defer cancelWaiter()
	returned := make(chan struct{})
	go func() { _, _, _ = GetNativeClipboard().GetImage(waiterCtx); close(returned) }()
	var connection net.Conn
	select {
	case connection = <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("native reader did not connect")
	}
	defer func() { _ = connection.Close() }()
	cancelWaiter()
	<-returned
	linuxClipboardWorker.mu.Lock()
	task := linuxClipboardWorker.task
	linuxClipboardWorker.mu.Unlock()
	if task == nil {
		t.Fatal("private setup operation disappeared before cancellation")
	}
	ShutdownClipboard()
	<-task.done
	linuxClipboardWorker.mu.Lock()
	busy := linuxClipboardWorker.task != nil
	linuxClipboardWorker.mu.Unlock()
	if busy {
		t.Fatal("cancelled private setup retained its worker slot")
	}
}
