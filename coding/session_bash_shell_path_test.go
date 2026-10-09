package coding

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi agent-session.ts:3851-3858 executeBash reads settingsManager.getShellPath() before the command runs and hands it to createLocalBashOperations({ shellPath }) (bash.ts:174):
// a shell path that does not exist fails the command with "Custom shell path not found", and a setting getShellPath rejects fails it before any operations run, even custom ones.
func TestExecuteBashPassesTheShellPathSettingToTheLocalShell(t *testing.T) {
	missing := "/definitely/not/a/shell"
	if runtime.GOOS == "windows" {
		missing = `C:\definitely\not\a\shell.exe`
	}
	session, err := NewSession(newTestServicesWithSettings(t, icodingagent.Settings{ShellPath: missing}), SessionOptions{Model: fakeModel(), SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	_, err = session.ExecuteBash(t.Context(), "echo hi", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "Custom shell path not found: "+missing) {
		t.Fatalf("ExecuteBash with a missing shell path = %v, want the custom shell path error", err)
	}
}

func TestExecuteBashRejectsAnInvalidShellPathSettingBeforeRunningOperations(t *testing.T) {
	raw := "file://server/share/a%2Fb"
	if runtime.GOOS == "windows" {
		raw = "file:///no/drive"
	}
	session, err := NewSession(newTestServicesWithSettings(t, icodingagent.Settings{ShellPath: raw}), SessionOptions{Model: fakeModel(), SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	ran := false
	operations := bashPersistenceOperations(func(context.Context, string, string, extension.BashOperationsExecOptions) (extension.BashOperationsResult, error) {
		ran = true
		return extension.BashOperationsResult{ExitCode: new(0)}, nil
	})
	if _, err := session.ExecuteBash(t.Context(), "custom", nil, &ExecuteBashOptions{Operations: operations}); err == nil {
		t.Fatal("ExecuteBash accepted a shell path setting getShellPath rejects")
	}
	if ran {
		t.Error("the custom operations ran although getShellPath rejected the setting")
	}
}
