//go:build linux

package nativeplatform

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi linux-platform-x11.c:185 delegates to xcb_connect, whose parser (libxcb 1.15 xcb_util.c:176 and :186) reads the display and screen with C strtoul
// into an int: leading white space and a sign are accepted, a minus negates modulo 2^64, an overflow saturates at ULONG_MAX, and the int keeps the low
// 32 bits. Local display numbers name Unix sockets; a network display's port is the low 16 bits of 6000 plus the number (xcb_util.c:244). The rows are
// what xcb_parse_display returns for each spelling on libxcb 1.15.
func TestX11DisplayNumbersParseAsXcbDoes(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  x11Display
	}{
		{":59535", x11Display{number: 59535, local: true}},
		{":60000", x11Display{number: 60000, local: true}},
		{"unix/:65536.2", x11Display{host: "unix", number: 65536, screen: 2, local: true}},
		{"localhost:59536", x11Display{host: "localhost", number: 59536}},
		{"tcp/localhost:60000", x11Display{host: "localhost", number: 60000}},
		{"[::1]:60000", x11Display{host: "::1", number: 60000}},
		{": 1", x11Display{number: 1, local: true}},
		{":\t1", x11Display{number: 1, local: true}},
		{":+1", x11Display{number: 1, local: true}},
		{":01", x11Display{number: 1, local: true}},
		{":-1", x11Display{number: -1, local: true}},
		{"unix:-1", x11Display{host: "unix", number: -1, local: true}},
		{":1.+2", x11Display{number: 1, screen: 2, local: true}},
		{":1. 2", x11Display{number: 1, screen: 2, local: true}},
		{":1.\v2", x11Display{number: 1, screen: 2, local: true}},
		{":1.-1", x11Display{number: 1, screen: -1, local: true}},
		{"unix:60000.-1", x11Display{host: "unix", number: 60000, screen: -1, local: true}},
		{":4294967297", x11Display{number: 1, local: true}},
		{":2147483648", x11Display{number: -2147483648, local: true}},
		{":-2147483649", x11Display{number: 2147483647, local: true}},
		{":18446744073709551615", x11Display{number: -1, local: true}},
		{":18446744073709551616", x11Display{number: -1, local: true}},
		{":-18446744073709551616", x11Display{number: -1, local: true}},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := parseX11Display(tc.input)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("display=%+v error=%v, want %+v", got, err, tc.want)
			}
		})
	}
	for _, input := range []string{":", ":1 ", ":0x1", ":1.", ":1.2 ", ":1.0x", "host:1.0.0", "unix:invalid", ":+", ": -"} {
		t.Run("reject "+input, func(t *testing.T) {
			if got, err := parseX11Display(input); err == nil {
				t.Fatalf("xcb_parse_display rejects %q, PiG accepted %+v", input, got)
			}
		})
	}
}

// x11OpenOracle is Pi's open_clipboard connection sequence (linux-platform-x11.c:184-189) against the system libxcb: it prints the root window of the
// screen Pi selects, or "error" where Pi's open_clipboard returns false.
const x11OpenOracle = `#include <stdio.h>
#include <xcb/xcb.h>

int main(void) {
    int screen_number = 0;
    xcb_connection_t* connection = xcb_connect(0, &screen_number);
    if (xcb_connection_has_error(connection)) {
        puts("error");
        xcb_disconnect(connection);
        return 0;
    }
    xcb_screen_iterator_t screens = xcb_setup_roots_iterator(xcb_get_setup(connection));
    while (screen_number-- > 0 && screens.rem) xcb_screen_next(&screens);
    if (!screens.rem) puts("error");
    else printf("root %u\n", screens.data->root);
    xcb_disconnect(connection);
    return 0;
}
`

// Each DISPLAY spelling reaches the same screen as Pi's xcb_connect on a two-screen server that listens on its Unix socket and on TCP, or fails as Pi's does.
func TestX11DisplaySpellingsConnectAsPiDoes(t *testing.T) {
	for _, command := range []string{"cc", "Xvfb"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Fatalf("the X11 display oracle requires %s: %v", command, err)
		}
	}
	directory := t.TempDir()
	source, oracle := filepath.Join(directory, "open.c"), filepath.Join(directory, "open")
	if err := os.WriteFile(source, []byte(x11OpenOracle), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.CommandContext(t.Context(), "cc", "-std=c11", "-Wall", "-Wextra", "-Werror", source, "-lxcb", "-o", oracle).CombinedOutput(); err != nil {
		t.Fatalf("compile the xcb oracle: %v\n%s", err, output)
	}
	// Each spelling connects the oracle and then PiG, one client at a time. Without -noreset the server resets whenever its
	// last client leaves and closes a connection that arrives during the reset, so on a loaded host the next client, PiG's
	// or the oracle's, gets EOF or a connection reset.
	server := startNativeTestProcess(t, "Xvfb", []string{"-displayfd", "1", "-screen", "0", "640x480x24", "-screen", "1", "320x240x24", "-listen", "tcp", "-ac", "-noreset"}, os.Environ())
	display := nativeReady(t, server)
	//portlint:allow numbers Xvfb -displayfd writes the display number it chose in decimal; the test only adds 2^16 and 2^32 to it
	number, err := strconv.ParseInt(display, 10, 64)
	if err != nil {
		t.Fatalf("Xvfb display %q: %v", display, err)
	}
	spellings := []string{
		":" + display, ": " + display, ":\t" + display, ":+" + display, ":0" + display, ":" + display + ".0", ":" + display + ".1",
		":" + display + ".+1", ":" + display + ".-1", ":" + display + ".2", ":-" + display, ":" + strconv.FormatInt(number+1<<32, 10),
		"unix:" + display + ".-1", "127.0.0.1:" + display, "127.0.0.1:" + strconv.FormatInt(number+1<<16, 10), "tcp/127.0.0.1:" + display + ".-1",
		":18446744073709551616",
	}
	for _, spelling := range spellings {
		t.Run(spelling, func(t *testing.T) {
			// Neither xcb_connect nor PiG's connection setup has a deadline of its own (the transfer deadline starts after setup), so the test budget bounds both.
			ctx := testbudget.Context(t)
			command := exec.CommandContext(ctx, oracle)
			command.Env = append(os.Environ(), "DISPLAY="+spelling)
			output, err := command.Output()
			if err != nil {
				t.Fatalf("xcb oracle: %v", err)
			}
			want := strings.TrimSpace(string(output))
			got, pigErr := pigX11Root(ctx, spelling)
			if got != want {
				t.Fatalf("DISPLAY=%q: PiG %s (%v), Pi (xcb_connect) %s", spelling, got, pigErr, want)
			}
		})
	}
}

// pigX11Root is the root window PiG's openX11Clipboard creates its window on for DISPLAY=spelling, or "error" with the reason.
func pigX11Root(ctx context.Context, spelling string) (string, error) {
	display, err := parseX11Display(spelling)
	if err != nil {
		return "error", err
	}
	connection, err := dialX11(ctx, display)
	if err != nil {
		return "error", err
	}
	defer func() { _ = connection.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		if err = connection.SetDeadline(deadline); err != nil {
			return "error", err
		}
	}
	_, root, err := setupX11(connection, display)
	if err != nil {
		return "error", err
	}
	return fmt.Sprintf("root %d", root), nil
}
