package extensionconformance

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/test/extension-conformance/sdkscenarios"
)

// TestSDKScenarios runs the one table of conformance scenarios against every
// SDK through the real host and requires the same observation from each. A row
// listed in a scenario's Red map must produce exactly its recorded Got, so the
// lane that fixes it deletes the entry and a regression inside the red row is
// still caught; every other row must pass.
func TestSDKScenarios(t *testing.T) {
	for _, scenario := range sdkscenarios.All() {
		t.Run(scenario.Group+"/"+scenario.ID, func(t *testing.T) {
			for _, sdk := range sdkscenarios.SDKs {
				if _, no := scenario.Unrepresentable[sdk]; no {
					continue // a language limit, named with its reason in the scenario
				}
				t.Run(string(sdk), func(t *testing.T) {
					t.Parallel()
					got := runScenario(t, sdk, scenario)
					want := strings.Join(scenario.Want, "\n")
					have := strings.Join(got, "\n")
					red, expectedRed := scenario.Red[sdk]
					switch {
					case have == want && expectedRed:
						t.Fatalf("%s now matches Pi: delete its Red entry (lane %s: %s)", sdk, red.Lane, red.Why)
					case expectedRed && have != strings.Join(red.Got, "\n"):
						t.Fatalf("%s red row changed (lane %s: %s): update its Red Got if the change is intended\n--- want (Pi) ---\n%s\n--- red Got ---\n%s\n--- got ---\n%s", sdk, red.Lane, red.Why, want, strings.Join(red.Got, "\n"), have)
					case expectedRed:
						t.Logf("expected red (lane %s: %s):\n--- want ---\n%s\n--- %s ---\n%s", red.Lane, red.Why, want, sdk, have)
					case have != want:
						t.Fatalf("%s diverges from Pi (%s)\n--- want ---\n%s\n--- got ---\n%s", sdk, scenario.Pi, want, have)
					}
				})
			}
		})
	}
}

func runScenario(t *testing.T, sdk sdkscenarios.SDK, scenario sdkscenarios.Scenario) []string {
	t.Helper()
	rig := newScenarioRig(t, sdk, scenario)
	return rig.observe(scenario)
}
