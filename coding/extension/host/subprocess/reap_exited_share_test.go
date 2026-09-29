package subprocess

import (
	"os"
	"path/filepath"
	"testing"
)

// A process that has exited is no longer shared, whatever references other extensions still hold: their processes died with it. reap must finish the dead process's cleanup instead of leaving it to the reference holders, which would leave its diagnostic log behind.
func TestReapCleansUpAnExitedProcessThatStillHasReferences(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "extension.log")
	if err := os.WriteFile(logPath, []byte("crash output"), 0o600); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	close(exited)
	share := newProcessShare("node-extension:reap", nil, exited, nil, nil)
	if !share.acquire() {
		t.Fatal("share was not acquirable")
	}
	me := &managedExt{share: share, exitedCh: exited, stderrLog: &processStderrLog{path: logPath}}
	me.reap()
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("the exited process's diagnostic log remains after reap: %v", err)
	}
}

// While the process runs, a stopped extension leaves the shared process and its log to the extensions still using them.
func TestReapLeavesALiveSharedProcessAlone(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "extension.log")
	if err := os.WriteFile(logPath, []byte("output"), 0o600); err != nil {
		t.Fatal(err)
	}
	running := make(chan struct{})
	share := newProcessShare("node-extension:reap", nil, running, nil, nil)
	if !share.acquire() {
		t.Fatal("share was not acquirable")
	}
	me := &managedExt{share: share, exitedCh: running, stderrLog: &processStderrLog{path: logPath}}
	me.reap()
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("reap removed the log of a process another extension still uses: %v", err)
	}
}
