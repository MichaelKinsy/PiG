// Ports the pure parts of test/p2/chat-contracts.test.ts ("FileIndex ranks files and directories...") and
// test/p2/file-index-listing.test.ts (Pi main c23b9ec1).
package fileindex_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	fileindex "github.com/MichaelKinsy/PiG/apps/p2/plugins/file-index"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	nodeenv "github.com/MichaelKinsy/PiG/durable/env/node"
	"github.com/MichaelKinsy/PiG/env"
)

func paths(items []fileindex.FileItem) []string {
	out := make([]string, len(items))
	for at, item := range items {
		out[at] = item.Path
	}
	return out
}

// Pure ranking: pi's name scores, directories first on ties, then depth, length and name; fuzzy matches last.
func TestRankFiles(t *testing.T) {
	items := fileindex.WithDirectories([]string{"src/app.ts", "src/web/app.tsx", "docs/apple.md", "README.md", "src/main.ts"})
	var directories []string
	for _, item := range items {
		if item.Dir {
			directories = append(directories, item.Path)
		}
	}
	if want := []string{"src", "src/web", "docs"}; !slices.Equal(directories, want) {
		t.Fatalf("directories = %v, want %v", directories, want)
	}
	for _, each := range []struct {
		query string
		kind  fileindex.Kind
		limit int
		want  []string
	}{
		{"app", fileindex.KindAny, 20, []string{"src/app.ts", "docs/apple.md", "src/web/app.tsx"}},
		{"web", fileindex.KindAny, 20, []string{"src/web", "src/web/app.tsx"}},
		{"mn", fileindex.KindFile, 20, []string{"src/main.ts"}},
		{"", fileindex.KindDir, 1, []string{"src"}},
	} {
		if got := paths(fileindex.RankFiles(items, each.query, each.limit, each.kind)); !slices.Equal(got, each.want) {
			t.Errorf("RankFiles(%q, %s) = %v, want %v", each.query, each.kind, got, each.want)
		}
	}
	if got := fileindex.RankFiles(items, "", 1, fileindex.KindDir); len(got) != 1 || got[0] != (fileindex.FileItem{Path: "src", Dir: true}) {
		t.Errorf("first directory = %v", got)
	}
}

func TestOutOfScope(t *testing.T) {
	for _, query := range []string{"/etc", "~/x", "../x", ".."} {
		if !fileindex.OutOfScope(query) {
			t.Errorf("%q is in scope", query)
		}
	}
	for _, query := range []string{"src", ".env", "..x", "a/../b"} {
		if fileindex.OutOfScope(query) {
			t.Errorf("%q is out of scope", query)
		}
	}
}

// failing is an environment whose listing fails with err.
type failing struct {
	durableenv.ExecutionEnv
	err error
}

func (f failing) ListFiles(context.Context, string, *durableenv.ListFilesOptions) (durableenv.FileListing, error) {
	return durableenv.FileListing{}, f.err
}

func tree(t *testing.T, files ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, file := range files {
		if strings.HasSuffix(file, "/") {
			if err := os.MkdirAll(filepath.Join(root, file), 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, file)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, file), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestEnvironmentsWithoutListFilesOrWithADaemonReleasedBeforeItFallBack(t *testing.T) {
	root := tree(t, "a.txt")
	listed, err := fileindex.ListIndex(context.Background(), nodeenv.NewNodeExecutionEnv(nodeenv.NodeExecutionEnvOptions{Cwd: root}), root, nil)
	if listed != nil || err != nil {
		t.Fatalf("a Node environment listed: %v, %v", listed, err)
	}
	old := failing{err: durableenv.NewFileError(durableenv.FileErrorNotSupported, "The pi-env daemon does not list files", root, nil)}
	if listed, err := fileindex.ListIndex(context.Background(), old, root, nil); listed != nil || err != nil {
		t.Fatalf("an old daemon listed: %v, %v", listed, err)
	}
	lost := failing{err: durableenv.NewFileError(durableenv.FileErrorUnknown, "lost", root, nil)}
	if _, err := fileindex.ListIndex(context.Background(), lost, root, nil); err == nil || !strings.Contains(err.Error(), "lost") {
		t.Fatalf("a failing listing: %v", err)
	}
	if _, err := fileindex.ListIndex(context.Background(), failing{err: errors.New("lost")}, root, nil); err == nil {
		t.Fatal("a plain error was swallowed")
	}
}

func TestTheDaemonsListingHasEmptyAndHiddenDirectoriesSkipsNodeModulesIgnoredAndPrivatePaths(t *testing.T) {
	daemon, err := env.LocalDaemon()
	if err != nil {
		t.Skip(err)
	}
	if _, err := os.Stat(daemon); err != nil {
		t.Skipf("no pi-env build at %s", daemon)
	}
	root := tree(t, "src/app.ts", "empty/", ".config/x.json", "node_modules/m/index.js", "dist/out.js", "data/sessions/s.sqlite", ".git/HEAD")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("dist/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	connection := env.NewConnection(env.ConnectionOptions{Command: []string{daemon}})
	defer connection.Close()
	local := env.NewLocalExecutionEnv(env.LocalExecutionEnvOptions{NodeExecutionEnvOptions: nodeenv.NodeExecutionEnvOptions{Cwd: root}, Connection: connection})
	listed, err := fileindex.ListIndex(context.Background(), local, root, []string{filepath.Join(root, "data")})
	if err != nil || listed == nil {
		t.Fatalf("listing: %v, %v", listed, err)
	}
	want := []fileindex.FileItem{
		{Path: ".config", Dir: true}, {Path: ".config/x.json"}, {Path: ".gitignore"}, {Path: "empty", Dir: true}, {Path: "src", Dir: true}, {Path: "src/app.ts"},
	}
	if !slices.Equal(listed.Items, want) || listed.Truncated {
		t.Fatalf("listed = %+v, want %+v", listed, want)
	}
}
