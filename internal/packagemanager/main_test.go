package packagemanager

import (
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// The oracle tests point HOME at a fixture and start Pi under Node, which reads PI_CODING_AGENT_DIR and PI_HOME from the environment, so every agent-directory variable is scoped for the whole binary.
func TestMain(m *testing.M) {
	os.Exit(testenv.RunScoped(m, "pig-packagemanager-"))
}
