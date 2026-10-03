package coding

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The harness owns the Services it builds, so its cleanup must drain model background work before the temp dirs are
// removed. Work admitted under Services that reads or writes the agent dir would otherwise run past the test and
// race the directory removal (Windows: "The directory is not empty").
func TestRecoveryHarnessCleanupDrainsServicesWork(t *testing.T) {
	var agentDir string
	finished, present, recreated := false, false, false
	t.Run("harness lifetime", func(t *testing.T) {
		h := newRecoveryHarness(t, harnessOptions{})
		services := h.session.services
		agentDir = services.AgentDir()
		authPath := filepath.Join(agentDir, "auth.json")
		if err := os.Remove(authPath); err != nil {
			t.Fatal(err)
		}
		admitted := services.Registry().StartModelTask(context.Background(), func(ctx context.Context) {
			<-ctx.Done()
			_, err := os.Stat(agentDir)
			present = err == nil
			// A reload on a missing store creates it, as a late background read does.
			_, _ = services.Auth().Read(context.Background(), "late")
			_, err = os.Stat(authPath)
			recreated = err == nil
			finished = true
		})
		if !admitted {
			t.Fatal("model task was not admitted")
		}
	})
	if !finished {
		t.Fatal("harness cleanup returned before Services drained its model task")
	}
	if !present {
		t.Error("the model task ran after the agent dir was removed")
	}
	if !recreated {
		t.Fatal("the late read did not write auth.json, so the test does not exercise an agent-dir writer")
	}
	// The late write must land before the temp dir removal: a write after it recreates the agent dir and leaks it.
	if _, err := os.Stat(agentDir); !os.IsNotExist(err) {
		t.Errorf("agent dir %s survived harness cleanup: %v", agentDir, err)
		_ = os.RemoveAll(filepath.Dir(agentDir))
	}
}
