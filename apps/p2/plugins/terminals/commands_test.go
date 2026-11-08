package terminals

import (
	"errors"
	"reflect"
	"regexp"
	"testing"
)

// Ports test/p2/terminal-commands.test.ts: the terminal tool's tmux commands: argv parsed getopt-style as tmux does, and
// tmux-like errors.

func parse(t *testing.T, command string, args ...string) TerminalCommand {
	t.Helper()
	parsed, err := ParseCommand(command, args)
	if err != nil {
		t.Fatalf("ParseCommand(%s, %q): %v", command, args, err)
	}
	return parsed
}

func TestTmuxArgvOptionsFirstCombinedFlagsAttachedOrSeparateValuesAndDoubleDash(t *testing.T) {
	want := func(got, expected TerminalCommand) {
		t.Helper()
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("got %+v, want %+v", got, expected)
		}
	}
	want(parse(t, "new-window", "-n", "dev", "-c", "web", "npm", "run", "dev"),
		TerminalCommand{Command: "new-window", Name: "dev", Directory: "web", ShellCommand: "npm run dev", HasShellCommand: true})
	want(parse(t, "new-window"), TerminalCommand{Command: "new-window"})
	want(parse(t, "send-keys", "-t1", "-l", "npm test", "Enter"),
		TerminalCommand{Command: "send-keys", Target: "1", Literal: true, Keys: []string{"npm test", "Enter"}})
	// Options end at the first argument that is not one, or at `--`: `-t` after a key is a key.
	if got := parse(t, "send-keys", "-t", "dev", "--", "-l", "q").Command; got != "send-keys" {
		t.Fatalf("command = %q", got)
	}
	want(parse(t, "send-keys", "-t", "dev", "--", "-l"),
		TerminalCommand{Command: "send-keys", Target: "dev", Keys: []string{"-l"}})
	want(parse(t, "send-keys", "-t", "2", "C-c", "-t"),
		TerminalCommand{Command: "send-keys", Target: "2", Keys: []string{"C-c", "-t"}})
	// A value may start with `-`: `-S -50`.
	want(parse(t, "capture-pane", "-ep", "-t", "2", "-S", "-50"),
		TerminalCommand{Command: "capture-pane", Target: "2", Scrollback: 50, Escapes: true})
	if got := parse(t, "capture-pane", "-t", "2", "-S", "-").Scrollback; got != 100_000 {
		t.Fatalf("-S - = %d", got)
	}
	if got := parse(t, "capture-pane", "-pt2").Scrollback; got != 0 {
		t.Fatalf("-pt2 = %d", got)
	}
	want(parse(t, "kill-window", "-t", ":3"), TerminalCommand{Command: "kill-window", Target: ":3"})
	want(parse(t, "list-windows"), TerminalCommand{Command: "list-windows"})
}

func TestWaitsQuietExitNoneRegexWithATimeoutInSeconds(t *testing.T) {
	two := 2.5
	got := parse(t, "read", "-t", "1", "-w", `/\$ $/`, "-T", "2.5")
	want := TerminalCommand{Command: "read", Target: "1", WaitOptions: WaitOptions{Wait: &WaitFor{Kind: WaitMatch, Pattern: `\$ $`}, Timeout: &two}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v %+v, want %+v %+v", got, *got.Wait, want, *want.Wait)
	}
	got = parse(t, "read", "-t", "1", "-w", "/ready/i")
	if want := (WaitFor{Kind: WaitMatch, Pattern: "ready", IgnoreCase: true}); *got.Wait != want || got.Timeout != nil {
		t.Fatalf("got %+v", got)
	}
	for _, kind := range []string{"quiet", "exit", "none"} {
		if got := parse(t, "send-keys", "-t", "1", "-w", kind, "q"); *got.Wait != (WaitFor{Kind: kind}) {
			t.Fatalf("%s: %+v", kind, *got.Wait)
		}
	}
}

func TestRefusalsReadLikeTmuxs(t *testing.T) {
	refused := func(command string, args []string, message string, pattern bool) {
		t.Helper()
		_, err := ParseCommand(command, args)
		var refusal *CommandError
		if !errors.As(err, &refusal) {
			t.Fatalf("%s %q: err = %v, want a CommandError", command, args, err)
		}
		if pattern {
			if !regexp.MustCompile(message).MatchString(refusal.Message) {
				t.Fatalf("%s %q: %q does not match %s", command, args, refusal.Message, message)
			}
		} else if refusal.Message != message {
			t.Fatalf("%s %q: %q, want %q", command, args, refusal.Message, message)
		}
	}
	refused("send-keys", []string{"-x", "-t", "1", "a"}, "send-keys: unknown option -- x\nusage: send-keys [-l] [-w wait] [-T seconds] -t target-window key ...", false)
	refused("send-keys", []string{"-t"}, `^send-keys: option requires an argument -- t\nusage: send-keys `, true)
	refused("send-keys", []string{"Enter"}, "usage: send-keys [-l] [-w wait] [-T seconds] -t target-window key ...", false)
	refused("send-keys", []string{"-t", "1"}, `^usage: send-keys`, true)
	refused("read", nil, "usage: read [-w wait] [-T seconds] -t target-window", false)
	refused("read", []string{"-t", "1", "extra"}, `^usage: read`, true)
	refused("kill-window", nil, "usage: kill-window -t target-window", false)
	refused("list-windows", []string{"-a"}, `^list-windows: unknown option -- a`, true)
	refused("capture-pane", []string{"-t", "1", "-S", "ten"}, "capture-pane: invalid start line: ten", false)
	refused("read", []string{"-t", "1", "-w", "soon"}, "read: invalid wait: soon (quiet, exit, none or /regex/)", false)
	refused("read", []string{"-t", "1", "-w", "/(/"}, `^read: invalid regex: `, true)
	refused("read", []string{"-t", "1", "-T", "500"}, "read: invalid timeout: 500 (0 to 120 seconds)", false)
	refused("read", []string{"-t", "1", "-T", "x"}, `^read: invalid timeout: x`, true)
}
