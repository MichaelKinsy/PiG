// SPDX-License-Identifier: MIT

package tui

import (
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestMain fails the package's test run when any test leaks a goroutine.
func TestMain(m *testing.M) { os.Exit(testenv.RunWithoutGoroutineLeaks(m)) }
