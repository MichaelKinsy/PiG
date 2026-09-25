package experimental

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPinnedUpstreamRadiusSource(t *testing.T) {
	command := exec.CommandContext(t.Context(), "node", "internal/experimental/testdata/radius_upstream_probe.cjs")
	command.Dir = "../.."
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "PI_OFFLINE=") {
			command.Env = append(command.Env, entry)
		}
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("pinned upstream probe: %v\n%s", err, output)
	}
	if string(output) != "pinned upstream Radius auth/envelope/host/final-chunk/backpressure: pass\n" {
		t.Fatalf("unexpected probe output: %s", output)
	}
}
