// SPDX-License-Identifier: MIT

package ciimages

import (
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestMain scopes the temporary directory (testenv.RunScoped). TestPorterValidationIgnoresCallerConfiguration runs the real pig binary, whose
// extension host creates its per-user socket directory ($TMPDIR/pig-<uid>) and removes only the runtime directory inside it, so without the scope
// that directory stays in the shared temporary directory after every run.
func TestMain(m *testing.M) {
	os.Exit(testenv.RunScoped(m, "pig-ci-images-"))
}
