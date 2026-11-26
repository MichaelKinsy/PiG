package codingagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/configvalue"
	"github.com/MichaelKinsy/PiG/tui"
)

// H2 regression: userBashResultOverride must preserve fullOutputError so interactive
// mode passes it to both SetCompleteWithOutputAndError and RecordUserBashResult.
func TestUserBashResultOverridePreservesFullOutputError(t *testing.T) {
	exit := 0
	override := map[string]any{
		"output":          "tail\n",
		"exitCode":        exit,
		"cancelled":       false,
		"truncated":       true,
		"fullOutputError": "ENOSPC: no space left on device, write",
	}
	res := userBashResultOverride(override)
	if !res.Truncated || res.FullOutputPath != "" || res.FullOutputError != "ENOSPC: no space left on device, write" {
		t.Fatalf("userBashResultOverride = %+v, want FullOutputError preserved", res)
	}

	// Verify the interactive block renders the error when completed with SetCompleteWithOutputAndError.
	block := tui.NewBashExecutionComponent("seq 3000", nil, false, 1)
	block.SetCompleteWithOutputAndError(res.ExitCode, res.Cancelled, res.Truncated, res.Output, res.FullOutputPath, res.FullOutputError)
	rendered := strings.Join(strings.Fields(strings.Join(block.Render(120), " ")), " ")
	if !strings.Contains(rendered, "Output truncated. Could not save the full output: ENOSPC: no space left on device, write") {
		t.Fatalf("rendered block missing fullOutputError warning: %q", rendered)
	}
}

// M1 (Pi 943a99e3c external-editor.test.ts:65): opening external editor against an unwritable/missing
// temp dir returns *ExternalEditorFailure with Node-style ENOENT message instead of panicking.
func TestOpenExternalEditorReportsMissingTempDirUpstream(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-editor-tmp", "tmp")
	t.Setenv("TMPDIR", missing)
	t.Setenv("TEMP", missing)
	t.Setenv("TMP", missing)

	got, err := OpenExternalEditor(t.Context(), "original", "true")
	if got != "original" {
		t.Fatalf("content = %q, want %q", got, "original")
	}
	var failure *ExternalEditorFailure
	if !errors.As(err, &failure) {
		t.Fatalf("err = %T (%v), want *ExternalEditorFailure", err, err)
	}
	pattern := regexp.MustCompile(`^External editor failed: ENOENT.*; prompt unchanged$`)
	if !pattern.MatchString(failure.Message) {
		t.Fatalf("failure.Message = %q, want match %s", failure.Message, pattern)
	}
}

// M4 regression (Pi 713563870): /login selector shows "stored key did not resolve" for a failing
// !command stored credential and does NOT spawn uncached !commands synchronously on the selector path.
func TestLoginProviderOptionsShowsUnresolvedStoredKeyWithoutRunningUncachedCommand(t *testing.T) {
	agentDir := t.TempDir()
	authPath := filepath.Join(agentDir, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"deepseek":{"type":"api_key","key":"!test-never-run-on-login-selector"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var commandCalls atomic.Int32
	restore := configvalue.SetExecutorForTest(func(_ context.Context, payload string) (string, bool) {
		if payload == "test-never-run-on-login-selector" {
			commandCalls.Add(1)
		}
		return "", false
	})
	t.Cleanup(restore)
	configvalue.ClearCacheEntry("!test-never-run-on-login-selector")
	t.Cleanup(func() { configvalue.ClearCacheEntry("!test-never-run-on-login-selector") })

	m := NewInteractiveMode(nil, InteractiveModeOptions{AgentDir: agentDir})
	options := m.getLoginProviderOptions()
	if got := commandCalls.Load(); got != 0 {
		t.Fatalf("getLoginProviderOptions spawned !command %d times, want 0", got)
	}
	var deepseek *tui.OAuthProvider
	for i := range options {
		if options[i].ID == "deepseek" && options[i].AuthType == "api_key" {
			deepseek = &options[i]
			break
		}
	}
	if deepseek == nil {
		t.Fatal("deepseek api_key option missing from getLoginProviderOptions")
	}
	rendered := strings.Join(tui.NewOAuthSelectorComponent("login", []tui.OAuthProvider{*deepseek}, nil, nil).Render(120), "\n")
	if !strings.Contains(rendered, "stored key did not resolve") {
		t.Fatalf("selector render = %q, want 'stored key did not resolve'", rendered)
	}
}
