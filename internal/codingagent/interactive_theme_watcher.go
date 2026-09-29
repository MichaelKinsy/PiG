// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts.
package codingagent

import (
	"context"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/tui"
)

func (m *InteractiveMode) startThemeWatcher(ctx context.Context) *tui.ThemeWatcher {
	return tui.StartThemeWatcher(ctx, filepath.Join(m.opts.AgentDir, "themes"), func(ctx context.Context, apply func()) error {
		return m.postToMain(ctx, func() {
			if ctx.Err() != nil {
				return
			}
			before := tui.ActiveTheme()
			apply()
			if tui.ActiveTheme() != before {
				// The editor resolves its border from ActiveTheme at render time; invalidation refreshes cached transcript components without clearing scrollback.
				m.tuiInst.Invalidate()
				m.tuiInst.RequestRender()
			}
		})
	})
}
