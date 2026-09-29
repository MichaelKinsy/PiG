//go:build linux

package nativeplatform

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"
)

// xcb_connect accepts explicit tcp/ transport, in addition to the ordinary host:display and local :display spellings.
func TestX11DisplayTransportPrefixes(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  x11Display
	}{
		{":3.1", x11Display{number: "3", screen: 1, local: true}},
		{"127.0.0.1:2", x11Display{host: "127.0.0.1", number: "2"}},
		{"tcp/127.0.0.1:2", x11Display{host: "127.0.0.1", number: "2"}},
		{"[::1]:2", x11Display{host: "::1", number: "2"}},
		{"tcp/[::1]:2.3", x11Display{host: "::1", number: "2", screen: 3}},
		{"unix/:2", x11Display{host: "unix", number: "2", local: true}},
		{"host/unix:2", x11Display{host: "unix", number: "2", local: true}},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := parseX11Display(tc.input)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("display = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

func TestNativeClipboardProcessExitWithBlockedAuthority(t *testing.T) {
	server := startNativeTestProcess(t, "Xvfb", []string{"-displayfd", "1", "-screen", "0", "640x480x24", "-nolisten", "tcp"}, os.Environ())
	display := nativeReady(t, server)
	authority := filepath.Join(t.TempDir(), "authority.fifo")
	if err := syscall.Mkfifo(authority, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBlockedClipboardAuthorityProcessFixture$")
	cmd.Env = append(os.Environ(), "DISPLAY=:"+display, "XAUTHORITY="+authority, "PIG_BLOCKED_CLIPBOARD_AUTHORITY=1")
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "passed\nPASS\n" {
		t.Fatalf("natural exit after blocked authority: %v\n%s", err, out)
	}
}

func TestBlockedClipboardAuthorityProcessFixture(t *testing.T) {
	if os.Getenv("PIG_BLOCKED_CLIPBOARD_AUTHORITY") == "" {
		return
	}
	defer ShutdownClipboard()
	helper := GetNativeClipboard()
	if helper == nil {
		t.Fatal("native helper absent")
	}
	if _, available, err := helper.GetText(t.Context()); available || err != nil {
		t.Fatalf("blocked authority result: available=%v error=%v", available, err)
	}
	fmt.Println("passed")
}

func TestNativeX11ExplicitTCPTransport(t *testing.T) {
	server := startNativeTestProcess(t, "Xvfb", []string{"-displayfd", "1", "-screen", "0", "640x480x24", "-listen", "tcp", "-ac"}, os.Environ())
	display := nativeReady(t, server)
	local := append(os.Environ(), "DISPLAY=:"+display)
	writeXclipTest(t, local, "UTF8_STRING", []byte("café 日本語"))
	for _, prefix := range []string{":", "127.0.0.1:", "tcp/127.0.0.1:"} {
		env := append(os.Environ(), "DISPLAY="+prefix+display)
		got := readNativeTestClipboard(t, "getText", env)
		if !got.OK || string(got.Value) != `"café 日本語"` {
			t.Fatalf("DISPLAY=%s%s: %+v", prefix, display, got)
		}
	}
}
