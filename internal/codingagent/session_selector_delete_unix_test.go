//go:build !windows

package codingagent

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Node 24.19.0 running Pi 0.87.1's deleteSessionFile (session-selector.ts:654-689) reports EACCES for a non-executable trash and runs executables in relative PATH entries, including an empty entry. Linux (glibc execvp) runs executable text without a shebang through /bin/sh; Darwin does not.
func TestDeleteSessionFileUnixPathLookup(t *testing.T) {
	t.Run("non executable reports EACCES", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "trash"), []byte("not executable"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", dir)
		target := t.TempDir()
		want := sessionDeleteResult{method: sessionDeleteUnlink, error: nodeUnlinkDirectoryError(target) + " (trash: spawnSync trash EACCES)"}
		if got := deleteSessionFile(target); got != want {
			t.Fatalf("got %+v; want %+v", got, want)
		}
	})
	for _, setup := range []string{"plain executable text", "missing interpreter before usable trash"} {
		t.Run(setup, func(t *testing.T) {
			dir := t.TempDir()
			path := dir
			body := "exit 0\n"
			if setup == "missing interpreter before usable trash" {
				body = "#!/nonexistent/rv-picker-interpreter\n"
				path += string(os.PathListSeparator) + installFakeTrash(t)
			}
			if err := os.WriteFile(filepath.Join(dir, "trash"), []byte(body), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", path)
			t.Setenv(fakeTrashEnv, "move")
			target := t.TempDir()
			want := sessionDeleteResult{ok: true, method: sessionDeleteTrash}
			if runtime.GOOS == "darwin" && setup == "plain executable text" {
				// libuv's Darwin posix_spawn search returns ENOEXEC without glibc's /bin/sh fallback (src/unix/process.c uv__spawn_and_init_child_posix_spawn).
				want = sessionDeleteResult{method: sessionDeleteUnlink, error: nodeUnlinkDirectoryError(target) + " (trash: spawnSync trash ENOEXEC)"}
			}
			if got := deleteSessionFile(target); got != want {
				t.Fatalf("got %+v; want %+v", got, want)
			}
		})
	}
	for _, path := range []string{".", "", ":/nonexistent"} {
		t.Run("relative PATH="+path, func(t *testing.T) {
			dir := installFakeTrash(t)
			t.Chdir(dir)
			t.Setenv("PATH", path)
			t.Setenv(fakeTrashEnv, "move")
			target := filepath.Join(t.TempDir(), "session.jsonl")
			if err := os.WriteFile(target, []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			want := sessionDeleteResult{ok: true, method: sessionDeleteTrash}
			if got := deleteSessionFile(target); got != want {
				t.Fatalf("got %+v; want %+v", got, want)
			}
		})
	}
}
