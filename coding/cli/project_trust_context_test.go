package cli

// pi: packages/coding-agent/src/core/project-trust.ts

// pi: packages/coding-agent/src/cli/project-trust.ts

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// scriptedTrustUI is a UIContext that records the dialog calls the project trust context forwards.
type scriptedTrustUI struct {
	extension.UIContext
	calls []string
}

func (ui *scriptedTrustUI) Select(_ context.Context, title string, _ []string, _ extension.ExtensionUIDialogOptions) (string, error) {
	ui.calls = append(ui.calls, "select:"+title)
	return "picked", nil
}

func (ui *scriptedTrustUI) Confirm(_ context.Context, title, message string, _ extension.ExtensionUIDialogOptions) (bool, error) {
	ui.calls = append(ui.calls, "confirm:"+title+"/"+message)
	return true, nil
}

func (ui *scriptedTrustUI) Input(_ context.Context, title, placeholder string, _ extension.ExtensionUIDialogOptions) (string, error) {
	ui.calls = append(ui.calls, "input:"+title+"/"+placeholder)
	return "typed", nil
}

func (ui *scriptedTrustUI) Notify(message, kind string) {
	ui.calls = append(ui.calls, "notify:"+kind+":"+message)
}

// cli/project-trust.ts:7-58 createProjectTrustContext: mode maps "interactive" to "tui"; dialogs prompt only with hasUI in an
// interactive run (select and input answer undefined, confirm false otherwise); notify prints chalk-colored text to stderr outside
// interactive mode and does nothing in an interactive run (cli/project-trust.ts:54-59); a replacement's live UI gets every call
// (interactive-mode.ts:2609-2621).
func TestProjectTrustContextFollowsCreateProjectTrustContext(t *testing.T) {
	cases := []struct {
		name       string
		mode       appMode
		hasUI      bool
		live       bool
		wantMode   extension.ExtensionMode
		prompts    bool
		wantStderr string
	}{
		{"interactive with UI", appModeInteractive, true, false, extension.ModeTUI, true, ""},
		{"interactive without UI", appModeInteractive, false, false, extension.ModeTUI, false, ""},
		{"live UI of an interactive replacement", appModeInteractive, true, true, extension.ModeTUI, true, ""},
		{"print", appModePrint, false, false, extension.ModePrint, false, "\x1b[36minfo text\x1b[39m\n\x1b[33mwarn text\x1b[39m\n\x1b[31merr text\x1b[39m\n"},
		{"json", appModeJSON, false, false, extension.ModeJSON, false, "\x1b[36minfo text\x1b[39m\n\x1b[33mwarn text\x1b[39m\n\x1b[31merr text\x1b[39m\n"},
		{"rpc claiming a UI still never prompts", appModeRPC, true, false, extension.ModeRPC, false, "\x1b[36minfo text\x1b[39m\n\x1b[33mwarn text\x1b[39m\n\x1b[31merr text\x1b[39m\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ui := &scriptedTrustUI{UIContext: extension.NoopUIContext}
			var stderr bytes.Buffer
			trust := buildProjectTrustContext("/project", tc.mode, tc.hasUI, ui, tc.live, &stderr, true)
			if trust.Cwd != "/project" || trust.Mode != tc.wantMode || trust.HasUI != tc.hasUI {
				t.Fatalf("context = %+v, want cwd /project mode %s hasUI %v", trust, tc.wantMode, tc.hasUI)
			}
			selected, _ := trust.UI.Select(t.Context(), "pick", []string{"a"}, extension.ExtensionUIDialogOptions{})
			confirmed, _ := trust.UI.Confirm(t.Context(), "sure", "really", extension.ExtensionUIDialogOptions{})
			typed, _ := trust.UI.Input(t.Context(), "name", "ph", extension.ExtensionUIDialogOptions{})
			trust.UI.Notify("info text", "info")
			trust.UI.Notify("warn text", "warning")
			trust.UI.Notify("err text", "error")
			wantCalls := []string(nil)
			if tc.prompts {
				if selected != "picked" || !confirmed || typed != "typed" {
					t.Fatalf("answers = %q %v %q, want the UI's", selected, confirmed, typed)
				}
				wantCalls = []string{"select:pick", "confirm:sure/really", "input:name/ph"}
			} else if selected != "" || confirmed || typed != "" {
				t.Fatalf("answers = %q %v %q, want undefined, false, undefined", selected, confirmed, typed)
			}
			if tc.live {
				wantCalls = append(wantCalls, "notify:info:info text", "notify:warning:warn text", "notify:error:err text")
			}
			if !slices.Equal(ui.calls, wantCalls) {
				t.Fatalf("UI calls = %q, want %q", ui.calls, wantCalls)
			}
			if stderr.String() != tc.wantStderr {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.wantStderr)
			}
		})
	}
}

// chalk at color level 0 writes the bare message.
func TestProjectTrustContextNotifyIsPlainWithoutColor(t *testing.T) {
	var stderr bytes.Buffer
	trust := buildProjectTrustContext("/project", appModePrint, false, nil, false, &stderr, false)
	trust.UI.Notify("plain", "error")
	if stderr.String() != "plain\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// resolveProjectTrusted hands project_trust handlers the context of the run: the folder being decided and the run's mode.
func TestResolveProjectTrustedPassesTheContextToHandlers(t *testing.T) {
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(codingagent.ProjectConfigDir(cwd), "extensions"), 0o755); err != nil {
		t.Fatal(err)
	}
	var got extension.ProjectTrustContext
	runner := inproc.NewRunner([]extension.Extension{{Path: "ext", Handlers: map[string][]extension.HandlerFn{"project_trust": {func(args ...any) (any, error) {
		got, _ = args[2].(extension.ProjectTrustContext)
		return extension.ProjectTrustEventResult{Trusted: extension.ProjectTrustYes}, nil
	}}}}}, cwd)
	trusted, err := resolveProjectTrusted(t.Context(), projectTrustResolutionOptions{CWD: cwd, Store: codingagent.NewProjectTrustStore(filepath.Join(t.TempDir(), "trust")), Runner: runner, Mode: appModeJSON})
	if err != nil || !trusted {
		t.Fatalf("trusted = %v, err = %v", trusted, err)
	}
	if got.Cwd != cwd || got.Mode != extension.ModeJSON || got.HasUI || got.UI == nil {
		t.Fatalf("handler context = %+v, want cwd %s, mode json, no UI", got, cwd)
	}
}

func TestProjectTrustPromptMode(t *testing.T) {
	cases := []struct {
		name                string
		mode                appMode
		helpOrList, startUI bool
		liveUI              bool
		wantMode            appMode
		wantHasUI           bool
		wantLive            bool
	}{
		{"interactive startup", appModeInteractive, false, true, false, appModeInteractive, true, false},
		{"interactive startup keeps the startup UI over a live one", appModeInteractive, false, true, true, appModeInteractive, true, false},
		{"--help prompts as print (main.ts:723)", appModeInteractive, true, false, false, appModePrint, false, false},
		{"--list-models under rpc prompts as print", appModeRPC, true, false, false, appModePrint, false, false},
		{"json run keeps its mode without a UI (main.ts:768-770)", appModeJSON, false, false, false, appModeJSON, false, false},
		{"a replacement's live UI is interactive with a UI", appModePrint, false, false, true, appModeInteractive, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, hasUI, live := projectTrustPromptMode(tc.mode, tc.helpOrList, tc.startUI, tc.liveUI)
			if mode != tc.wantMode || hasUI != tc.wantHasUI || live != tc.wantLive {
				t.Fatalf("got (%s, %v, %v), want (%s, %v, %v)", mode, hasUI, live, tc.wantMode, tc.wantHasUI, tc.wantLive)
			}
		})
	}
}

// dismissingTrustUI is the startup trust UI after the user closes the selector or the input: it reports context.Canceled.
type dismissingTrustUI struct{ extension.UIContext }

func (dismissingTrustUI) Select(context.Context, string, []string, extension.ExtensionUIDialogOptions) (string, error) {
	return "", context.Canceled
}

func (dismissingTrustUI) Input(context.Context, string, string, extension.ExtensionUIDialogOptions) (string, error) {
	return "", context.Canceled
}

// A dismissed startup dialog answers undefined: showStartupSelector and showStartupInput resolve undefined on cancel (startup-ui.ts:154-172)
// and createProjectTrustContext returns that (cli/project-trust.ts:18-28,45-52), so the handler sees no answer and no error.
func TestProjectTrustContextDismissedStartupDialogAnswersUndefined(t *testing.T) {
	trust := buildProjectTrustContext("/project", appModeInteractive, true, dismissingTrustUI{extension.NoopUIContext}, false, &bytes.Buffer{}, false)
	if selected, err := trust.UI.Select(t.Context(), "pick", []string{"a"}, extension.ExtensionUIDialogOptions{}); selected != "" || err != nil {
		t.Fatalf("Select = %q, %v; want undefined", selected, err)
	}
	if typed, err := trust.UI.Input(t.Context(), "name", "ph", extension.ExtensionUIDialogOptions{}); typed != "" || err != nil {
		t.Fatalf("Input = %q, %v; want undefined", typed, err)
	}
}
