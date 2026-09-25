package experimental

import (
	"syscall"
	"testing"
)

func TestInternalProcessDetachedSession(t *testing.T) {
	child, err := SpawnInternalProcess("coordinator", []string{"-test.run=^TestInternalProcessChild$"}, InternalProcessSpawnOptions{Env: map[string]string{"PIG_PROCESS_TEST_CHILD": "wait"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := TerminateInternalProcess(child); err != nil {
			t.Error(err)
		}
	})
	group, err := syscall.Getpgid(child.PID())
	if err != nil {
		t.Fatal(err)
	}
	if group != child.PID() {
		t.Fatalf("child process group=%d, want child PID=%d", group, child.PID())
	}
	if err := TerminateInternalProcess(child); err != nil {
		t.Fatal(err)
	}
	status, ok := child.ProcessState().Sys().(syscall.WaitStatus)
	if !ok || status.Signal() != syscall.SIGKILL {
		t.Fatalf("exit status: %v", child.ProcessState())
	}
}
