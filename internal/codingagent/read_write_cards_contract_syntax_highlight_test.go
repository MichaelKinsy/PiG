//go:build !pig_strip_syntax_highlight

package codingagent

import (
	"testing"
)

// The read and write tool cards (renderers/read.ts and renderers/write.ts, with render-utils.ts' path, range and output helpers) against pinned Pi over
// readWriteCardProbes. Rows are compared byte for byte: colours, backgrounds and padding included.
func TestReadWriteCardsMatchPi(t *testing.T) {
	probes, names := readWriteCardProbes(t)
	compareToolCards(t, probes, names)
}
