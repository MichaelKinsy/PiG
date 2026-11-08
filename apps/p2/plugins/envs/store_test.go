package envs

import (
	"context"
	"os"
	"testing"
	"time"
)

// Ports envs.test.ts "independent env observer reads before watching and holds last valid state".
func TestEnvObserverReadsBeforeWatchingAndHoldsTheLastValidState(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	store := NewEnvStore(directory)
	t.Cleanup(store.Close)
	if err := SaveEnv(ctx, directory, "gpu", object(t, sshBox)); err != nil {
		t.Fatal(err)
	}
	if err := store.Start(ctx); err != nil {
		t.Fatal(err)
	}
	host := func() any {
		gpu, _ := store.Snapshot().Configs.Value("gpu").(interface{ Value(string) any })
		if gpu == nil {
			return nil
		}
		return gpu.Value("host")
	}
	if host() != "box" {
		t.Fatalf("snapshot = %v", store.Snapshot().Configs)
	}
	if err := os.WriteFile(EnvsPath(directory), []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for store.Snapshot().Error == "" {
		if time.Now().After(deadline) {
			t.Fatal("an invalid envs.json never produced an error")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if host() != "box" {
		t.Errorf("the last valid configuration was lost: %v", store.Snapshot().Configs)
	}
	// Invalid files are not overwritten by a partial mutation.
	_ = SaveEnv(ctx, directory, "gpu", object(t, sshBox))
	if data, _ := os.ReadFile(EnvsPath(directory)); string(data) != "bad" {
		t.Errorf("envs.json = %q", data)
	}
}
