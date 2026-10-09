package experimental

import (
	"reflect"
	"testing"
)

// upstream: cli/experimental/command.ts:parseOptions. Every option except the repeatable -e rejects a second occurrence in either spelling, and the duplicate does not replace the first value.
func TestExperimentalCLIRejectsDuplicateOptions(t *testing.T) {
	t.Parallel()
	const serverID = "00000000-0000-4000-8000-000000000001"
	for _, test := range []struct {
		args []string
		dup  string
	}{
		{[]string{"server", "--server-id", serverID, "--server-id=" + serverID}, "--server-id"},
		{[]string{"server", "--session-dir", "a", "--session-dir", "b"}, "--session-dir"},
		{[]string{"server", "--provider", "p", "--provider", "q", "--model", "m"}, "--provider"},
		{[]string{"server", "--model", "m", "--model=n"}, "--model"},
		{[]string{"client", "--connect", "unix:///a", "--connect", "unix:///b"}, "--connect"},
		{[]string{"client", "--session-id", "a", "--session-id", "b"}, "--session-id"},
		{[]string{"client", "--provider", "p", "--provider", "q", "--model", "m"}, "--provider"},
		{[]string{"client", "--model", "m", "--model", "n"}, "--model"},
		{[]string{"client", "--continue", "--continue"}, "--continue"},
		{[]string{"client", "-c", "-c"}, "-c"},
		{[]string{"client", "--resume", "--resume"}, "--resume"},
		{[]string{"client", "-r", "-r"}, "-r"},
	} {
		got, err := Cli.Parse(test.args)
		want := CommandParseResult{Errors: []string{test.dup + " may only be specified once"}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("parse(%q) = %#v, %v; want %#v", test.args, got, err, want)
		}
	}
	got, err := Cli.Parse([]string{"client", "-e", "a", "-e", "b", "-e=c"})
	want := CommandParseResult{Ok: true, Command: ClientCommand{Command: "client", PluginPackages: []string{"a", "b", "c"}}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("repeatable -e = %#v, %v; want %#v", got, err, want)
	}
}
