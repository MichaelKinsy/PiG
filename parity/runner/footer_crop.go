//go:build parity

package runner

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// validateCWDFooterCrop requires the complete canonical cwd and configured branch to fit when the crop uses the snapshot name. This conservative bound does not depend on HOME abbreviation. Pi truncates the cwd row to terminal width; an invisible anchor can otherwise match the startup resource listing instead of the footer.
func validateCWDFooterCrop(cwd string, cfg TmuxDriverConfig) error {
	if cfg.CaptureStart != "parity-snap-cwd-" {
		return nil
	}
	line := cwd
	if cfg.GitBranch != "" {
		line += " (" + cfg.GitBranch + ")"
	}
	if needed := widthx.VisibleWidth(line); needed > cfg.Width {
		return fmt.Errorf("footer cwd crop %q requires %d columns for %q, but terminal has %d columns; set TMPDIR to a shorter clean root or increase tmux.width", cfg.CaptureStart, needed, line, cfg.Width)
	}
	return nil
}
