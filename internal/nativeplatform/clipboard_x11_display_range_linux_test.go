//go:build linux

package nativeplatform

import (
	"reflect"
	"testing"
)

// Pi linux-platform-x11.c:185 delegates to xcb_connect. Local Unix display numbers name sockets, not TCP ports; the 6000 port offset only limits network displays.
func TestX11LocalDisplayNumbersAreNotTCPPorts(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  x11Display
	}{
		{":59535", x11Display{number: "59535", local: true}},
		{":59536", x11Display{number: "59536", local: true}},
		{":60000", x11Display{number: "60000", local: true}},
		{"unix:60000", x11Display{host: "unix", number: "60000", local: true}},
		{"unix/:65536.2", x11Display{host: "unix", number: "65536", screen: 2, local: true}},
		{"host/unix:60000", x11Display{host: "unix", number: "60000", local: true}},
		{"localhost:59535", x11Display{host: "localhost", number: "59535"}},
		{"tcp/localhost:59535", x11Display{host: "localhost", number: "59535"}},
		{"tcp/[::1]:0.3", x11Display{host: "::1", number: "0", screen: 3}},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := parseX11Display(tc.input)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("display=%+v error=%v, want %+v", got, err, tc.want)
			}
		})
	}
	for _, input := range []string{"localhost:59536", "tcp/localhost:60000", "[::1]:60000", "unix:-1", "unix:invalid", "unix:60000.-1"} {
		t.Run("reject "+input, func(t *testing.T) {
			if got, err := parseX11Display(input); err == nil {
				t.Fatalf("invalid display accepted: %+v", got)
			}
		})
	}
}
