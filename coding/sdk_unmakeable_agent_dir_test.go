package coding

// pi: packages/coding-agent/src/core/sdk.ts

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// unmakeableAgentDirs are the agent directories that cannot be made: under a regular file (ENOTDIR) and under a directory the process cannot write into (EACCES, absent where the process can write there anyway). A read-only file system (EROFS) is not made here: a test cannot mount one, so that row is untested.
func unmakeableAgentDirs(t *testing.T) map[string]string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dirs := map[string]string{"file where the directory should be": filepath.Join(file, "agent")}
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	if probe, err := os.Create(filepath.Join(locked, "probe")); err == nil {
		_ = probe.Close()
		_ = os.Remove(probe.Name())
	} else {
		dirs["unwritable parent"] = filepath.Join(locked, "agent")
	}
	return dirs
}

// What Pi's createAgentSession observes at each point for an agent directory that cannot be made, as the Pi oracle reports it.
type piUnmakeableAgentDir struct {
	// Create is the message the default session manager's creation throws (sdk.ts:201), or "ok".
	Create string `json:"create"`
	// Logout is the message of the first credential write (ModelRuntime.logout), or "ok".
	Logout string `json:"logout"`
	// Credentials is the number of credentials list() resolves.
	Credentials int `json:"credentials"`
	// Settings are the messages of the errors the first settings write records (settings-manager.ts enqueueWrite, drainErrors).
	Settings []string `json:"settings"`
}

func probePiUnmakeableAgentDir(t *testing.T, cwd, agentDir string) piUnmakeableAgentDir {
	t.Helper()
	var observed piUnmakeableAgentDir
	pioracle.Run(t, `
const path = await import("node:path");
const out = { create: "ok", logout: "ok", credentials: -1, settings: [] };
const { SessionManager, getDefaultSessionDir } = await load("pi-coding-agent/core/session-manager.js");
try { SessionManager.create(input.cwd, getDefaultSessionDir(input.cwd, input.agentDir)); } catch (error) { out.create = error.message; }
const { ModelRuntime } = await load("pi-coding-agent/core/model-runtime.js");
const runtime = await ModelRuntime.create({ authPath: path.join(input.agentDir, "auth.json"), modelsPath: path.join(input.agentDir, "models.json") });
try { await runtime.logout("openai"); } catch (error) { out.logout = error.message; }
out.credentials = (await runtime.credentials.list()).length;
const { SettingsManager } = await load("pi-coding-agent/core/settings-manager.js");
const settings = SettingsManager.create(input.cwd, input.agentDir);
settings.setTheme("dark");
await settings.flush();
out.settings = settings.drainErrors().map((entry) => entry.scope + ": " + entry.error.message);
emit(out);`, map[string]string{"cwd": cwd, "agentDir": agentDir}, &observed)
	return observed
}

// sdk.ts:198 awaits ModelRuntime.create({ authPath, modelsPath }), which never rejects for an agent directory that cannot be made (auth-storage.ts:68,99,379-392: the lock that makes the directory is inside reload(), whose catch keeps the last snapshot), so createAgentSession with a session manager of the caller's returns a session. The problem is reported by the first operation that writes: ModelRuntime.logout rejects with Node's mkdir error, the first setting write records one (drainErrors), and list() resolves empty. Each row runs the same steps on Pi's modules first.
func TestCreateAgentSessionWithASuppliedSessionManagerOpensWhereTheAgentDirCannotBeMade(t *testing.T) {
	for name, agentDir := range unmakeableAgentDirs(t) {
		t.Run(name, func(t *testing.T) {
			cwd := t.TempDir()
			want := probePiUnmakeableAgentDir(t, cwd, agentDir)
			manager, err := NewInMemorySessionManager(cwd)
			if err != nil {
				t.Fatal(err)
			}
			result, err := CreateAgentSession(CreateAgentSessionOptions{CWD: cwd, AgentDir: agentDir, SessionManager: manager})
			if err != nil || result.Session == nil {
				t.Fatalf("CreateAgentSession = session %v, error %v; Pi's createAgentSession resolves", result.Session != nil, err)
			}
			t.Cleanup(func() { _ = result.Session.Close() })
			runtime := result.Session.ModelRuntime()
			if credentials, err := runtime.ListCredentials(context.Background()); err != nil || len(credentials) != want.Credentials {
				t.Fatalf("ListCredentials = %v, %v; Pi lists %d", credentials, err, want.Credentials)
			}
			if err := runtime.Logout(t.Context(), "openai"); err == nil || err.Error() != want.Logout {
				t.Fatalf("Logout error = %v, want Pi's %q", err, want.Logout)
			}
			_ = result.Session.SettingsManager().SetTheme("dark")
			var got []string
			for _, recorded := range result.Session.SettingsManager().DrainErrors() {
				got = append(got, recorded.Scope+": "+recorded.Error.Error())
			}
			if len(got) != len(want.Settings) || (len(got) == 1 && got[0] != want.Settings[0]) {
				t.Fatalf("settings errors after the first write = %q, want Pi's %q", got, want.Settings)
			}
		})
	}
}

// Without a session manager of the caller's, createAgentSession creates the default one (sdk.ts:201) and its directory is made eagerly, so the call rejects with Node's mkdir error for <agentDir>/sessions/<cwd> and nothing else (session-manager.ts:597-602): no auth storage error, no prefix.
func TestCreateAgentSessionRejectsWithNodesMkdirErrorForTheDefaultSessionDirectory(t *testing.T) {
	for name, agentDir := range unmakeableAgentDirs(t) {
		t.Run(name, func(t *testing.T) {
			cwd := t.TempDir()
			want := probePiUnmakeableAgentDir(t, cwd, agentDir)
			if want.Create == "ok" {
				t.Fatalf("Pi created the session directory under %s", agentDir)
			}
			result, err := CreateAgentSession(CreateAgentSessionOptions{CWD: cwd, AgentDir: agentDir})
			if result.Session != nil {
				_ = result.Session.Close()
			}
			if err == nil || err.Error() != want.Create || result.Session != nil {
				t.Fatalf("CreateAgentSession = session %v, error %v; want no session and Pi's %q", result.Session != nil, err, want.Create)
			}
		})
	}
}
