package coding

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: packages/coding-agent/test/sdk-session-manager.test.ts:28 — uses agentDir for the default persisted session path.
func TestUpstreamSDKSessionManagerDefaultPersistedPath(t *testing.T) {
	_, model, cwd, agentDir := sessionManagerFixture(t)
	session := createSessionWithServicesOptions(t, CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir}, SessionOptions{Model: model})
	safePath := "--" + strings.NewReplacer("/", "-", `\`, "-", ":", "-").Replace(strings.TrimPrefix(cwd, string(filepath.Separator))) + "--"
	expectedDir := filepath.Join(agentDir, "sessions", safePath)
	if got := session.SessionManager().GetSessionDir(); got != expectedDir {
		t.Errorf("sessionDir=%q want=%q", got, expectedDir)
	}
	if !strings.HasPrefix(session.Path(), expectedDir+string(filepath.Separator)) {
		t.Errorf("sessionFile=%q missing prefix %q", session.Path(), expectedDir)
	}
	_, statErr := os.Stat(session.Path())
	printSessionManagerProbe(t, 1, []any{session.SessionManager().GetSessionDir() == expectedDir, strings.HasPrefix(session.Path(), expectedDir+string(filepath.Separator)), session.SessionManager().IsPersisted(), statErr == nil})
}

// upstream: packages/coding-agent/test/sdk-session-manager.test.ts:49 — keeps an explicit sessionManager override.
func TestUpstreamSDKSessionManagerExplicitOverride(t *testing.T) {
	_, model, cwd, agentDir := sessionManagerFixture(t)
	manager, err := NewInMemorySessionManager(cwd)
	if err != nil {
		t.Fatal(err)
	}
	session := createSessionWithServicesOptions(t, CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir, SessionManager: manager}, SessionOptions{Model: model, SessionManager: manager})
	if session.SessionManager() != manager {
		t.Fatal("explicit manager identity changed")
	}
	if session.SessionManager().IsPersisted() {
		t.Fatal("in-memory manager became persisted")
	}
	printSessionManagerProbe(t, 2, []any{session.SessionManager() == manager, manager.IsPersisted(), manager.GetSessionFile() == nil})
}

// upstream: packages/coding-agent/test/sdk-session-manager.test.ts:67 — derives cwd from an explicit sessionManager when cwd is omitted.
func TestUpstreamSDKSessionManagerDerivesCWD(t *testing.T) {
	_, model, cwd, agentDir := sessionManagerFixture(t)
	sessionCWD := filepath.Join(filepath.Dir(cwd), "session-project")
	if err := os.MkdirAll(sessionCWD, 0o755); err != nil {
		t.Fatal(err)
	}
	manager, err := NewInMemorySessionManager(sessionCWD)
	if err != nil {
		t.Fatal(err)
	}
	session := createSessionWithServicesOptions(t, CreateAgentSessionServicesOptions{AgentDir: agentDir, SessionManager: manager}, SessionOptions{Model: model, SessionManager: manager})
	if session.SessionManager() != manager {
		t.Fatal("explicit manager identity changed")
	}
	// upstream system-prompt.ts:170 renders the cwd with forward slashes.
	promptCWD := "<cwd>\n" + strings.ReplaceAll(sessionCWD, `\`, "/") + "\n</cwd>"
	if !strings.Contains(session.SystemPrompt(), promptCWD) {
		t.Fatalf("wrong prompt cwd: %s", session.SystemPrompt())
	}
	// Git Bash's pwd prints an MSYS path (/tmp/...); pwd -W prints the Windows path.
	pwd := "pwd"
	if runtime.GOOS == "windows" {
		pwd = "pwd -W"
	}
	output := executeSessionBash(t, session, pwd)
	got, err := filepath.EvalSymlinks(strings.TrimSpace(output))
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(sessionCWD)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("bash pwd=%q want=%q", got, want)
	}
	printSessionManagerProbe(t, 3, []any{session.SessionManager() == manager, strings.Contains(session.SystemPrompt(), promptCWD), got == want})
}

// upstream: packages/coding-agent/test/sdk-session-manager.test.ts:96 — exposes current session state to the built-in bash tool.
func TestUpstreamSDKSessionManagerBashCurrentState(t *testing.T) {
	_, model, cwd, agentDir := sessionManagerFixture(t)
	session := createSessionWithServicesOptions(t, CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir}, SessionOptions{Model: model, ThinkingLevel: ai.ThinkingHigh})
	if session.Path() == "" {
		t.Fatal("missing sessionFile")
	}
	if !strings.Contains(session.SystemPrompt(), "You can inspect PI_* environment variables for current model and session details.") {
		t.Fatal("missing session environment guideline")
	}
	output := executeSessionBash(t, session, `printf '%s\n' "$PI_SESSION_ID" "$PI_SESSION_FILE" "$PI_PROVIDER" "$PI_MODEL" "$PI_REASONING_LEVEL"`)
	got := strings.Split(strings.TrimSpace(output), "\n")
	want := []string{session.ID(), session.Path(), model.ProviderMeta.ProviderID, model.ID, string(session.ThinkingLevel())}
	if !slices.Equal(got, want) {
		t.Fatalf("bash state=%q want=%q", got, want)
	}
	printSessionManagerProbe(t, 4, []any{got[0] == session.ID(), got[1] == session.Path(), got[2], got[3], got[4]})
}
