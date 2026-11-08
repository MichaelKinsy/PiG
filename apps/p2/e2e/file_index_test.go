// Ports the FileIndex cases of test/p2/chat-contracts.test.ts and test/p2/file-index-listing.test.ts (Pi main c23b9ec1): the
// index ranks the session directory's files and directories, follows changes, refuses viewers, and leaves out p2's data
// directory.

package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/env"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/file-index"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/workspace"
)

func indexSession(t *testing.T, fixture *p2test.Fixture, owner host.Host, cwd string) string {
	t.Helper()
	sessions, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", cwd), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	return object(created).Value("id").(string)
}

func completePaths(t *testing.T, index *host.Reference, request *delta.JsonObject) []string {
	t.Helper()
	answered, err := index.Call("complete", request, p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, each := range listOf(object(answered).Value("items")) {
		found = append(found, object(each).Value("path").(string))
	}
	return found
}

func sameStrings(a, b []string) bool { return strings.Join(a, "\x00") == strings.Join(b, "\x00") }

func TestFileIndexRanksFilesAndDirectoriesFollowsChangesRefusesViewers(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{AllowedHosts: []string{"share.test"}})
	owner := fixture.Browser()
	session := indexSession(t, fixture, owner, fixture.Workspace)
	for _, directory := range []string{"src/web"} {
		if err := os.MkdirAll(filepath.Join(fixture.Workspace, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"src/app.ts", "src/web/view.tsx", ".env.example"} {
		if err := os.WriteFile(filepath.Join(fixture.Workspace, file), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	index, err := p2test.HoldIn(owner, p2runtime.FileIndex, delta.JsonObjectOf("session", session, "conversation", float64(1)), "reader")
	if err != nil {
		t.Fatal(err)
	}
	answered, err := index.Call("complete", delta.JsonObjectOf("query", "view"), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	if encoded, _ := chordjson.Marshal(answered); string(encoded) != `{"items":[{"path":"src/web/view.tsx","dir":false}],"truncated":false}` {
		t.Fatalf("view = %s", encoded)
	}
	if got := completePaths(t, index, delta.JsonObjectOf("query", "", "kind", "dir")); !sameStrings(got, []string{"src", "src/web"}) {
		t.Fatalf("directories = %v", got)
	}
	// Hidden files are included; out-of-scope prefixes complete nothing (cwd-relative only).
	if got := completePaths(t, index, delta.JsonObjectOf("query", ".env")); !sameStrings(got, []string{".env.example"}) {
		t.Fatalf(".env = %v", got)
	}
	for _, query := range []string{"/etc", "~/x", "../x", ".."} {
		if got := completePaths(t, index, delta.JsonObjectOf("query", query)); len(got) != 0 {
			t.Fatalf("%s completed %v", query, got)
		}
	}
	listed, err := index.Call("list", delta.JsonObjectOf("path", "src"), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	if encoded, _ := chordjson.Marshal(listed); string(encoded) != `{"entries":[{"name":"web","dir":true},{"name":"app.ts","dir":false}],"truncated":false}` {
		t.Fatalf("list = %s", encoded)
	}
	if _, err := index.Call("list", delta.JsonObjectOf("path", "../"), p2test.Background); err == nil || !strings.Contains(err.Error(), "Invalid path") {
		t.Fatalf("list ../: %v", err)
	}
	// A new file advances `changes`; the next query sees it.
	before, _ := index.Member("changes").Value()
	count, _ := before.(float64)
	if err := os.WriteFile(filepath.Join(fixture.Workspace, "src/added.ts"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := p2test.Until(index.Member("changes"), func(value delta.JsonValue) bool { number, _ := value.(float64); return number > count }, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if got := completePaths(t, index, delta.JsonObjectOf("query", "added")); !sameStrings(got, []string{"src/added.ts"}) {
		t.Fatalf("added = %v", got)
	}
	// View-only guests get no file names.
	viewer := guestOf(t, fixture, owner, session, "view", "Viewer")
	denied, err := viewer.Hold(p2test.Background, p2runtime.FileIndex, delta.JsonObjectOf("session", session, "conversation", float64(1)), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	p2test.ExpectChord(t, p2runtime.WhenLive(p2test.Background, denied), data.CodeForbidden)
}

func TestALocalSessionsIndexLeavesOutP2sDataDirectoryFromEitherSource(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{})
	// The session works in the directory that holds p2's data directory (fixture: <root>/data next to <root>/workspace).
	root := filepath.Dir(fixture.Workspace)
	if err := os.MkdirAll(filepath.Join(fixture.Workspace, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.Workspace, "src", "parser.ts"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(fixture.Workspace, "emptydir"), 0o755); err != nil {
		t.Fatal(err)
	}
	owner := fixture.Browser()
	session := indexSession(t, fixture, owner, root)
	index, err := p2test.HoldIn(owner, p2runtime.FileIndex, delta.JsonObjectOf("session", session, "conversation", float64(1)), "reader")
	if err != nil {
		t.Fatal(err)
	}
	answered, err := index.Call("complete", delta.JsonObjectOf("query", "parser", "limit", float64(10)), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	if first := object(listOf(object(answered).Value("items"))[0]); first.Value("path") != "workspace/src/parser.ts" || first.Value("dir") != false {
		t.Fatalf("first = %v", first)
	}
	all := completePaths(t, index, delta.JsonObjectOf("query", "", "limit", float64(100)))
	if len(all) == 0 {
		t.Fatal("nothing indexed")
	}
	for _, path := range all {
		if path == "data" || strings.HasPrefix(path, "data/") {
			t.Fatalf("the data directory is private: %s", path)
		}
	}
	// Empty directories come only from the daemon's listing: a local session uses it where pi-env ships a daemon with it.
	daemon := false
	if path, err := env.LocalDaemon(); err == nil {
		if _, err := os.Stat(path); err == nil {
			daemon = true
		}
	}
	found := false
	for _, path := range completePaths(t, index, delta.JsonObjectOf("query", "emptydir", "limit", float64(10))) {
		found = found || path == "workspace/emptydir"
	}
	if found != daemon {
		t.Fatalf("workspace/emptydir indexed = %v, daemon = %v", found, daemon)
	}
}
