package cli

import (
	"slices"
	"testing"
)

// cli/args.ts parseArgs: --api-key, --append-system-prompt (repeatable), --no-builtin-tools (and -nbt), --no-mcp, --theme
// (repeatable) and --use-theme take their values and set the matching members; --use-theme without a name, or followed by an
// option, is an error diagnostic, and an option whose value is missing is not consumed as a flag.
// mutation-checked: dropping the --api-key, --append-system-prompt, --no-builtin-tools, --no-mcp, --theme and --use-theme cases of parseArgs fails it
// Pi: packages/coding-agent/src/cli/args.ts:119 (--api-key)
// Pi: packages/coding-agent/src/cli/args.ts:123 (--append-system-prompt)
// Pi: packages/coding-agent/src/cli/args.ts:149 (--no-builtin-tools)
// Pi: packages/coding-agent/src/cli/args.ts:185 (--no-mcp)
// Pi: packages/coding-agent/src/cli/args.ts:193 (--theme)
// Pi: packages/coding-agent/src/cli/args.ts:196 (--use-theme)
func TestParseFlagsTakesApiKeySystemPromptAppendBuiltinToolsMcpAndThemeOptions(t *testing.T) {
	got := parseArgs([]string{"--api-key", "k1", "--append-system-prompt", "one", "--append-system-prompt", "two", "--no-builtin-tools", "--no-mcp", "--theme", "a.json", "--theme", "b.json", "--use-theme", "dark"})
	if got.APIKey != "k1" {
		t.Fatalf("APIKey = %q, want k1", got.APIKey)
	}
	if !slices.Equal(got.AppendSystemPrompt, []string{"one", "two"}) {
		t.Fatalf("AppendSystemPrompt = %q, want one, two", got.AppendSystemPrompt)
	}
	if !got.NoBuiltinTools || !got.NoMcp {
		t.Fatalf("NoBuiltinTools = %v, NoMcp = %v, want both set", got.NoBuiltinTools, got.NoMcp)
	}
	if !slices.Equal(got.Themes, []string{"a.json", "b.json"}) {
		t.Fatalf("Themes = %q, want a.json, b.json", got.Themes)
	}
	if got.UseTheme == nil || *got.UseTheme != "dark" {
		t.Fatalf("UseTheme = %v, want dark", got.UseTheme)
	}
	if short := parseArgs([]string{"-nbt"}); !short.NoBuiltinTools {
		t.Fatal("-nbt did not set NoBuiltinTools")
	}
	for _, args := range [][]string{{"--use-theme"}, {"--use-theme", "--print"}} {
		diagnostics := parseArgs(args).Diagnostics
		if len(diagnostics) != 1 || diagnostics[0].Type != "error" || diagnostics[0].Message != "--use-theme requires a theme name" {
			t.Fatalf("%q diagnostics = %+v, want the --use-theme error", args, diagnostics)
		}
	}
}

// package-manager-cli.ts parsePackageCommand (:409-470): --local belongs to install and remove, and --all, --self and
// --force to update; on any other command the first such option is the invalid option and sets nothing.
// mutation-checked: dropping the command checks of the --local, --all, --self and --force cases fails it
// Pi: packages/coding-agent/src/package-manager-cli.ts:409 (--local)
// Pi: packages/coding-agent/src/package-manager-cli.ts:418 (--self)
// Pi: packages/coding-agent/src/package-manager-cli.ts:445 (--all)
// Pi: packages/coding-agent/src/package-manager-cli.ts:464 (--force)
func TestParsePackageCommandPlacesLocalAllSelfAndForceOnTheirCommands(t *testing.T) {
	type flagCase struct {
		flag  string
		valid []string
		set   func(*packageCLIOptions) bool
	}
	for _, c := range []flagCase{
		{"--local", []string{"install", "remove"}, func(o *packageCLIOptions) bool { return o.local }},
		{"--all", []string{"update"}, func(o *packageCLIOptions) bool { return o.allPackages }},
		{"--self", []string{"update"}, func(o *packageCLIOptions) bool { return o.selfOnly }},
		{"--force", []string{"update"}, func(o *packageCLIOptions) bool { return o.force }},
	} {
		for _, command := range []string{"install", "remove", "update", "list"} {
			args := []string{command, c.flag}
			if command == "install" || command == "remove" {
				args = append(args, "npm:example")
			}
			opts, ok := parsePackageCommand(args)
			if !ok {
				t.Fatalf("%q is not a package command", args)
			}
			if slices.Contains(c.valid, command) {
				if !c.set(opts) || opts.invalidOption != "" {
					t.Fatalf("%q: set = %v, invalid option %q; want the option set and no invalid option", args, c.set(opts), opts.invalidOption)
				}
			} else if c.set(opts) || opts.invalidOption != c.flag {
				t.Fatalf("%q: set = %v, invalid option %q; want nothing set and %q invalid", args, c.set(opts), opts.invalidOption, c.flag)
			}
		}
	}
}
