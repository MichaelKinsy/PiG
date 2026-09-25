package main

import (
	"bytes"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Expected diagnostics come from Pi 0.87.1 cli/args.ts parseArgs.
func TestParseFlagsDiagnosticsMatchPi(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		mode  string // empty means the default "text"
		print bool
		want  []argDiagnostic
	}{
		{name: "mode missing", args: []string{"--mode"}, want: []argDiagnostic{{"error", "--mode requires text, json, or rpc"}}},
		{name: "mode flag-shaped keeps next flag", args: []string{"--mode", "--print"}, print: true, want: []argDiagnostic{{"error", "--mode requires text, json, or rpc"}}},
		{name: "mode unknown", args: []string{"--mode", "yaml"}, want: []argDiagnostic{{"error", `Invalid mode "yaml". Valid values: text, json, rpc`}}},
		{name: "mode empty", args: []string{"--mode", ""}, want: []argDiagnostic{{"error", `Invalid mode "". Valid values: text, json, rpc`}}},
		{name: "mode valid", args: []string{"--mode", "json"}, mode: "json"},
		{name: "thinking invalid", args: []string{"--thinking", "huge"}, want: []argDiagnostic{{"warning", `Invalid thinking level "huge". Valid values: off, minimal, low, medium, high, xhigh, max`}}},
		{name: "unknown short option", args: []string{"-x"}, want: []argDiagnostic{{"error", "Unknown option: -x"}}},
		{name: "name missing", args: []string{"--name"}, want: []argDiagnostic{{"error", "--name requires a value"}}},
		{name: "use-theme missing", args: []string{"--use-theme", "--print"}, print: true, want: []argDiagnostic{{"error", "--use-theme requires a theme name"}}},
		{name: "tui-mode missing", args: []string{"--tui-mode"}, want: []argDiagnostic{{"error", "--tui-mode requires regular or fullscreen"}}},
		{name: "tui-mode invalid", args: []string{"--tui-mode", "compact"}, want: []argDiagnostic{{"error", `Invalid TUI mode "compact". Valid values: regular, fullscreen`}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flags := parseFlags(tc.args)
			if !slices.Equal(flags.Diagnostics, tc.want) {
				t.Fatalf("diagnostics = %#v, want %#v", flags.Diagnostics, tc.want)
			}
			wantMode := tc.mode
			if wantMode == "" {
				wantMode = "text"
			}
			if flags.Mode != wantMode {
				t.Fatalf("mode = %q, want %q", flags.Mode, wantMode)
			}
			if (flags.Print != "") != tc.print {
				t.Fatalf("print = %q, want enabled=%t", flags.Print, tc.print)
			}
		})
	}
}

func TestReportArgDiagnosticsMatchesPiMainOutput(t *testing.T) {
	diagnostics := []argDiagnostic{{"warning", "w"}, {"error", "e"}}
	var plain bytes.Buffer
	if !reportArgDiagnostics(&plain, diagnostics, false) {
		t.Fatal("an error diagnostic must request exit status 1")
	}
	if got, want := plain.String(), "Warning: w\nError: e\n"; got != want {
		t.Fatalf("plain output = %q, want %q", got, want)
	}
	var colored bytes.Buffer
	reportArgDiagnostics(&colored, diagnostics, true)
	if got, want := colored.String(), "\x1b[33mWarning: w\x1b[39m\n\x1b[31mError: e\x1b[39m\n"; got != want {
		t.Fatalf("colored output = %q, want %q", got, want)
	}
	if reportArgDiagnostics(&bytes.Buffer{}, diagnostics[:1], false) {
		t.Fatal("a warning alone must not exit")
	}
}

func TestTuiModeAndUseThemeParseForThisRun(t *testing.T) {
	flags := parseFlags([]string{"--tui-mode", "fullscreen", "--use-theme", "light"})
	if flags.TuiMode != "fullscreen" || flags.UseTheme != "light" || len(flags.Diagnostics) != 0 {
		t.Fatalf("flags = %+v", flags)
	}
}

// Every option in Pi's help must be one PiG parses; a listed option that PiG
// ignores would be a false claim.
func TestHelpListsOnlyOptionsPiGParses(t *testing.T) {
	option := regexp.MustCompile(`(?m)^  (--[a-z][a-z-]*)(?:, (-[a-z]+))?(?: <[^>]+>)?`)
	values := map[string]string{"--mode": "json", "--tui-mode": "regular", "--thinking": "low"}
	for _, match := range option.FindAllStringSubmatch(upstreamHelp, -1) {
		name := match[1]
		if name == "--help" || name == "--version" {
			continue
		}
		args := []string{name}
		if strings.Contains(match[0], "<") {
			value := values[name]
			if value == "" {
				value = "value"
			}
			args = append(args, value)
		} else if value, ok := values[name]; ok {
			args = append(args, value)
		}
		flags := parseFlags(args)
		if _, unknown := flags.UnknownFlags[strings.TrimPrefix(name, "--")]; unknown {
			t.Errorf("help lists %s but parseFlags treats it as an extension flag", name)
		}
		for _, diagnostic := range flags.Diagnostics {
			t.Errorf("help lists %s but parseFlags reports %q", name, diagnostic.Message)
		}
	}
}

func TestHelpIsPisHelpWithPiGCommandsAfterPisCommands(t *testing.T) {
	var out bytes.Buffer
	printHelp(&out, false)
	text := out.String()
	if !strings.HasPrefix(text, "pig - AI coding assistant with read, bash, edit, write tools\n") {
		t.Fatalf("help starts %q", text[:min(80, len(text))])
	}
	commands, pig, options := strings.Index(text, "Commands:\n"), strings.Index(text, "PiG Commands:\n"), strings.Index(text, "Options:\n")
	if commands < 0 || pig < commands || options < pig {
		t.Fatalf("section order: Commands %d, PiG Commands %d, Options %d", commands, pig, options)
	}
	var colored bytes.Buffer
	printHelp(&colored, true)
	if !strings.HasPrefix(colored.String(), "\x1b[1mpig\x1b[22m - ") || !strings.Contains(colored.String(), "\x1b[1mOptions:\x1b[22m") {
		t.Fatal("terminal help lacks bold headers")
	}
}
