package codingagent

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// ensureManagedTools awaits both installers before enabling input. Pi executes each ensureTool up to its first await in Promise.all argument order (fd, then rg): a present tool, or one offline mode does not download, reports its status and settles there, without reading input. Only downloads run concurrently, sharing the startup cancellation lifetime, while the owner goroutine gives terminal input to the startup editor and renders each status as it arrives.
func (m *InteractiveMode) ensureManagedTools(ctx context.Context, tm *tools.ToolsManager) {
	var downloads []func(context.Context, func(tools.ToolStatus)) string
	for _, name := range []string{"fd", "rg"} {
		if _, download := tm.BeginEnsureTool(name, m.showManagedToolStatus); download != nil {
			downloads = append(downloads, download)
		}
	}
	if len(downloads) == 0 {
		return
	}
	statuses := make(chan tools.ToolStatus)
	var wg sync.WaitGroup
	for _, download := range downloads {
		wg.Go(func() { download(ctx, func(status tools.ToolStatus) { statuses <- status }) })
	}
	go func() {
		wg.Wait()
		close(statuses)
	}()
	readCh := m.inputReadCh
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
