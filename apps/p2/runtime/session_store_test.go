package runtime_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
)

// apps/p2/src/runtime/session-store.ts has no unit test upstream: harness-cwd, subagent-cwd and tree open a stopped
// session's records with openSessionStorage (the tree cases arrive with the tree plugin). These cases pin its contract.

func TestOpenSessionStorageOpensAndClosesAStoppedSession(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	ctx := context.Background()
	dataDir := t.TempDir()
	for range 2 { // closed, it opens again: nothing stays claimed
		opened, err := p2runtime.OpenSessionStorage(ctx, dataDir, "s1")
		if err != nil {
			t.Fatal(err)
		}
		if opened.Store == nil || opened.Storage == nil {
			t.Fatal("no store or storage")
		}
		if err := opened.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(filepath.Join(dataDir, "sessions", "s1", "store.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("store mode %v", info.Mode().Perm())
	}
}

func TestOpenSessionStorageClaimsTheDurableOwner(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	ctx := context.Background()
	opened, err := p2runtime.OpenSessionStorage(ctx, t.TempDir(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close(ctx) })
	if _, err := p2runtime.DurableStorage(ctx, opened.Store); err == nil {
		t.Fatal("a second claim of p2.durable on the store was accepted")
	}
}

func TestOpenSessionStorageRefusesAnIdThatIsNoPlainName(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	dataDir := t.TempDir()
	if _, err := p2runtime.OpenSessionStorage(context.Background(), dataDir, "../x"); err == nil {
		t.Fatal("accepted")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "sessions")); err == nil {
		t.Fatal("a directory was made for a refused id")
	}
}
