package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestResolveProjectTrustedDefaultsToDeniedWithoutUI(t *testing.T) {
	project := trustProjectFixture(t)
	trusted, err := resolveProjectTrusted(context.Background(), projectTrustResolutionOptions{
		CWD: project, Store: codingagent.NewProjectTrustStore(t.TempDir()), Default: "ask", UI: extension.NoopUIContext,
	})
	if err != nil {
		t.Fatal(err)
	}
	if trusted {
		t.Fatal("unresolved non-interactive project was trusted")
	}
}

func TestResolveProjectTrustedHonorsOverrideBeforeProjectResources(t *testing.T) {
	project := trustProjectFixture(t)
	for _, trusted := range []bool{false, true} {
		got, err := resolveProjectTrusted(context.Background(), projectTrustResolutionOptions{
			CWD: project, Store: codingagent.NewProjectTrustStore(t.TempDir()), Override: new(trusted), Default: "ask",
		})
		if err != nil {
			t.Fatal(err)
		}
		if got != trusted {
			t.Fatalf("override %v resolved to %v", trusted, got)
		}
	}
}

func TestResolveProjectTrustedUsesFirstExtensionDecision(t *testing.T) {
	project := trustProjectFixture(t)
	ext := extension.Extension{
		Name: "trust", ResolvedPath: "/trust",
		Handlers: map[string][]extension.HandlerFn{
			"project_trust": {
				func(...any) (any, error) {
					return extension.ProjectTrustEventResult{Trusted: extension.ProjectTrustUndecided}, nil
				},
				func(...any) (any, error) {
					return extension.ProjectTrustEventResult{Trusted: extension.ProjectTrustYes, Remember: new(true)}, nil
				},
			},
		},
	}
	store := codingagent.NewProjectTrustStore(t.TempDir())
	trusted, err := resolveProjectTrusted(context.Background(), projectTrustResolutionOptions{
		CWD: project, Store: store, Default: "never", Runner: inproc.NewRunner([]extension.Extension{ext}, project), UI: extension.NoopUIContext,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !trusted {
		t.Fatal("decisive extension trust result was ignored")
	}
	stored, err := store.Get(project)
	if err != nil || stored == nil || !*stored {
		t.Fatalf("remembered extension decision = %v, err = %v", stored, err)
	}
}

func TestResolveProjectTrustedInteractiveSelectionPersists(t *testing.T) {
	project := trustProjectFixture(t)
	store := codingagent.NewProjectTrustStore(t.TempDir())
	ui := &projectTrustTestUI{UIContext: extension.NoopUIContext, selected: "Trust"}
	trusted, err := resolveProjectTrusted(context.Background(), projectTrustResolutionOptions{
		CWD: project, Store: store, Default: "ask", UI: ui,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !trusted {
		t.Fatal("interactive Trust selection was denied")
	}
	wantPrompt := "Trust project folder?\n" + project + "\n\nThis allows pig to load .pig settings and resources, install missing project packages, and execute project extensions."
	if ui.title != wantPrompt {
		t.Fatalf("trust prompt = %q, want %q", ui.title, wantPrompt)
	}
	stored, err := store.Get(project)
	if err != nil || stored == nil || !*stored {
		t.Fatalf("stored prompt decision = %v, err = %v", stored, err)
	}
}

// Pi 0.87.1 core/project-trust.ts:46-94 resolves overrides, resource presence, saved decisions and defaults before asking.
func TestResolveProjectTrustedPromptConditions(t *testing.T) {
	for _, tc := range []struct {
		name, fallback                     string
		override, saved                    *bool
		resources, wantTrusted, wantPrompt bool
	}{
		{name: "empty", fallback: "ask", wantTrusted: true},
		{name: "approve", resources: true, override: new(true), wantTrusted: true},
		{name: "deny", resources: true, override: new(false)},
		{name: "inherited trust", resources: true, saved: new(true), wantTrusted: true},
		{name: "inherited denial", resources: true, saved: new(false)},
		{name: "always", resources: true, fallback: "always", wantTrusted: true},
		{name: "never", resources: true, fallback: "never"},
		{name: "ask", resources: true, fallback: "ask", wantPrompt: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			cwd := filepath.Join(parent, "project")
			if err := os.MkdirAll(cwd, 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.resources {
				if err := os.MkdirAll(filepath.Join(cwd, codingagent.CONFIG_DIR_NAME, "skills"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			store := codingagent.NewProjectTrustStore(t.TempDir())
			if tc.saved != nil {
				if err := store.Set(parent, tc.saved); err != nil {
					t.Fatal(err)
				}
			}
			ui := &projectTrustTestUI{UIContext: extension.NoopUIContext, selected: "Do not trust (this session only)"}
			got, err := resolveProjectTrusted(t.Context(), projectTrustResolutionOptions{CWD: cwd, Store: store, Override: tc.override, Default: tc.fallback, UI: ui})
			if err != nil || got != tc.wantTrusted || (ui.title != "") != tc.wantPrompt {
				t.Fatalf("trusted=%v, prompt=%q, err=%v; want trusted=%v, prompt=%v", got, ui.title, err, tc.wantTrusted, tc.wantPrompt)
			}
		})
	}
}

// Pi 0.87.1 core/project-trust.ts:83-94 declines without UI; only InteractiveMode.renderInitialMessages emits the warning.
func TestProjectTrustNoninteractiveDoesNotPromptOrWarn(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	for _, mode := range []string{"print", "json", "rpc"} {
		t.Run(mode, func(t *testing.T) {
			cwd := trustProjectFixture(t)
			args := []string{"--offline", "--no-extensions", "--no-session", "--model", "openai/gpt-4.1"}
			if mode == "print" {
				args = append(args, "--print")
			} else {
				args = append(args, "--mode", mode)
			}
			cmd := exec.CommandContext(t.Context(), binary, args...)
			cmd.Dir = cwd
			cmd.Env = append(os.Environ(), "PIG_HOME="+t.TempDir(), "PIG_CODING_AGENT_DIR="+t.TempDir())
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s: %v\n%s", mode, err, output)
			}
			for _, forbidden := range []string{"Trust project folder?", "This project is not trusted."} {
				if strings.Contains(string(output), forbidden) {
					t.Errorf("%s emitted %q: %s", mode, forbidden, output)
				}
			}
		})
	}
}

type projectTrustTestUI struct {
	extension.UIContext
	selected string
	title    string
}

func (ui *projectTrustTestUI) Select(_ context.Context, title string, _ []string, _ extension.ExtensionUIDialogOptions) (string, error) {
	ui.title = title
	return ui.selected, nil
}

func trustProjectFixture(t *testing.T) string {
	t.Helper()
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, codingagent.CONFIG_DIR_NAME, "extensions"), 0o755); err != nil {
		t.Fatal(err)
	}
	return project
}
