package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestPiDirectoriesSessionOverridePrecedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PIG_CODING_AGENT_SESSION_DIR", "~/pig-sessions")
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "~/pi-sessions")
	for _, shared := range []bool{false, true} {
		t.Setenv("PIG_USE_PI_DIRS", map[bool]string{false: "", true: "1"}[shared])
		want := filepath.Join(home, map[bool]string{false: "pig-sessions", true: "pi-sessions"}[shared])
		got, err := resolveSessionDir("", nil)
		if err != nil || got != want {
			t.Fatalf("shared=%v: %q %v, want %q", shared, got, err, want)
		}
		got, err = resolveSessionDir("explicit", nil)
		if err != nil || got != "explicit" {
			t.Fatalf("flag lost precedence: %q %v", got, err)
		}
	}
	t.Setenv("PIG_USE_PI_DIRS", "1")
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	settings := codingagent.NewSettingsManagerWithProjectTrust(t.TempDir(), t.TempDir(), false)
	got, err := resolveSessionDir("", settings)
	if err != nil || got != "" {
		t.Fatalf("PiG session override leaked into shared default: %q %v", got, err)
	}
}

// Pi agent-session.ts:3124 binds getSystemPrompt in every execution mode.
func TestHeadlessExtensionGetsConfiguredSystemPrompt(t *testing.T) {
	services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	session, err := coding.NewSession(services, coding.SessionOptions{SystemPrompt: "SHARED-SYSTEM-INSTRUCTIONS"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	})
	_, actions := sessionExtensionActions(func() *coding.Session { return session })
	if actions.GetSystemPrompt == nil || !strings.Contains(actions.GetSystemPrompt(), "SHARED-SYSTEM-INSTRUCTIONS") {
		t.Fatal("in-process headless context lost the configured prompt")
	}
	bridge := subprocess.NewUIBridge(func() {})
	bindSessionExtensionActions(nil, bridge, func() *coding.Session { return session }, extension.ContextActions{})
	result, err := bridge.HandleCall("probe", &subprocess.CallPayload{Method: "getSystemPrompt", Args: json.RawMessage(`{}`)})
	if err != nil || result == nil || result.Error != nil || !strings.Contains(string(result.Result), "SHARED-SYSTEM-INSTRUCTIONS") {
		t.Fatalf("subprocess prompt: %+v, %v", result, err)
	}
}
