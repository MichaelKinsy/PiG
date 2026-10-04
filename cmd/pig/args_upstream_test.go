package main

import (
	"reflect"
	"testing"
)

// These snapshots preserve every assertion in Pi's parseArgs tests. Go's parser
// applies the text-mode default before returning; Pi applies it at the caller.
func TestArgsUpstream(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want CLIFlags
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:6
		{name: "parses --version flag", args: []string{"--version"}, want: CLIFlags{Version: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:11
		{name: "parses -v shorthand", args: []string{"-v"}, want: CLIFlags{Version: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:16
		{name: "--version takes precedence over other args", args: []string{"--version", "--help", "some message"}, want: CLIFlags{Version: true, Help: true, Args: []string{"some message"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:25
		{name: "parses --help flag", args: []string{"--help"}, want: CLIFlags{Help: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:30
		{name: "parses -h shorthand", args: []string{"-h"}, want: CLIFlags{Help: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:37
		{name: "parses --print flag", args: []string{"--print"}, want: CLIFlags{Print: " "}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:42
		{name: "parses -p shorthand", args: []string{"-p"}, want: CLIFlags{Print: " "}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:47
		{name: "parses prompt after -p even when it starts with YAML frontmatter", args: []string{"-p", "---\ntitle: hello\n---\nSay hi."}, want: CLIFlags{Print: " ", Args: []string{"---\ntitle: hello\n---\nSay hi."}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:55
		{name: "does not consume options after -p as prompts", args: []string{"-p", "--provider", "openai", "Say hi."}, want: CLIFlags{Print: " ", Provider: "openai", Args: []string{"Say hi."}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:64
		{name: "parses --continue flag", args: []string{"--continue"}, want: CLIFlags{Continue: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:69
		{name: "parses -c shorthand", args: []string{"-c"}, want: CLIFlags{Continue: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:76
		{name: "parses --resume flag", args: []string{"--resume"}, want: CLIFlags{ResumeAny: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:81
		{name: "parses -r shorthand", args: []string{"-r"}, want: CLIFlags{ResumeAny: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:88
		{name: "parses --provider", args: []string{"--provider", "openai"}, want: CLIFlags{Provider: "openai"}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:93
		{name: "parses --model", args: []string{"--model", "gpt-4o"}, want: CLIFlags{Model: "gpt-4o"}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:98
		{name: "parses --api-key", args: []string{"--api-key", "sk-test-key"}, want: CLIFlags{APIKey: "sk-test-key"}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:103
		{name: "parses --system-prompt", args: []string{"--system-prompt", "You are a helpful assistant"}, want: CLIFlags{SystemPrompt: "You are a helpful assistant", systemPromptSet: true}},
		// Pi args.ts preserves an explicitly supplied empty systemPrompt rather than omitting the property.
		{name: "preserves empty --system-prompt presence", args: []string{"--system-prompt", ""}, want: CLIFlags{systemPromptSet: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:108
		{name: "parses --append-system-prompt", args: []string{"--append-system-prompt", "Additional context"}, want: CLIFlags{AppendSystemPrompt: []string{"Additional context"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:113
		{name: "parses multiple --append-system-prompt flags", args: []string{"--append-system-prompt", "Context A", "--append-system-prompt", "Context B"}, want: CLIFlags{AppendSystemPrompt: []string{"Context A", "Context B"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:118
		{name: "parses --session", args: []string{"--session", "/path/to/session.jsonl"}, want: CLIFlags{Session: "/path/to/session.jsonl"}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:123
		{name: "parses --session-id", args: []string{"--session-id", "orchestrated-session"}, want: CLIFlags{SessionID: "orchestrated-session"}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:128
		{name: "parses --fork", args: []string{"--fork", "1234abcd"}, want: CLIFlags{Fork: "1234abcd"}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:134
		{name: "parses --export", args: []string{"--export", "session.jsonl"}, want: CLIFlags{Export: "session.jsonl"}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:139
		{name: "parses --thinking", args: []string{"--thinking", "high"}, want: CLIFlags{Thinking: "high"}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:144
		{name: "parses --models as comma-separated list", args: []string{"--models", "gpt-4o,claude-sonnet,gemini-pro"}, want: CLIFlags{Models: []string{"gpt-4o", "claude-sonnet", "gemini-pro"}}},
		// .upstream/v1.0.1/packages/coding-agent/test/args.test.ts:150 (Issue #10334)
		{name: "ignores empty entries in --models", args: []string{"--models", "gpt-4o, ,claude-sonnet,"}, want: CLIFlags{Models: []string{"gpt-4o", "claude-sonnet"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:152
		{name: "parses --mode text", args: []string{"--mode", "text"}, want: CLIFlags{Mode: "text", modeSet: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:152
		{name: "parses --mode json", args: []string{"--mode", "json"}, want: CLIFlags{Mode: "json", modeSet: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:152
		{name: "parses --mode rpc", args: []string{"--mode", "rpc"}, want: CLIFlags{Mode: "rpc", modeSet: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:158
		{name: "rejects invalid --mode value 'yaml'", args: []string{"--mode", "yaml", "--version"}, want: CLIFlags{Version: true, Diagnostics: []argDiagnostic{{Type: "error", Message: "Invalid mode \"yaml\". Valid values: text, json, rpc"}}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:158
		{name: "rejects invalid --mode value ''", args: []string{"--mode", "", "--version"}, want: CLIFlags{Version: true, Diagnostics: []argDiagnostic{{Type: "error", Message: "Invalid mode \"\". Valid values: text, json, rpc"}}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:169
		{name: "reports a missing --mode value", args: []string{"--mode"}, want: CLIFlags{Diagnostics: []argDiagnostic{{Type: "error", Message: "--mode requires text, json, or rpc"}}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:176
		{name: "does not consume another option as a --mode value", args: []string{"--mode", "--version"}, want: CLIFlags{Version: true, Diagnostics: []argDiagnostic{{Type: "error", Message: "--mode requires text, json, or rpc"}}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:184
		{name: "reports an invalid --mode value after a valid one", args: []string{"--mode", "json", "--mode", "yaml"}, want: CLIFlags{Mode: "json", modeSet: true, Diagnostics: []argDiagnostic{{Type: "error", Message: "Invalid mode \"yaml\". Valid values: text, json, rpc"}}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:193
		{name: "parses --name flag with value", args: []string{"--name", "my-session"}, want: CLIFlags{Name: "my-session", NameSet: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:198
		{name: "parses -n shorthand", args: []string{"-n", "quick-session"}, want: CLIFlags{Name: "quick-session", NameSet: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:203
		{name: "preserves empty values for main validation", args: []string{"--name", ""}, want: CLIFlags{Name: "", NameSet: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:213
		{name: "reports missing value", args: []string{"--name"}, want: CLIFlags{Diagnostics: []argDiagnostic{{Type: "error", Message: "--name requires a value"}}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:218
		{name: "works alongside other flags", args: []string{"--name", "named-run", "--print", "--model", "gpt-4o", "hello"}, want: CLIFlags{Name: "named-run", NameSet: true, Print: " ", Model: "gpt-4o", Args: []string{"hello"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:228
		{name: "parses --no-session flag", args: []string{"--no-session"}, want: CLIFlags{NoSession: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:233
		{name: "preserves custom session IDs for non-persisting commands --help", args: []string{"--session-id", "ephemeral-id", "--help"}, want: CLIFlags{SessionID: "ephemeral-id", Help: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:233
		{name: "preserves custom session IDs for non-persisting commands --list-models", args: []string{"--session-id", "ephemeral-id", "--list-models"}, want: CLIFlags{SessionID: "ephemeral-id", ListModelsAll: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:233
		{name: "preserves custom session IDs for non-persisting commands --no-session", args: []string{"--session-id", "ephemeral-id", "--no-session"}, want: CLIFlags{SessionID: "ephemeral-id", NoSession: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:250
		{name: "parses single --extension", args: []string{"--extension", "./my-extension.ts"}, want: CLIFlags{Extensions: []string{"./my-extension.ts"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:255
		{name: "parses -e shorthand", args: []string{"-e", "./my-extension.ts"}, want: CLIFlags{Extensions: []string{"./my-extension.ts"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:260
		{name: "parses multiple --extension flags", args: []string{"--extension", "./ext1.ts", "-e", "./ext2.ts"}, want: CLIFlags{Extensions: []string{"./ext1.ts", "./ext2.ts"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:267
		{name: "parses --no-extensions flag", args: []string{"--no-extensions"}, want: CLIFlags{NoExtensions: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:272
		{name: "parses --no-extensions with explicit -e flags", args: []string{"--no-extensions", "-e", "foo.ts", "-e", "bar.ts"}, want: CLIFlags{NoExtensions: true, Extensions: []string{"foo.ts", "bar.ts"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:280
		{name: "parses single --skill", args: []string{"--skill", "./skill-dir"}, want: CLIFlags{Skills: []string{"./skill-dir"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:285
		{name: "parses multiple --skill flags", args: []string{"--skill", "./skill-a", "--skill", "./skill-b"}, want: CLIFlags{Skills: []string{"./skill-a", "./skill-b"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:292
		{name: "parses single --prompt-template", args: []string{"--prompt-template", "./prompts"}, want: CLIFlags{PromptTemplates: []string{"./prompts"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:297
		{name: "parses multiple --prompt-template flags", args: []string{"--prompt-template", "./one", "--prompt-template", "./two"}, want: CLIFlags{PromptTemplates: []string{"./one", "./two"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:304
		{name: "parses single --theme", args: []string{"--theme", "./theme.json"}, want: CLIFlags{Themes: []string{"./theme.json"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:309
		{name: "parses multiple --theme flags", args: []string{"--theme", "./dark.json", "--theme", "./light.json"}, want: CLIFlags{Themes: []string{"./dark.json", "./light.json"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:316
		{name: "parses --use-theme", args: []string{"--use-theme", "light"}, want: CLIFlags{UseTheme: new("light")}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:321
		{name: "reports when the theme name value is missing", args: []string{"--use-theme", "--print"}, want: CLIFlags{Print: " ", Diagnostics: []argDiagnostic{{Type: "error", Message: "--use-theme requires a theme name"}}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:330
		{name: "parses --no-skills flag", args: []string{"--no-skills"}, want: CLIFlags{NoSkills: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:337
		{name: "parses --no-prompt-templates flag", args: []string{"--no-prompt-templates"}, want: CLIFlags{NoPromptTemplates: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:344
		{name: "parses --no-themes flag", args: []string{"--no-themes"}, want: CLIFlags{NoThemes: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:351
		{name: "parses --no-context-files flag", args: []string{"--no-context-files"}, want: CLIFlags{NoContextFiles: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:356
		{name: "parses -nc shorthand", args: []string{"-nc"}, want: CLIFlags{NoContextFiles: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:363
		{name: "parses --approve", args: []string{"--approve"}, want: CLIFlags{ProjectTrustOverride: new(true)}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:368
		{name: "parses -a shorthand", args: []string{"-a"}, want: CLIFlags{ProjectTrustOverride: new(true)}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:373
		{name: "parses --no-approve", args: []string{"--no-approve"}, want: CLIFlags{ProjectTrustOverride: new(false)}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:378
		{name: "parses -na shorthand", args: []string{"-na"}, want: CLIFlags{ProjectTrustOverride: new(false)}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:385
		{name: "parses --verbose flag", args: []string{"--verbose"}, want: CLIFlags{Verbose: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:392
		{name: "parses --offline flag", args: []string{"--offline"}, want: CLIFlags{Offline: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:399
		{name: "parses regular mode", args: []string{"--tui-mode", "regular"}, want: CLIFlags{TuiMode: "regular"}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:399
		{name: "parses fullscreen mode", args: []string{"--tui-mode", "fullscreen"}, want: CLIFlags{TuiMode: "fullscreen"}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:404
		{name: "rejects invalid modes", args: []string{"--tui-mode", "other"}, want: CLIFlags{Diagnostics: []argDiagnostic{{Type: "error", Message: "Invalid TUI mode \"other\". Valid values: regular, fullscreen"}}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:411
		{name: "requires a mode", args: []string{"--tui-mode"}, want: CLIFlags{Diagnostics: []argDiagnostic{{Type: "error", Message: "--tui-mode requires regular or fullscreen"}}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:416
		{name: "does not recognize the old --ui-mode flag", args: []string{"--ui-mode", "fullscreen"}, want: CLIFlags{UnknownFlags: map[string]any{"ui-mode": "fullscreen"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:424
		{name: "parses --no-tools flag", args: []string{"--no-tools"}, want: CLIFlags{NoTools: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:429
		{name: "parses -nt shorthand", args: []string{"-nt"}, want: CLIFlags{NoTools: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:434
		{name: "parses --no-builtin-tools flag", args: []string{"--no-builtin-tools"}, want: CLIFlags{NoBuiltinTools: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:439
		{name: "parses -nbt shorthand", args: []string{"-nbt"}, want: CLIFlags{NoBuiltinTools: true}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:444
		{name: "parses --tools flag", args: []string{"--tools", "read,bash"}, want: CLIFlags{Tools: []string{"read", "bash"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:449
		{name: "parses -t shorthand", args: []string{"-t", "read,bash"}, want: CLIFlags{Tools: []string{"read", "bash"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:454
		{name: "parses --exclude-tools flag", args: []string{"--exclude-tools", "read,bash"}, want: CLIFlags{ExcludeTools: []string{"read", "bash"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:459
		{name: "parses -xt shorthand", args: []string{"-xt", "read,bash"}, want: CLIFlags{ExcludeTools: []string{"read", "bash"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:464
		{name: "parses --no-tools with explicit --tools flags", args: []string{"--no-tools", "--tools", "read,bash"}, want: CLIFlags{NoTools: true, Tools: []string{"read", "bash"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:470
		{name: "parses --no-builtin-tools with explicit --tools flags", args: []string{"--no-builtin-tools", "--tools", "read,bash"}, want: CLIFlags{NoBuiltinTools: true, Tools: []string{"read", "bash"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:478
		{name: "parses plain text messages", args: []string{"hello", "world"}, want: CLIFlags{Args: []string{"hello", "world"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:483
		{name: "parses @file arguments", args: []string{"@README.md", "@src/main.ts"}, want: CLIFlags{FileArgs: []string{"README.md", "src/main.ts"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:488
		{name: "parses mixed messages and file args", args: []string{"@file.txt", "explain this", "@image.png"}, want: CLIFlags{FileArgs: []string{"file.txt", "image.png"}, Args: []string{"explain this"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:494
		{name: "captures unknown long flags with string values", args: []string{"--unknown-flag", "message"}, want: CLIFlags{UnknownFlags: map[string]any{"unknown-flag": "message"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:500
		{name: "captures unknown boolean long flags", args: []string{"--unknown-flag"}, want: CLIFlags{UnknownFlags: map[string]any{"unknown-flag": true}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:505
		{name: "captures unknown long flags with equals syntax", args: []string{"--unknown-flag=value"}, want: CLIFlags{UnknownFlags: map[string]any{"unknown-flag": "value"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:512
		{name: "parses multiple flags together", args: []string{"--provider", "anthropic", "--model", "claude-sonnet", "--print", "--thinking", "high", "@prompt.md", "Do the task"}, want: CLIFlags{Provider: "anthropic", Model: "claude-sonnet", Print: " ", Thinking: "high", FileArgs: []string{"prompt.md"}, Args: []string{"Do the task"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.want.Mode == "" {
				tc.want.Mode = "text"
			}
			if tc.want.UnknownFlags == nil {
				tc.want.UnknownFlags = map[string]any{}
			}
			got := parseFlags(tc.args)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseFlags(%q) = %+v, want %+v", tc.args, got, tc.want)
			}
		})
	}
}
