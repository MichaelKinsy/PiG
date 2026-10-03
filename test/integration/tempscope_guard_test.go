//go:build integration

package integration

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Guards the TestMain temporary-directory scope: pig and tmux children write scratch files under the run's directory, which TestMain removes.
func TestTempDirIsScopedToTheIntegrationRun(t *testing.T) {
	testenv.RequireScopedTempDir(t, "pig-integration-")
}
