package cli

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
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

// trust-manager.ts TRUST_REQUIRING_PROJECT_CONFIG_RESOURCES lists mcp.json: a project whose only config is `<config dir>/mcp.json` (stdio servers run
// commands) must go through the trust decision, and without a UI or a stored decision it stays untrusted.
func TestResolveProjectTrustedTreatsAProjectMcpJSONAsUntrustedUntilDecided(t *testing.T) {
	project := t.TempDir()
	configDir := filepath.Join(project, codingagent.CONFIG_DIR_NAME)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "mcp.json"), []byte(`{"mcpServers":{"x":{"command":"true"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	trusted, err := resolveProjectTrusted(context.Background(), projectTrustResolutionOptions{
		CWD: project, Store: codingagent.NewProjectTrustStore(t.TempDir()), Default: "ask", UI: extension.NoopUIContext,
	})
	if err != nil {
		t.Fatal(err)
	}
	if trusted {
		t.Fatal("a project with only a project mcp.json was trusted without a decision")
	}
}

func TestTrustPromptNamesTheShadowedWorkspacePiglet(t *testing.T) {
	project := trustProjectFixture(t)
	ui := &projectTrustTestUI{UIContext: extension.NoopUIContext, selected: "No"}
	_, err := resolveProjectTrusted(context.Background(), projectTrustResolutionOptions{
		CWD: project, Store: codingagent.NewProjectTrustStore(t.TempDir()), Default: "ask", UI: ui, HasUI: true, ShadowedPiglet: "demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ui.title, `Piglet "demo"`) || !strings.Contains(ui.title, "not used for this run") {
		t.Fatalf("trust prompt does not name the shadowed Piglet:\n%s", ui.title)
	}
	ui.title = ""
	if _, err := resolveProjectTrusted(context.Background(), projectTrustResolutionOptions{
		CWD: project, Store: codingagent.NewProjectTrustStore(t.TempDir()), Default: "ask", UI: ui, HasUI: true,
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ui.title, "defines the Piglet") {
		t.Fatalf("trust prompt names a Piglet when none is shadowed:\n%s", ui.title)
	}
}

// Port of Pi main 642a705e11c6f2a3baafc76fb15399fd2187fd43: built-in project trust prompt passes blockedKind "permission".
func TestResolveProjectTrustedPassesPermissionBlockedKind(t *testing.T) {
	var gotOpts extension.ExtensionUIDialogOptions
	recUI := &recordingTrustSelectUI{
		UIContext: extension.NoopUIContext,
		onSelect: func(_ string, options []string, opts extension.ExtensionUIDialogOptions) string {
			gotOpts = opts
			return options[0]
		},
	}
	projectDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectDir, codingagent.ConfigDirName()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, codingagent.ConfigDirName(), "settings.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := codingagent.NewProjectTrustStore(t.TempDir())
	trusted, err := resolveProjectTrusted(context.Background(), projectTrustResolutionOptions{
		CWD:     projectDir,
		Store:   store,
		Default: "ask",
		UI:      recUI,
	})
	if err != nil || !trusted {
		t.Fatalf("trusted=%v err=%v", trusted, err)
	}
	if gotOpts.BlockedKind != tui.ProgramStatusKindPermission {
		t.Fatalf("BlockedKind = %q, want %q", gotOpts.BlockedKind, tui.ProgramStatusKindPermission)
	}
}

type recordingTrustSelectUI struct {
	extension.UIContext
	onSelect func(title string, options []string, opts extension.ExtensionUIDialogOptions) string
}

func (u *recordingTrustSelectUI) Select(_ context.Context, title string, options []string, opts extension.ExtensionUIDialogOptions) (string, error) {
	return u.onSelect(title, options, opts), nil
}

type cliStatusStartupTerminal struct {
	keys    []string
	reports []tui.ProgramStatus
	stopped bool
}

func (s *cliStatusStartupTerminal) StartWithReadError(input func([]byte), _ func(), _ func(error)) error {
	for _, key := range s.keys {
		input([]byte(key))
	}
	return nil
}

func (s *cliStatusStartupTerminal) Stop()        { s.stopped = true }
func (s *cliStatusStartupTerminal) Write(string) {}
func (s *cliStatusStartupTerminal) SetProgramStatus(status tui.ProgramStatus) {
	s.reports = append(s.reports, status)
}

// Port of Pi main 27a40ea2a0f58246fa9793a752215bad7d2cecaf & 642a705e11c6f2a3baafc76fb15399fd2187fd43
// (packages/coding-agent/test/startup-ui-program-status.test.ts): exercises startupTrustUI -> ShowStartupSelector / ShowStartupInput.
func TestStartupTrustUIProgramStatusForwarding(t *testing.T) {
	t.Run("reports a project trust confirm as a permission request", func(t *testing.T) {
		term := &cliStatusStartupTerminal{keys: []string{"\r"}}
		ui := newStartupTrustUI(codingagent.StartupUIOptions{AgentDir: t.TempDir(), Terminal: term, Output: io.Discard})
		ctx := newProjectTrustContext("/work/project", appModeInteractive, true, ui, false)
		confirmed, err := ctx.UI.Confirm(context.Background(), "Load project extensions?", "They run with your permissions.", extension.ExtensionUIDialogOptions{})
		if err != nil || !confirmed {
			t.Fatalf("confirmed=%v err=%v", confirmed, err)
		}
		want := []tui.ProgramStatus{{App: codingagent.AppName, State: tui.ProgramStateBlocked, Kind: tui.ProgramStatusKindPermission, Message: "Load project extensions?"}}
		if !reflect.DeepEqual(term.reports, want) {
			t.Fatalf("reports = %+v, want %+v", term.reports, want)
		}
		if !term.stopped {
			t.Fatal("terminal was not stopped")
		}
	})

	t.Run("reports the kind an extension passes to a project trust select", func(t *testing.T) {
		term := &cliStatusStartupTerminal{keys: []string{"\r"}}
		ui := newStartupTrustUI(codingagent.StartupUIOptions{AgentDir: t.TempDir(), Terminal: term, Output: io.Discard})
		ctx := newProjectTrustContext("/work/project", appModeInteractive, true, ui, false)
		choice, err := ctx.UI.Select(context.Background(), "Run project hooks?", []string{"Run", "Skip"}, extension.ExtensionUIDialogOptions{BlockedKind: tui.ProgramStatusKindPermission})
		if err != nil || choice != "Run" {
			t.Fatalf("choice=%q err=%v", choice, err)
		}
		want := []tui.ProgramStatus{{App: codingagent.AppName, State: tui.ProgramStateBlocked, Kind: tui.ProgramStatusKindPermission, Message: "Run project hooks?"}}
		if !reflect.DeepEqual(term.reports, want) {
			t.Fatalf("reports = %+v, want %+v", term.reports, want)
		}
	})

	t.Run("reports built-in resolveProjectTrusted prompt as permission request", func(t *testing.T) {
		projectDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(projectDir, codingagent.ConfigDirName()), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(projectDir, codingagent.ConfigDirName(), "settings.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		term := &cliStatusStartupTerminal{keys: []string{"\r"}}
		ui := newStartupTrustUI(codingagent.StartupUIOptions{AgentDir: t.TempDir(), Terminal: term, Output: io.Discard})
		trusted, err := resolveProjectTrusted(context.Background(), projectTrustResolutionOptions{
			CWD:     projectDir,
			Store:   codingagent.NewProjectTrustStore(t.TempDir()),
			Default: "ask",
			UI:      ui,
		})
		if err != nil || !trusted {
			t.Fatalf("trusted=%v err=%v", trusted, err)
		}
		want := []tui.ProgramStatus{{App: codingagent.AppName, State: tui.ProgramStateBlocked, Kind: tui.ProgramStatusKindPermission, Message: "Trust project folder?"}}
		if !reflect.DeepEqual(term.reports, want) {
			t.Fatalf("reports = %+v, want %+v", term.reports, want)
		}
	})

	t.Run("reports startup input blockedKind forwarded through startupTrustUI", func(t *testing.T) {
		term := &cliStatusStartupTerminal{keys: []string{"secret", "\r"}}
		ui := newStartupTrustUI(codingagent.StartupUIOptions{AgentDir: t.TempDir(), Terminal: term, Output: io.Discard})
		ctx := newProjectTrustContext("/work/project", appModeInteractive, true, ui, false)
		val, err := ctx.UI.Input(context.Background(), "Enter token", "", extension.ExtensionUIDialogOptions{BlockedKind: tui.ProgramStatusKindAuth})
		if err != nil || val != "secret" {
			t.Fatalf("val=%q err=%v", val, err)
		}
		want := []tui.ProgramStatus{{App: codingagent.AppName, State: tui.ProgramStateBlocked, Kind: tui.ProgramStatusKindAuth, Message: "Enter token"}}
		if !reflect.DeepEqual(term.reports, want) {
			t.Fatalf("reports = %+v, want %+v", term.reports, want)
		}
	})
}
