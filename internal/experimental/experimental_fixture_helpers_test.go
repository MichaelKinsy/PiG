package experimental

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/experimental/durabletest"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
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

// The shared fixtures from experimental-session-support.ts must create and read the real catalog and the worker-owned storage, not manufacture model or Session snapshots.
func TestExperimentalFixtureCatalogRoundTrip(t *testing.T) {
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
	listed, err := ListSessions(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"demo-1", "demo-2", "configured"} {
		if !slices.ContainsFunc(listed, func(item SessionCatalogMetadata) bool { return item.ID == id }) {
			t.Fatalf("fresh Session %s missing from the catalog", id)
		}
	}
	if len(metadata) != 1 || metadata[0].Cwd != cwd {
		t.Fatalf("created = %+v; want cwd %q", metadata, cwd)
	}
	// A fresh Session has no storage until its worker opens it.
	if state := readExperimentalSessionState(t, root, "configured"); state.Model != nil {
		t.Fatalf("fresh Session has a model: %+v", state.Model)
	}
	storage, err := durabletest.OpenFile(SessionStoragePath(metadata[0]))
	if err != nil {
		t.Fatal(err)
	}
	configured := services.ModelRef{Provider: "test", ModelId: "configured"}
	if err := storage.Conversation.Configure(t.Context(), services.ConversationConfiguration{Model: &configured}); err != nil {
		t.Fatal(err)
	}
	if err := storage.Harness.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if state := readExperimentalSessionState(t, root, "configured"); !reflect.DeepEqual(state.Model, &configured) {
		t.Fatalf("state=%#v, want model=%#v", state, configured)
	}
	// A duplicate Session ID fails like the catalog does.
	if _, err := CreateSession(root, CreateSessionOptions{ID: new("demo-1"), Cwd: cwd}); err == nil || err.Error() != "Session demo-1 already exists" {
		t.Fatalf("duplicate = %v", err)
	}
}
