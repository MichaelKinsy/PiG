package env

// Ports packages/env/src/remote-env.ts #resolve, checked against the cases testdata/generate_resolve_cases.mjs records
// from Pi's own RemoteExecutionEnv.absolutePath: `~`, `file://` URLs, relative paths, and Windows drive-relative paths
// against the remote's per-drive working directories.

import (
	"encoding/json"
	"os"
	"runtime"
	"testing"
)

// connectedTo is a Connection whose start already reported info, so path rules run without a daemon.
func connectedTo(info RemoteInfo) *Connection {
	connection := NewConnection(ConnectionOptions{})
	ready := &readyStart{done: make(chan struct{}), info: info, session: &session{id: 1, live: true}}
	close(ready.done)
	connection.ready = ready
	return connection
}

// mutation-checked: zeroing the results of RemoteExecutionEnv.AbsolutePath fails it
// mutation-checked: dropping the reads and writes of RemoteInfo.Cwd, RemoteInfo.DriveCwds, RemoteInfo.Home, RemoteInfo.OS fails it
// Pi: packages/env/src/remote-env.ts:432 (absolutePath)
// TestResolvePathMatchesPi checks packages/env/src/remote-env.ts:371 (#resolve over the remote info's os, home, cwd and
// driveCwds) through absolutePath (remote-env.ts:432) against the recorded Pi cases.
func TestResolvePathMatchesPi(t *testing.T) {
	var file struct {
		Cases []struct {
			Info struct {
				OS        string            `json:"os"`
				Home      string            `json:"home"`
				Cwd       string            `json:"cwd"`
				DriveCwds map[string]string `json:"driveCwds"`
			} `json:"info"`
			Cwd      string `json:"cwd"`
			Path     string `json:"path"`
			Resolved string `json:"resolved"`
		} `json:"cases"`
	}
	mustDo(json.Unmarshal(must(os.ReadFile("testdata/resolve_cases.json")), &file))
	windowsCases := 0
	for _, c := range file.Cases {
		// Rooted Windows paths without a drive take the drive of the client's working directory, which the cases were
		// recorded on a POSIX client without.
		if c.Info.OS == "windows" && runtime.GOOS == "windows" {
			continue
		}
		if c.Info.OS == "windows" {
			windowsCases++
		}
		connection := connectedTo(RemoteInfo{Protocol: 1, OS: c.Info.OS, Home: c.Info.Home, Cwd: c.Info.Cwd, DriveCwds: c.Info.DriveCwds})
		environment := NewRemoteExecutionEnv(RemoteExecutionEnvOptions{Connection: connection, ID: "pi-env:test", Cwd: c.Cwd})
		// upstream: packages/env/src/remote-env.ts:432 absolutePath resolves against the session cwd and the remote platform.
		got, err := environment.AbsolutePath(background, c.Path)
		if err != nil || got != c.Resolved {
			t.Errorf("%s cwd %q: AbsolutePath(%q) = %q, %v; Pi gives %q", c.Info.OS, c.Cwd, c.Path, got, err, c.Resolved)
		}
	}
	if len(file.Cases) < 100 || (runtime.GOOS != "windows" && windowsCases < 60) {
		t.Fatalf("case table is too small: %d cases, %d Windows", len(file.Cases), windowsCases)
	}
}
