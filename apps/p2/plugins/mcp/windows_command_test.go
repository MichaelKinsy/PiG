package mcp

import (
	"reflect"
	"strings"
	"testing"
)

// Ports test/p2/mcp-windows-command.test.ts: `.exe` and `.com` directly, everything else through `cmd.exe` with the command
// line in a variable, quoted as cross-spawn quotes it (the expected lines are cross-spawn's `lib/util/escape.js` output for
// the same values).

var cmd = []string{"cmd.exe", "/d", "/s", "/c", "%P2_MCP_COMMAND%"}

func TestAnExeOrComProgramStartsDirectlyItsArgumentsAsTheyAre(t *testing.T) {
	got := WindowsCommandOf(`C:\tools\server.exe`, []string{"a b", `"q"`})
	if want := []string{`C:\tools\server.exe`, "a b", `"q"`}; !reflect.DeepEqual(got.Argv, want) || len(got.Env) != 0 {
		t.Fatalf("got %#v", got)
	}
	got = WindowsCommandOf(`C:\TOOLS\SERVER.COM`, nil)
	if want := []string{`C:\TOOLS\SERVER.COM`}; !reflect.DeepEqual(got.Argv, want) || len(got.Env) != 0 {
		t.Fatalf("got %#v", got)
	}
}

func TestAnyOtherCommandRunsThroughCmdExeTheLineQuotedAsCrossSpawnQuotesIt(t *testing.T) {
	got := WindowsCommandOf("npx", []string{"plain", "a b", `say "hi"`, `C:\dir\`, `tail\"q`, "100%", "a&b|c", "^"})
	if !reflect.DeepEqual(got.Argv, cmd) {
		t.Fatalf("argv %#v", got.Argv)
	}
	want := strings.Join([]string{
		"npx",
		`^"plain^"`,
		`^"a^ b^"`,
		`^"say^ \^"hi\^"^"`,
		`^"C:\dir\\^"`,
		`^"tail\\\^"q^"`,
		`^"100^%^"`,
		`^"a^&b^|c^"`,
		`^"^^^"`,
	}, " ")
	if got.Env["P2_MCP_COMMAND"] != want {
		t.Fatalf("line %q, want %q", got.Env["P2_MCP_COMMAND"], want)
	}
}

func TestTheProgramsPathIsNormalizedAndItsMetaCharactersEscaped(t *testing.T) {
	got := WindowsCommandOf("C:/Program Files (x86)/tool.cmd", nil)
	if want := `C:\Program^ Files^ ^(x86^)\tool.cmd`; got.Env["P2_MCP_COMMAND"] != want {
		t.Fatalf("line %q, want %q", got.Env["P2_MCP_COMMAND"], want)
	}
}

func TestACmdShimInNodeModulesBinGetsItsArgumentsEscapedTwice(t *testing.T) {
	got := WindowsCommandOf(`C:\work\node_modules\.bin\server.cmd`, []string{"a b", "100%"})
	if !reflect.DeepEqual(got.Argv, cmd) {
		t.Fatalf("argv %#v", got.Argv)
	}
	want := `C:\work\node_modules\.bin\server.cmd ^^^"a^^^ b^^^" ^^^"100^^^%^^^"`
	if got.Env["P2_MCP_COMMAND"] != want {
		t.Fatalf("line %q, want %q", got.Env["P2_MCP_COMMAND"], want)
	}
}
