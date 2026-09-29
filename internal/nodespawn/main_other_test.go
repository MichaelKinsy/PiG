//go:build !windows

package nodespawn

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

const imageHelper = "PIG_NODESPAWN_IMAGE_HELPER"

// TestMain lets copies of the test binary stand in for spawned programs: one
// reports the image file it runs from, one its environment.
func TestMain(m *testing.M) {
	if os.Getenv(imageHelper) == "1" {
		image, err := os.Executable()
		if err != nil || json.NewEncoder(os.Stdout).Encode(image) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	testenv.ReportEnvironIfRequested()
	os.Exit(m.Run())
}
