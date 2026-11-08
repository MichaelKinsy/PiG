package envs

import (
	"context"
	"fmt"
	"os/exec"
	"testing"
	"time"
)

// A sandbox whose host cannot be reached keeps its configuration: moving it to another host stops its VM on the old one first,
// and deleting stops it too; both are refused when that fails (manager.ts `save` and `delete`).
func TestASandboxIsNeitherMovedNorDeletedWhileItsVMCannotBeStopped(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("no ssh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	directory := t.TempDir()
	closed := freePort(t)
	for name, text := range map[string]string{
		"slayer": fmt.Sprintf(`{"type":"ssh","host":"127.0.0.1","port":%d,"user":"nobody"}`, closed),
		"other":  sshHost,
	} {
		if err := SaveEnv(ctx, directory, name, object(t, text)); err != nil {
			t.Fatal(err)
		}
	}
	box := `{"type":"gondolin","host":"slayer"}`
	if err := SaveEnv(ctx, directory, "box1", object(t, box)); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(directory, func(context.Context, string) (int, error) { return 0, nil }, nil, nil)
	defer manager.Close()

	err := manager.Save(ctx, "box1", object(t, `{"type":"gondolin","host":"other"}`))
	wantError(t, err, `Could not stop sandbox box1's VM on "slayer", so it was not moved`)
	// Other changes apply when the VM restarts: no stop is needed.
	if err := manager.Save(ctx, "box1", object(t, `{"type":"gondolin","host":"slayer","cpus":4}`)); err != nil {
		t.Fatal(err)
	}
	err = manager.Delete(ctx, "box1", false)
	wantError(t, err, "Could not stop sandbox box1's VM, so it was not deleted")
	configs, err := LoadEnvs(ctx, directory)
	if err != nil || !IsSandbox(configs, "box1") {
		t.Fatalf("the sandbox is gone after refused changes: %v, %v", configs, err)
	}
	if _, err := manager.Secrets(ctx, "slayer"); err == nil {
		t.Error("secrets of an SSH environment were listed")
	}
	if _, err := manager.Config(ctx, "missing"); err == nil || err.Error() != `Environment "missing" is not configured` {
		t.Errorf("Config(missing) = %v", err)
	}
	wantError(t, manager.ForgetHostKey(ctx, "box1"), "is a sandbox; manage its host's SSH settings instead")
}

func TestUsesRefuseDeletingAnEnvironmentUnlessForced(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	if err := SaveEnv(ctx, directory, "gpu", object(t, sshHost)); err != nil {
		t.Fatal(err)
	}
	for count, want := range map[int]string{1: `1 session still use "gpu"`, 3: `3 sessions still use "gpu"`} {
		manager := NewManager(directory, func(context.Context, string) (int, error) { return count, nil }, nil, nil)
		wantError(t, manager.Delete(ctx, "gpu", false), want)
		manager.Close()
	}
	manager := NewManager(directory, func(context.Context, string) (int, error) { return 2, nil }, nil, nil)
	defer manager.Close()
	if err := manager.Delete(ctx, "gpu", true); err != nil {
		t.Fatal(err)
	}
	if configs, _ := LoadEnvs(ctx, directory); configs.Len() != 0 {
		t.Errorf("a forced delete left %v", configs)
	}
}
