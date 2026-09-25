package codingagent

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// ensureManagedTools awaits both installers before enabling input. The owner goroutine renders each status as it arrives, while downloads run concurrently and share the startup cancellation lifetime.
func (m *InteractiveMode) ensureManagedTools(ctx context.Context, tm *tools.ToolsManager) {
	statuses := make(chan tools.ToolStatus)
	var initialStatuses []chan tools.ToolStatus
	var wg sync.WaitGroup
	for _, name := range []string{"fd", "rg"} {
		initial := make(chan tools.ToolStatus)
		initialStatuses = append(initialStatuses, initial)
		wg.Go(func() {
			first := true
			tm.EnsureTool(ctx, name, func(status tools.ToolStatus) {
				if first {
					first = false
					initial <- status
					close(initial)
				} else {
					statuses <- status
				}
			})
			if first {
				close(initial)
			}
		})
	}
	go func() {
		wg.Wait()
		close(statuses)
	}()
	// Pi executes each ensureTool up to its first await in Promise.all argument order. Initial reports therefore precede asynchronous completion reports, with fd before rg.
	for _, initial := range initialStatuses {
		if status, ok := <-initial; ok {
			m.showManagedToolStatus(status)
		}
	}
	for status := range statuses {
		m.showManagedToolStatus(status)
	}
}
