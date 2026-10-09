package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// A Piglet's strip.apis disables an API when BuildModel builds the provider for one of its models, the way a Piglet
// Binary that compiles the API out does; models of other APIs still build.
func TestBuildModelReportsStrippedAPI(t *testing.T) {
	svcs, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ spec, provider, api string }{
		{"amazon-bedrock/amazon.nova-2-lite-v1:0", "amazon-bedrock", "bedrock-converse-stream"},
		{"google-vertex/gemini-2.5-flash", "google-vertex", "google-vertex"},
		{"mistral/codestral-latest", "mistral", "mistral-conversations"},
	} {
		t.Run(tc.api, func(t *testing.T) {
			if !pigstrip.Has(pigstrip.ListAPIs, tc.api) {
				if _, err := BuildModel(tc.spec, svcs); err != nil {
					t.Fatalf("unstripped BuildModel(%s): %v", tc.spec, err)
				}
			}
			t.Cleanup(pigstrip.Strip(pigstrip.ListAPIs, tc.api))
			want := "Provider " + tc.provider + ": API " + tc.api + " is stripped from this Piglet (strip.apis: " + tc.api + ")"
			if _, err := BuildModel(tc.spec, svcs); err == nil || err.Error() != want {
				t.Fatalf("stripped BuildModel(%s) error = %v, want %q", tc.spec, err, want)
			}
			if _, err := BuildModel("anthropic/claude-haiku-4-5", svcs); err != nil {
				t.Fatalf("BuildModel(anthropic) with %s stripped: %v", tc.api, err)
			}
		})
	}
}
