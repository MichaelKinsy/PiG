package main

import (
	"slices"
	"testing"
)

const fxArgs = `package main

type flags struct {
	verbose, noHelp, lonely, sink bool
	name                          string
}

func parseFlags(args []string) flags {
	var f flags
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--verbose", "-v":
			f.verbose = true
		case "--name":
			f.name = args[i+1]
		case "--nohelp":
			f.noHelp = true
		case "--lonely":
			f.lonely = true
		case "--sink":
			f.sink = true
		case "--untested":
		}
	}
	return f
}

func run(f flags) string {
	if f.lonely {
		return "lonely"
	}
	if f.verbose && !f.noHelp {
		return f.name
	}
	return ""
}

func printHelp() string {
	return "  --verbose, -v  verbose\n  --name <n>  name\n  --untested\n"
}

func otherHelp() string { return "  --sink --lonely  help text\n" }

func main() {}
`

const fxArgsTest = `package main

import "testing"

func TestParseFlags(t *testing.T) {
	f := parseFlags([]string{"--verbose", "-v", "--name", "x", "--nohelp", "--lonely", "--sink"})
	if run(f) != "" || f.name != "x" {
		t.Fatal("flags")
	}
}

func TestHelp(t *testing.T) {
	if printHelp() == "" {
		t.Fatal("help")
	}
}
`

const fxPackages = `package main

type pkgOptions struct{ help bool }

func parsePackageCommand(args []string) pkgOptions {
	var o pkgOptions
	for _, arg := range args {
		switch arg {
		case "list", "install":
		case "--help":
			o.help = true
		}
	}
	return o
}

func runPackage(o pkgOptions) string {
	if o.help {
		return packageHelp()
	}
	return ""
}

func packageHelp() string { return "Usage: pi install|list [--help]\n" }
`

const fxPackagesTest = `package main

import "testing"

func TestInstallHelp(t *testing.T) {
	if runPackage(parsePackageCommand([]string{"install", "--help"})) == "" {
		t.Fatal("help")
	}
}
`

const fxConfig = `package main

import "slices"

func runConfigCommand(args []string) string {
	if slices.Contains(args, "-h") || slices.Contains(args, "--help") {
		return printConfigHelp()
	}
	return ""
}

func printConfigHelp() string { return "usage" }
`

const fxConfigTest = `package main

import "testing"

func TestConfigHelp(t *testing.T) {
	if runConfigCommand([]string{"--help"}) != "usage" {
		t.Fatal("help")
	}
}
`

// C1 closes a command-line row only when the parser names the flag and every alias and an asserting test names it, and the subcommand for a subcommand's flag.
func TestCLIRowsNeedParserAliasesAndAssertingTests(t *testing.T) {
	flag := func(id, command, name string, aliases ...string) m {
		return m{"id": id, "command": command, "flag": name, "aliases": aliases}
	}
	rows := []m{
		flag("cli:pi/--verbose", "pi", "--verbose", "-v"),
		flag("cli:pi/--name", "pi", "--name"),
		flag("cli:pi/--untested", "pi", "--untested"),
		flag("cli:pi/--nohelp", "pi", "--nohelp"),
		flag("cli:pi/--sink", "pi", "--sink"),
		flag("cli:pi/--lonely", "pi", "--lonely"),
		flag("cli:pi/--missing", "pi", "--missing"),
		flag("cli:pi/--aliased", "pi", "--name", "-n"),
		flag("cli:pi-install/--help", "pi-install", "--help"),
		flag("cli:pi-list/--help", "pi-list", "--help"),
		flag("cli:pi-config/--help", "pi-config", "--help", "-h"),
		flag("cli:pi-config/--local", "pi-config", "--local"),
	}
	_, ds := fixtureLedger(t, map[string]string{
		"coding/cli/args.go": fxArgs, "coding/cli/args_test.go": fxArgsTest,
		"coding/cli/package_commands.go": fxPackages, "coding/cli/package_commands_test.go": fxPackagesTest,
		"coding/cli/config_command.go": fxConfig, "coding/cli/config_command_test.go": fxConfigTest,
		"test/parity/interfaces/cli-v0.0.0.json": string(js(m{"interfaces": rows})),
	}, func(inv []m) []m {
		for _, r := range rows {
			inv = append(inv, m{"id": r["id"], "_mappingOnly": true})
		}
		return inv
	}, nil, nil)
	for _, tc := range []struct{ id, reason, target string }{
		{"cli:pi/--verbose", "", "coding/cli/args.go#parseFlags"},
		{"cli:pi/--name", "", "coding/cli/args.go#parseFlags"},
		{"cli:pi/--untested", reasonCLI, ""},
		{"cli:pi/--nohelp", reasonCLI, ""},
		{"cli:pi/--sink", reasonCLI, ""},
		{"cli:pi/--lonely", reasonCLI, ""},
		{"cli:pi/--missing", reasonCLI, ""},
		{"cli:pi/--aliased", reasonCLI, ""},
		{"cli:pi-install/--help", "", "coding/cli/package_commands.go#parsePackageCommand"},
		{"cli:pi-list/--help", reasonCLI, ""},
		{"cli:pi-config/--help", "", "coding/cli/config_command.go#runConfigCommand"},
		{"cli:pi-config/--local", reasonCLI, ""},
	} {
		d := ds[tc.id]
		if d == nil {
			t.Fatalf("%s is undecided", tc.id)
		}
		if tc.reason != "" {
			if !d.Gap || d.Reason != tc.reason {
				t.Errorf("%s: gap=%v reason=%q, want gap %q", tc.id, d.Gap, d.Reason, tc.reason)
			}
			continue
		}
		if d.Gap {
			t.Errorf("%s: gap %s: %s", tc.id, d.Reason, d.Detail)
			continue
		}
		if d.Sym == nil || d.Sym.Target() != tc.target || len(d.Info.Tests) == 0 {
			t.Errorf("%s: gap=%v sym=%v info=%v, want %s with an asserting test", tc.id, d.Gap, d.Sym, d.Info, tc.target)
		}
	}
}

// A test that covers both the parser and the help text is one evidence entry: the mapping rejects a repeated reference.
func TestAppendNewKeepsEvidenceUnique(t *testing.T) {
	got := appendNew([]string{"test:a#A", "test:b#B"}, []string{"test:b#B", "test:c#C", "test:c#C"})
	if want := []string{"test:a#A", "test:b#B", "test:c#C"}; !slices.Equal(got, want) {
		t.Fatalf("appendNew = %v, want %v", got, want)
	}
}
