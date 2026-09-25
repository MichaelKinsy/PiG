package main

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestParseFlags(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		check func(t *testing.T, f CLIFlags)
	}{
		{
			name: "empty",
			args: nil,
			check: func(t *testing.T, f CLIFlags) {
				if f.Version || f.Help || f.Model != "" || f.Print != "" ||
					f.NoExtensions || f.Continue || f.Verbose ||
					f.ResumeAny ||
					len(f.Extensions) != 0 || len(f.Skills) != 0 ||
					len(f.Args) != 0 {
					t.Error("expected zero defaults")
				}
			},
		},
		{
			name: "version-long",
			args: []string{"--version"},
			check: func(t *testing.T, f CLIFlags) {
				if !f.Version {
					t.Error("expected Version=true")
				}
			},
		},
		{
			name: "version-short",
			args: []string{"-v"},
			check: func(t *testing.T, f CLIFlags) {
				if !f.Version {
					t.Error("expected Version=true")
				}
			},
		},
		{
			name: "help-long",
			args: []string{"--help"},
			check: func(t *testing.T, f CLIFlags) {
				if !f.Help {
					t.Error("expected Help=true")
				}
			},
		},
		{
			name: "help-short",
			args: []string{"-h"},
			check: func(t *testing.T, f CLIFlags) {
				if !f.Help {
					t.Error("expected Help=true")
				}
			},
		},
		{
			name: "model",
			args: []string{"--model", "github-copilot/gpt-4o"},
			check: func(t *testing.T, f CLIFlags) {
				if f.Model != "github-copilot/gpt-4o" {
					t.Errorf("Model = %q, want github-copilot/gpt-4o", f.Model)
				}
			},
		},
		{
			name: "print",
			args: []string{"--print", "hello world"},
			check: func(t *testing.T, f CLIFlags) {
				// --print is a bare boolean (upstream semantics).
				// "hello world" becomes a positional arg, not the Print value.
				if f.Print != " " {
					t.Errorf("Print = %q, want sentinel %q", f.Print, " ")
				}
				if len(f.Args) == 0 || f.Args[0] != "hello world" {
					t.Errorf("Args = %v, want [hello world]", f.Args)
				}
			},
		},
		{
			name: "no-extensions",
			args: []string{"--no-extensions"},
			check: func(t *testing.T, f CLIFlags) {
				if !f.NoExtensions {
					t.Error("expected NoExtensions=true")
				}
			},
		},
		{
			name: "export",
			args: []string{"--export", "session.jsonl", "out.html"},
			check: func(t *testing.T, f CLIFlags) {
				if f.Export != "session.jsonl" {
					t.Errorf("Export = %q, want session.jsonl", f.Export)
				}
				if !slices.Equal(f.Args, []string{"out.html"}) {
					t.Errorf("Args = %v, want [out.html]", f.Args)
				}
			},
		},
		{
			name: "extension-single",
			args: []string{"-e", "/path/to/ext.ts"},
			check: func(t *testing.T, f CLIFlags) {
				want := []string{"/path/to/ext.ts"}
				if !slices.Equal(f.Extensions, want) {
					t.Errorf("Extensions = %v, want %v", f.Extensions, want)
				}
			},
		},
		{
			name: "extension-multiple",
			args: []string{"-e", "ext1", "-e", "ext2"},
			check: func(t *testing.T, f CLIFlags) {
				want := []string{"ext1", "ext2"}
				if !slices.Equal(f.Extensions, want) {
					t.Errorf("Extensions = %v, want %v", f.Extensions, want)
				}
			},
		},
		{
			name: "skill",
			args: []string{"--skill", "github"},
			check: func(t *testing.T, f CLIFlags) {
				want := []string{"github"}
				if !slices.Equal(f.Skills, want) {
					t.Errorf("Skills = %v, want %v", f.Skills, want)
				}
			},
		},
		{
			name: "resume-does-not-consume-positional",
			args: []string{"--resume", "session-123"},
			check: func(t *testing.T, f CLIFlags) {
				if !f.ResumeAny {
					t.Error("expected ResumeAny=true")
				}
				if !slices.Equal(f.Args, []string{"session-123"}) {
					t.Errorf("Args = %v, want positional session-123", f.Args)
				}
			},
		},
		{
			name: "resume-bare",
			args: []string{"--resume"},
			check: func(t *testing.T, f CLIFlags) {
				if !f.ResumeAny {
					t.Error("expected ResumeAny=true")
				}
			},
		},
		{
			name: "resume-before-flag",
			args: []string{"--resume", "--model", "gpt-4o"},
			check: func(t *testing.T, f CLIFlags) {
				if !f.ResumeAny {
					t.Error("expected ResumeAny=true")
				}
				if f.Model != "gpt-4o" {
					t.Errorf("Model = %q, want gpt-4o", f.Model)
				}
			},
		},
		{
			name: "continue",
			args: []string{"--continue"},
			check: func(t *testing.T, f CLIFlags) {
				if !f.Continue {
					t.Error("expected Continue=true")
				}
			},
		},
		{
			name: "mode-rpc",
			args: []string{"--mode", "rpc"},
			check: func(t *testing.T, f CLIFlags) {
				if f.Mode != "rpc" {
					t.Errorf("Mode = %q, want rpc", f.Mode)
				}
			},
		},
		{
			name: "verbose",
			args: []string{"--verbose"},
			check: func(t *testing.T, f CLIFlags) {
				if !f.Verbose {
					t.Error("expected Verbose=true")
				}
			},
		},
		{
			name: "trailing-args",
			args: []string{"hello", "world"},
			check: func(t *testing.T, f CLIFlags) {
				want := []string{"hello", "world"}
				if !slices.Equal(f.Args, want) {
					t.Errorf("Args = %v, want %v", f.Args, want)
				}
			},
		},
		{
			name: "combined",
			args: []string{"--model", "gpt-4o", "--no-extensions", "hello"},
			check: func(t *testing.T, f CLIFlags) {
				if f.Model != "gpt-4o" {
					t.Errorf("Model = %q", f.Model)
				}
				if !f.NoExtensions {
					t.Error("expected NoExtensions=true")
				}
				if !slices.Equal(f.Args, []string{"hello"}) {
					t.Errorf("Args = %v", f.Args)
				}
			},
		},
		{
			name: "unknown-flag-skipped",
			args: []string{"--unknown-flag", "value", "hello"},
			check: func(t *testing.T, f CLIFlags) {
				want := []string{"hello"}
				if !slices.Equal(f.Args, want) {
					t.Errorf("Args = %v, want %v", f.Args, want)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := parseFlags(tc.args)
			tc.check(t, f)
		})
	}
}

func TestHelpTextMentionsPiglets(t *testing.T) {
	help := captureStdout(func() { printHelp(os.Stdout, false) })
	for _, want := range []string{"--piglet <name|path>", "pig piglet <command>"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help text missing %q:\n%s", want, help)
		}
	}
	for _, removed := range []string{"  --piglets", "  pig piglets"} {
		if strings.Contains(help, removed) {
			t.Fatalf("help text contains removed piglet syntax %q:\n%s", removed, help)
		}
	}
}

func TestHelpTextUsesPigSelfUpdateTargets(t *testing.T) {
	help := captureStdout(func() { printHelp(os.Stdout, false) })
	if !strings.Contains(help, "pig update [source|self]") || strings.Contains(help, "source|self|pi") {
		t.Fatalf("help has stale self-update targets:\n%s", help)
	}
}

func TestHelpTextMentionsProviderEnvVars(t *testing.T) {
	help := captureStdout(func() { printHelp(os.Stdout, false) })
	for _, envVar := range []string{
		"TOGETHER_API_KEY",
		"XIAOMI_API_KEY",
		"XIAOMI_TOKEN_PLAN_CN_API_KEY",
		"XIAOMI_TOKEN_PLAN_AMS_API_KEY",
		"XIAOMI_TOKEN_PLAN_SGP_API_KEY",
	} {
		if !strings.Contains(help, envVar) {
			t.Fatalf("help text missing %s env var:\n%s", envVar, help)
		}
	}
}

func captureStdout(fn func()) string {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		panic(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	readDone := drainPipe(r)
	fn()
	_ = w.Close()
	result := <-readDone
	_ = r.Close()
	if result.err != nil {
		panic(result.err)
	}
	return string(result.data)
}

func TestParseFlags_UpstreamParity(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		check func(t *testing.T, f CLIFlags)
	}{
		{"--provider", []string{"--provider", "openai"}, func(t *testing.T, f CLIFlags) {
			if f.Provider != "openai" {
				t.Errorf("Provider = %q", f.Provider)
			}
		}},
		{"--system-prompt", []string{"--system-prompt", "be brief"}, func(t *testing.T, f CLIFlags) {
			if f.SystemPrompt != "be brief" {
				t.Errorf("SystemPrompt = %q", f.SystemPrompt)
			}
		}},
		{"--append-system-prompt repeatable", []string{"--append-system-prompt", "a", "--append-system-prompt", "b"}, func(t *testing.T, f CLIFlags) {
			if len(f.AppendSystemPrompt) != 2 {
				t.Errorf("len = %d", len(f.AppendSystemPrompt))
			}
		}},
		{"--thinking valid", []string{"--thinking", "high"}, func(t *testing.T, f CLIFlags) {
			if f.Thinking != "high" {
				t.Errorf("Thinking = %q", f.Thinking)
			}
		}},
		{"--thinking invalid", []string{"--thinking", "bogus"}, func(t *testing.T, f CLIFlags) {
			if f.Thinking != "" {
				t.Errorf("should be empty, got %q", f.Thinking)
			}
		}},
		{"--no-session", []string{"--no-session"}, func(t *testing.T, f CLIFlags) {
			if !f.NoSession {
				t.Error("NoSession should be true")
			}
		}},
		{"--session", []string{"--session", "abc123"}, func(t *testing.T, f CLIFlags) {
			if f.Session != "abc123" {
				t.Errorf("Session = %q", f.Session)
			}
		}},
		{"--fork", []string{"--fork", "def456"}, func(t *testing.T, f CLIFlags) {
			if f.Fork != "def456" {
				t.Errorf("Fork = %q", f.Fork)
			}
		}},
		{"--no-tools", []string{"--no-tools"}, func(t *testing.T, f CLIFlags) {
			if !f.NoTools {
				t.Error("NoTools should be true")
			}
		}},
		{"-nt shorthand", []string{"-nt"}, func(t *testing.T, f CLIFlags) {
			if !f.NoTools {
				t.Error("NoTools should be true")
			}
		}},
		{"--name", []string{"--name", "My Session"}, func(t *testing.T, f CLIFlags) {
			if f.Name != "My Session" {
				t.Errorf("Name = %q, want %q", f.Name, "My Session")
			}
		}},
		{"-n shorthand", []string{"-n", "quick"}, func(t *testing.T, f CLIFlags) {
			if f.Name != "quick" {
				t.Errorf("Name = %q, want %q", f.Name, "quick")
			}
		}},
		{"--no-builtin-tools", []string{"--no-builtin-tools"}, func(t *testing.T, f CLIFlags) {
			if !f.NoBuiltinTools {
				t.Error("NoBuiltinTools should be true")
			}
		}},
		{"-nbt shorthand", []string{"-nbt"}, func(t *testing.T, f CLIFlags) {
			if !f.NoBuiltinTools {
				t.Error("NoBuiltinTools should be true")
			}
		}},
		{"--tools csv", []string{"--tools", "read,bash,grep"}, func(t *testing.T, f CLIFlags) {
			if len(f.Tools) != 3 {
				t.Errorf("len(Tools) = %d", len(f.Tools))
			}
			if f.Tools[0] != "read" {
				t.Errorf("Tools[0] = %q", f.Tools[0])
			}
		}},
		{"-t shorthand", []string{"-t", "read,bash,grep"}, func(t *testing.T, f CLIFlags) {
			want := []string{"read", "bash", "grep"}
			if !slices.Equal(f.Tools, want) {
				t.Errorf("Tools = %v, want %v", f.Tools, want)
			}
		}},
		{"--list-models bare", []string{"--list-models"}, func(t *testing.T, f CLIFlags) {
			if !f.ListModelsAll {
				t.Error("ListModelsAll should be true")
			}
		}},
		{"--list-models with search", []string{"--list-models", "gpt"}, func(t *testing.T, f CLIFlags) {
			if f.ListModels != "gpt" {
				t.Errorf("ListModels = %q", f.ListModels)
			}
		}},
		{"--extension/-e", []string{"-e", "a.ts", "--extension", "b.ts"}, func(t *testing.T, f CLIFlags) {
			if len(f.Extensions) != 2 {
				t.Errorf("len = %d", len(f.Extensions))
			}
		}},
		{"--no-skills/-ns", []string{"-ns"}, func(t *testing.T, f CLIFlags) {
			if !f.NoSkills {
				t.Error("NoSkills should be true")
			}
		}},
		{"--no-context-files/-nc", []string{"-nc"}, func(t *testing.T, f CLIFlags) {
			if !f.NoContextFiles {
				t.Error("NoContextFiles should be true")
			}
		}},
		{"--offline", []string{"--offline"}, func(t *testing.T, f CLIFlags) {
			if !f.Offline {
				t.Error("Offline should be true")
			}
		}},
		{"--approve/-a", []string{"-a"}, func(t *testing.T, f CLIFlags) {
			if f.ProjectTrustOverride == nil || !*f.ProjectTrustOverride {
				t.Errorf("ProjectTrustOverride = %v, want true", f.ProjectTrustOverride)
			}
		}},
		{"--no-approve/-na", []string{"-na"}, func(t *testing.T, f CLIFlags) {
			if f.ProjectTrustOverride == nil || *f.ProjectTrustOverride {
				t.Errorf("ProjectTrustOverride = %v, want false", f.ProjectTrustOverride)
			}
		}},
		{"--models csv", []string{"--models", "sonnet,haiku,gpt-4o"}, func(t *testing.T, f CLIFlags) {
			if len(f.Models) != 3 {
				t.Errorf("len(Models) = %d", len(f.Models))
			}
		}},
		{"@file args", []string{"@prompt.md", "@image.png", "hello"}, func(t *testing.T, f CLIFlags) {
			if len(f.FileArgs) != 2 {
				t.Errorf("len(FileArgs) = %d", len(f.FileArgs))
			}
			if f.FileArgs[0] != "prompt.md" {
				t.Errorf("FileArgs[0] = %q", f.FileArgs[0])
			}
			if len(f.Args) != 1 {
				t.Errorf("len(Args) = %d", len(f.Args))
			}
		}},
		{"-c shorthand", []string{"-c"}, func(t *testing.T, f CLIFlags) {
			if !f.Continue {
				t.Error("Continue should be true")
			}
		}},
		{"-r shorthand", []string{"-r"}, func(t *testing.T, f CLIFlags) {
			if !f.ResumeAny {
				t.Error("ResumeAny should be true")
			}
		}},
		{"-p bare", []string{"-p"}, func(t *testing.T, f CLIFlags) {
			if f.Print == "" {
				t.Error("Print should be set")
			}
		}},
		{"-p with prompt", []string{"-p", "hello"}, func(t *testing.T, f CLIFlags) {
			// -p is bare boolean; "hello" becomes positional arg
			if f.Print != " " {
				t.Errorf("Print = %q, want sentinel", f.Print)
			}
			if len(f.Args) == 0 || f.Args[0] != "hello" {
				t.Errorf("Args = %v, want [hello]", f.Args)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := parseFlags(tc.args)
			tc.check(t, f)
		})
	}
}

// PiG's /share returns the gateway's canonical URL (D64), so Pi's
// PI_SHARE_VIEWER_URL has no effect and --help must not advertise it or its
// pi.dev default.
func TestHelpOmitsUnusedShareViewerURL(t *testing.T) {
	var out strings.Builder
	printHelp(&out, false)
	if strings.Contains(out.String(), "PI_SHARE_VIEWER_URL") || strings.Contains(out.String(), "pi.dev") {
		t.Fatalf("help advertises the unused share viewer URL:\n%s", out.String())
	}
}
