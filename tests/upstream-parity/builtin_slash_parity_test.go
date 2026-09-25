package parity

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// builtinNamePattern matches each entry of upstream's BUILTIN_SLASH_COMMANDS
// array, whose elements start `{ name: "<id>"`.
var builtinNamePattern = regexp.MustCompile(`(?m)^\s*\{\s*name:\s*"([a-z][a-z0-9-]*)"`)

// TestBuiltinSlashCommands_CoverUpstream pins pig's builtin slash commands
// against upstream's BUILTIN_SLASH_COMMANDS rather than against pig's own
// list. The existing registry test compares BuiltinSlashCommands() to
// defaultBuiltins(), which cannot notice upstream adding a command, and a
// source comment claimed for a long time that seven upstream builtins were
// unimplemented stubs when in fact all of them had landed.
//
// A missing command is a real user-visible gap: the user types a command that
// works in pi and pig answers "Unknown command".
func TestBuiltinSlashCommands_CoverUpstream(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine caller path")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	src := filepath.Join(repoRoot, ".upstream", "current",
		"packages", "coding-agent", "src", "core", "slash-commands.ts")
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("mirror missing at %s; run automation/gen/mirror-upstream.sh: %v", src, err)
	}

	upstreamNames := builtinSlashCommandNames(t, string(raw))
	if len(upstreamNames) == 0 {
		t.Fatal("parsed no builtin names from upstream slash-commands.ts; the array shape changed")
	}

	var pigNames []string
	for _, cmd := range codingagent.BuiltinSlashCommands() {
		pigNames = append(pigNames, cmd.Name)
	}

	for _, name := range upstreamNames {
		if !slices.Contains(pigNames, name) {
			gap(t, "slash:"+name, "upstream builtin /%s not implemented in pig; "+
				"implement it or record a numbered divergence in DIVERGENCES.md", name)
		}
	}
}

// builtinSlashCommandNames extracts the names declared in upstream's
// BUILTIN_SLASH_COMMANDS array literal.
func builtinSlashCommandNames(t *testing.T, source string) []string {
	t.Helper()
	const marker = "BUILTIN_SLASH_COMMANDS"
	start := indexAfter(source, marker)
	if start < 0 {
		t.Fatalf("%s not found in upstream slash-commands.ts", marker)
	}
	// The array ends at the first line that closes it at column zero.
	end := indexAfter(source[start:], "\n];")
	if end < 0 {
		t.Fatal("could not find the end of the BUILTIN_SLASH_COMMANDS array")
	}

	var names []string
	for _, m := range builtinNamePattern.FindAllStringSubmatch(source[start:start+end], -1) {
		names = append(names, m[1])
	}
	return names
}

// indexAfter returns the index just past the first occurrence of sub, or -1.
func indexAfter(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i + len(sub)
		}
	}
	return -1
}
