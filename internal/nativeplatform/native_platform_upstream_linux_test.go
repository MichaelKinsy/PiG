//go:build linux

package nativeplatform

import (
	"net"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
)

// .upstream/v0.87.1/packages/tui/test/native-platform.test.ts:83 (Linux loads X11 lazily and rechecks DISPLAY) through the real Linux helper: obtaining the helper never connects, each read connects to the current DISPLAY, and a removed DISPLAY hides the helper again while a returning DISPLAY yields the same cached object.
func TestUpstreamNativePlatformLinuxLazyConnectionAndDisplayRecheck(t *testing.T) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		if GetNativeClipboard() != nil {
			t.Fatal("helper exists on an unsupported architecture")
		}
		return
	}
	server, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()
	var connections atomic.Int32
	go func() {
		for {
			connection, err := server.Accept()
			if err != nil {
				return
			}
			connections.Add(1)
			_ = connection.Close() // A refused setup reports the display as unavailable.
		}
	}()
	port := server.Addr().(*net.TCPAddr).Port
	if port <= 6000 {
		t.Fatal("test server port does not encode an X11 display")
	}
	display := "127.0.0.1:" + strconv.Itoa(port-6000)

	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	if GetNativeClipboard() != nil {
		t.Fatal("Wayland-only session returned an X11 helper")
	}
	t.Setenv("DISPLAY", display)
	clipboard := GetNativeClipboard()
	if clipboard == nil {
		t.Fatal("no helper with DISPLAY set")
	}
	if connections.Load() != 0 {
		t.Fatalf("obtaining the helper made %d connections", connections.Load())
	}
	if value, ok, err := clipboard.GetText(t.Context()); value != nil || ok || err != nil {
		t.Fatalf("getText on a refusing display = %v, %v, %v", value, ok, err)
	}
	if connections.Load() == 0 {
		t.Fatal("getText did not connect lazily")
	}
	t.Setenv("DISPLAY", "")
	if GetNativeClipboard() != nil {
		t.Fatal("helper returned after DISPLAY was removed")
	}
	t.Setenv("DISPLAY", display)
	if GetNativeClipboard() != clipboard {
		t.Fatal("helper identity changed after DISPLAY returned")
	}
}
