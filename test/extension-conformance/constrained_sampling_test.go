package extensionconformance

import "testing"

// Pi types.ts:484 explicitly accepts false independently of an omitted sampling value.
func TestConstrainedSamplingFalseAcrossSDKs(t *testing.T) {
	t.Parallel()
	cases := allHarnessCases()
	for _, language := range []string{"go", "python", "rust"} {
		cases = append(cases, harnessCase{name: "packed-" + language, make: func(t *testing.T) *harness { return makePackedUIHarness(t, language) }})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			got := toolConstrainedSampling(h.runner)
			if got["sampling_disabled"] != "false" {
				t.Fatalf("sampling_disabled = %q, want explicit false", got["sampling_disabled"])
			}
			if _, present := got["echo"]; present {
				t.Fatalf("unset echo sampling became present: %s", got["echo"])
			}
		})
	}
}
