package env

// Ports the RemoteExecutionEnv half of Pi 75ca6e7d0849 (#12055, packages/env/src/remote-env.ts listDir): an entry the daemon reports
// as removed between enumeration and lstat (error ENOENT) is left out of the listing; any other entry error fails it.

import (
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/env/daemon"
)

// runVanishingEntryDaemon answers one directory listing whose entries are a file, one removed after the daemon enumerated it (ENOENT) and,
// when the directory path says so, one that cannot be read (EACCES), the way the Rust daemon reports a failed per-entry lstat.
func runVanishingEntryDaemon(input io.Reader, output io.Writer, token string) int {
	if _, err := fmt.Fprintf(output, "PI-ENV %s\n", token); err != nil {
		return 1
	}
	denied := false
	for {
		request, err := daemon.ReadFrame(input)
		if err != nil || request == nil {
			return 0
		}
		if request.Kind != daemon.FrameRequest {
			continue
		}
		object, _ := request.JSON.(daemon.Object)
		var result daemon.Object
		switch object["op"] {
		case "hello":
			result = daemon.Object{
				"protocol": 1, "version": "test", "os": "linux", "arch": "x86_64", "home": "/", "separator": "/", "tmpdir": "/tmp",
				"cwd": "/", "driveCwds": daemon.Object{}, "pid": os.Getpid(),
			}
		case "opendir":
			denied = object["path"] == "/denied"
			result = daemon.Object{"handle": 1}
		case "readdir":
			entries := []any{
				daemon.Object{"name": "doc-1.jsonl", "info": daemon.Object{"kind": "file", "size": 5, "mtimeSec": 0, "mtimeNsec": 0}},
				daemon.Object{"name": "doc-2.jsonl.reclaim", "error": daemon.Object{"code": "ENOENT", "message": "No such file or directory"}},
			}
			if denied {
				entries = append(entries, daemon.Object{"name": "denied", "error": daemon.Object{"code": "EACCES", "message": "Permission denied"}})
			}
			result = daemon.Object{"entries": entries, "done": true}
		default:
			result = daemon.Object{}
		}
		if err := daemon.WriteFrame(output, &daemon.Frame{Kind: daemon.FrameResult, ID: request.ID, JSON: result}); err != nil {
			return 1
		}
	}
}

func vanishingEntryEnvironment(t *testing.T) *RemoteExecutionEnv {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	connection := NewConnection(ConnectionOptions{Command: []string{self, "vanishing-entry-daemon"}})
	t.Cleanup(connection.Close)
	return NewRemoteExecutionEnv(RemoteExecutionEnvOptions{Connection: connection, ID: "pi-env:vanishing", Cwd: "/"})
}

func TestRemoteListDirLeavesOutAnEntryRemovedBetweenEnumerationAndLstat(t *testing.T) {
	entries, err := vanishingEntryEnvironment(t).ListDir(background, "/dir")
	if err != nil {
		t.Fatalf("ListDir failed on an entry removed during the listing: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "doc-1.jsonl" {
		t.Fatalf("ListDir = %+v, want only doc-1.jsonl", entries)
	}
}

func TestRemoteListDirFailsOnOtherEntryErrors(t *testing.T) {
	if _, err := vanishingEntryEnvironment(t).ListDir(background, "/denied"); err == nil {
		t.Fatal("ListDir succeeded although an entry could not be read (EACCES)")
	}
}
