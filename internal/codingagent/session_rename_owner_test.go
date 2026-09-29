package codingagent

import (
	"errors"
	"testing"
	"testing/synctest"
)

func TestModalStorageOperationKeepsOwnerTasksLive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := &InteractiveMode{uiTaskCh: make(chan func())}
		want := errors.New("storage failure")
		ownerUpdated := false
		got := m.runModalOperation(func() error {
			// Storage cannot run on the owner: this send needs the same approved modal task pump that renders and applies UI updates.
			m.uiTaskCh <- func() { ownerUpdated = true }
			return want
		})
		m.backgroundTasks.Wait()
		if !ownerUpdated || !errors.Is(got, want) {
			t.Fatalf("owner update=%t error=%v", ownerUpdated, got)
		}
	})
}
