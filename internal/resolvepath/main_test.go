package resolvepath

import (
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// The oracle test points HOME at a temporary directory and starts Pi under Node, which reads the agent-directory variables from the environment, so every one of them is scoped for the whole binary.
func TestMain(m *testing.M) {
	os.Exit(testenv.RunScoped(m, "pig-resolvepath-"))
}
