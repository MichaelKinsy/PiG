package cli

import (
	"strings"
	"testing"
)

func TestParseArgsListen(t *testing.T) {
	const combo = "--listen cannot be combined with --continue, --resume, --session, --session-id or --fork"
	cases := []struct {
		args       []string
		listen     string
		diagnostic string
	}{
		{[]string{"--mode", "rpc", "--listen", "/tmp/x.sock"}, "/tmp/x.sock", ""},
		{[]string{"--mode", "rpc", "--listen"}, "", "--listen requires a socket path"},
		{[]string{"--mode", "rpc", "--listen", "--no-session"}, "", "--listen requires a socket path"},
		{[]string{"--listen", "/tmp/x.sock"}, "/tmp/x.sock", "--listen requires --mode rpc"},
		{[]string{"--mode", "rpc", "--listen", "/tmp/x.sock", "--continue"}, "/tmp/x.sock", combo},
		{[]string{"--mode", "rpc", "--listen", "/tmp/x.sock", "--session", "s"}, "/tmp/x.sock", combo},
	}
	for _, tc := range cases {
		flags := parseArgs(tc.args)
		if flags.Listen != tc.listen {
			t.Errorf("%v: Listen = %q, want %q", tc.args, flags.Listen, tc.listen)
		}
		got := ""
		for _, d := range flags.Diagnostics {
			if d.Type == "error" {
				got = d.Message
			}
		}
		if got != tc.diagnostic {
			t.Errorf("%v: diagnostic = %q, want %q", tc.args, got, tc.diagnostic)
		}
	}
}

func TestPigCommandsListsListen(t *testing.T) {
	const line = "  --listen <path>                Serve one RPC session per connection on the Unix socket at path (with --mode rpc);"
	if !strings.Contains(pigCommands, line) {
		t.Errorf("pigCommands lacks the --listen help line")
	}
}

func TestParseArgsListenShareExtensions(t *testing.T) {
	cases := []struct {
		args       []string
		share      bool
		diagnostic string
	}{
		{[]string{"--mode", "rpc", "--listen", "/tmp/x.sock"}, false, ""},
		{[]string{"--mode", "rpc", "--listen", "/tmp/x.sock", "--listen-share-extensions"}, true, ""},
		{[]string{"--listen-share-extensions", "--mode", "rpc", "--listen", "/tmp/x.sock"}, true, ""},
		{[]string{"--mode", "rpc", "--listen-share-extensions"}, true, "--listen-share-extensions requires --listen"},
		{[]string{"--listen-share-extensions"}, true, "--listen-share-extensions requires --listen"},
	}
	for _, tc := range cases {
		flags := parseArgs(tc.args)
		if flags.ListenShareExtensions != tc.share {
			t.Errorf("%v: ListenShareExtensions = %v, want %v", tc.args, flags.ListenShareExtensions, tc.share)
		}
		got := ""
		for _, d := range flags.Diagnostics {
			if d.Type == "error" {
				got = d.Message
			}
		}
		if got != tc.diagnostic {
			t.Errorf("%v: diagnostic = %q, want %q", tc.args, got, tc.diagnostic)
		}
	}
}

// The help says what the opt-in does and warns where it is used (D232).
func TestPigCommandsWarnsAboutListenShareExtensions(t *testing.T) {
	for _, want := range []string{
		"--listen-share-extensions      With --listen, let the sessions share extension processes. WARNING: module-level",
		"and package-level extension state is then shared between sessions",
		"every session has its own extension processes",
	} {
		if !strings.Contains(pigCommands, want) {
			t.Errorf("pigCommands lacks %q", want)
		}
	}
}
