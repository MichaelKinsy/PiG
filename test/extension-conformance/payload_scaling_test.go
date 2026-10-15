package extensionconformance

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/sdkscenarios"
)

const (
	// payloadSmall and payloadLarge are the echoed text sizes whose times are compared. A linear SDK and host take about payloadLarge/payloadSmall times as long for the larger one.
	// The sizes are 16x apart so a quadratic term outgrows the linear cost it hides behind at small sizes: a Python reader that grows the frame by bytes concatenation in 4 KiB chunks measured x18.8 between 1 and 8 MiB, under a limit of 3x linear, and x85 between 1 and 16 MiB.
	payloadSmall = 1 << 20
	payloadLarge = 16 << 20
	// payloadScalingFactor is the slowdown of the large echo, relative to the small one, that counts as super-linear copying (a quadratic buffer append or a repeated re-encode). Linear is 16; the margin, three times linear, covers scheduler noise on a loaded host.
	payloadScalingFactor = 48.0
	// payloadCeiling is the longest one large echo may take in any SDK: a gross bound, not a ratchet, since the host's load varies.
	payloadCeiling = 30 * time.Second
	payloadRuns    = 3
)

// TestExtensionPayloadScalesLinearly echoes 1 MiB and 8 MiB of text through every SDK's tool and requires the time to grow about in proportion to the size, so a payload copy that is quadratic in the SDK's reader, writer or JSON codec fails here and not as a hang on a large file read.
func TestExtensionPayloadScalesLinearly(t *testing.T) {
	if testing.Short() {
		t.Skip("builds subprocess fixtures")
	}
	for _, sdk := range sdkscenarios.SDKs {
		t.Run(string(sdk), func(t *testing.T) {
			rig := newScenarioRig(t, sdk, sdkscenarios.Scenario{Spec: soakSpec()})
			if len(rig.loadFailure) != 0 {
				t.Fatalf("load: %v", rig.loadFailure)
			}
			echo := func(size int) time.Duration {
				text := strings.Repeat("abcdefghij klmnop\"é\n", size/20+1)[:size]
				params, err := json.Marshal(map[string]any{"text": text})
				if err != nil {
					t.Fatal(err)
				}
				best := time.Duration(1<<63 - 1)
				for range payloadRuns {
					ctx, cancel := context.WithTimeout(t.Context(), payloadCeiling)
					start := time.Now()
					result, err := rig.primary().Tools["echo"].Definition.Execute(ctx, "call", params, nil)
					elapsed := time.Since(start)
					cancel()
					if err != nil {
						t.Fatalf("echo of %d bytes: %v", size, err)
					}
					if got := result.Text(); got != "echo:"+text {
						t.Fatalf("echo of %d bytes came back as %d bytes", size, len(got))
					}
					best = min(best, elapsed)
				}
				return best
			}
			echo(1 << 16)
			small, large := echo(payloadSmall), echo(payloadLarge)
			t.Logf("%s: %d MiB in %v, %d MiB in %v (x%.1f for x%d the bytes)", sdk, payloadSmall>>20, small, payloadLarge>>20, large, float64(large)/float64(small), payloadLarge/payloadSmall)
			testenv.TimingGate(t, large > payloadCeiling, "echoing %d MiB took %v, over the %v ceiling", payloadLarge>>20, large, payloadCeiling)
			ratio := float64(large) / float64(small)
			testenv.TimingGate(t, ratio > payloadScalingFactor, "echoing %d times the bytes took %.1f times as long (limit %.0f): a copy in the SDK or host is super-linear", payloadLarge/payloadSmall, ratio, payloadScalingFactor)
		})
	}
}
