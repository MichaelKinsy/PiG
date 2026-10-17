package piglet

import (
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestMain scopes the temporary directory and every agent-directory variable to one directory the run removes, so a test that sets only PIG_HOME and then resolves a Piglet, which reads the project trust decision from the agent directory, cannot touch the directory a lane or a user exported.
func TestMain(m *testing.M) {
	os.Exit(testenv.RunScoped(m, "pig-piglet-"))
}

// Guards the TestMain wiring: without it these tests write the agent directory the environment names.
func TestAgentEnvIsIsolatedToThePackageRun(t *testing.T) {
	testenv.RequireScopedTempDir(t, "pig-piglet-")
	testenv.RequireIsolatedAgentEnv(t)
}
