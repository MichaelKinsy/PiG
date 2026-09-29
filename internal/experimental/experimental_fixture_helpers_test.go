package experimental

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness/env"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

// The supported Unix platforms have a 103-byte minimum sun_path budget. The longest server filename comes from experimental/server.ts, not the current temporary directory's observed length.
func TestExperimentalFixtureSocketPathsFitNativeLimitWithLongTestingDirectoryNames(t *testing.T) {
	isolateExperimentalTest(t)
	if got := os.Getenv("PI_OFFLINE"); got != "1" {
		t.Fatalf("offline fixture=%q, want 1", got)
	}
	if runtime.GOOS == "windows" {
		return
	} // Upstream Unix transport is unavailable on Windows; no Unix socket is created there.
	name := "server-00000000-0000-4000-8000-000000000001-0123456789ab.sock"
	for _, directory := range []string{socketDir(t), filepath.Join(os.Getenv("HOME"), ".pi", "server")} {
		path := filepath.Join(directory, name)
		if len(path) > 103 {
			t.Fatalf("fixture socket path uses %d bytes, maximum 103: %s", len(path), path)
		}
	}
}

// The shared fixtures from experimental-session-support.ts must create and read the real durable JSONL repository, not manufacture model or Session snapshots.
func TestExperimentalFixtureRepositoryRoundTrip(t *testing.T) {
	agentDir := setupExperimentalRemoteTest(t)
	if got := os.Getenv("PI_CODING_AGENT_DIR"); got != agentDir {
		t.Fatalf("agent root=%q, want %q", got, agentDir)
	}
	data, err := os.ReadFile(filepath.Join(agentDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"anthropic":{"type":"api_key","key":"test-key"}}` {
		t.Fatalf("fixture auth=%q", data)
	}
	root := filepath.Join(agentDir, "experimental", "sessions")
	cwd := filepath.Join(t.TempDir(), "other-cwd")
	metadata := createExperimentalSessions(t, root, []string{"configured"}, cwd)
	fileSystem := env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: cwd})
	repo := session.NewJsonlSessionRepo(session.JsonlSessionRepoOptions{FileSystem: fileSystem, SessionsRoot: root})
	ctx := context.Background()
	t.Cleanup(func() {
		if err := repo.Close(ctx); err != nil {
			t.Error(err)
		}
		fileSystem.Cleanup(ctx)
	})
	// experimental-session-support.ts:12-31 creates headers only. Its reader at :40-74 requires the main branch that a worker initializes.
	created, err := repo.List(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"demo-1", "demo-2"} {
		index := slices.IndexFunc(created, func(item session.SessionMetadata) bool { return item.ID == id })
		if index < 0 {
			t.Fatalf("fresh Session %s missing from repository", id)
		}
		fresh, err := repo.Open(ctx, created[index])
		if err != nil {
			t.Fatal(err)
		}
		branch, branchError := fresh.Branch(ctx, "main")
		closeError := fresh.Close(ctx)
		if branchError != nil || closeError != nil || branch != nil {
			t.Fatalf("fresh Session %s main=%v, read=%v, close=%v; want absent branch", id, branch, branchError, closeError)
		}
	}
	opened, err := repo.Open(ctx, metadata[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := opened.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	if _, err := opened.CreateBranch(ctx, "main", nil); err != nil {
		t.Fatal(err)
	}
	configuration := session.LaneConfiguration{Model: session.ModelRef{Provider: "test", ModelID: "configured"}, ThinkingLevel: ai.ThinkingOff, ActiveToolNames: []string{"read"}}
	if err := opened.SetValue(ctx, session.LaneConfig("main").Address(), configuration); err != nil {
		t.Fatal(err)
	}
	if err := opened.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(ctx); err != nil {
		t.Fatal(err)
	}
	state := readExperimentalSessionState(t, root, "configured")
	if !reflect.DeepEqual(state.Model, &configuration.Model) || !reflect.DeepEqual(state.ActiveTools, configuration.ActiveToolNames) {
		t.Fatalf("state=%#v, want model=%#v tools=%#v", state, configuration.Model, configuration.ActiveToolNames)
	}
}
