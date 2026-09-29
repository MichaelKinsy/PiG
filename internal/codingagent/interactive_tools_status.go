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
	readCh := m.inputReadCh
	for _, initial := range initialStatuses {
		if status, ok := receiveDuringStartup(m, initial, &readCh); ok {
			m.showManagedToolStatus(status)
		}
	}
	for {
		status, ok := receiveDuringStartup(m, statuses, &readCh)
		if !ok {
			return
		}
		m.showManagedToolStatus(status)
	}
}

// receiveDuringStartup waits for one value while giving terminal input to the startup editor, as Pi's editor accepts text during managed-tool setup (interactive-mode.ts:944-947, 1017-1028).
func receiveDuringStartup[T any](m *InteractiveMode, ch <-chan T, readCh *chan inputChunk) (T, bool) {
	for {
		select {
		case value, ok := <-ch:
			return value, ok
		case input, ok := <-*readCh:
			if !ok {
				*readCh = nil
				continue
			}
			m.handleStartupInput(input, true)
		}
	}
}
