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
	const line = "  --listen <path>                Serve one RPC session per connection on the Unix socket at path (with --mode rpc)"
	if !strings.Contains(pigCommands, line) {
		t.Errorf("pigCommands lacks the --listen help line")
	}
}
