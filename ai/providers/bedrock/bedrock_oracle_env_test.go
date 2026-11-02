//go:build !pig_strip_bedrock_converse_stream

package bedrock

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The Bedrock oracle tests compare PiG with Pi's own bedrock-converse-stream running under Node. Pi's client reads the process environment, and an ambient AWS
// variable changes what it does: the AWS SDK selects bearer auth when AWS_BEARER_TOKEN_BEDROCK is merely present (@aws-sdk/core NODE_AUTH_SCHEME_PREFERENCE_OPTIONS,
// `key in env`), even empty, and the request then fails signing before it is sent (PiG's D195 is the deliberate difference). The live-providers workflow maps every
// unset AWS secret to an empty variable, so its environment differs from CI's, and an oracle that inherits it compares PiG with an error Pi never returns on a
// developer machine. The oracle tests are therefore a function of the checkout, not of the caller's AWS configuration.

// ambientAWSEnvironment is what the live-providers workflow exports to its test step when no AWS secret is configured: each variable present and empty.
var ambientAWSEnvironment = []string{
	"AWS_PROFILE=", "AWS_ACCESS_KEY_ID=", "AWS_SECRET_ACCESS_KEY=", "AWS_SESSION_TOKEN=", "AWS_BEARER_TOKEN_BEDROCK=",
	"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI=", "AWS_CONTAINER_CREDENTIALS_FULL_URI=", "AWS_WEB_IDENTITY_TOKEN_FILE=", "AWS_REGION=", "AWS_DEFAULT_REGION=",
}

// withoutAWSEnvironment is environment with every AWS_ variable removed. The Pi oracle scripts set the AWS variables they need themselves.
func withoutAWSEnvironment(environment []string) []string {
	kept := make([]string, 0, len(environment))
	for _, entry := range environment {
		if !strings.HasPrefix(entry, "AWS_") {
			kept = append(kept, entry)
		}
	}
	return kept
}

// Each oracle test runs again in a child test binary whose environment carries the live-providers workflow's empty AWS variables; it must pass there as it does here.
func TestBedrockOraclesIgnoreAmbientAWSEnvironment(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	oracles := []string{"TestBedrockUserAgentMatchesPi", "TestBedrockResponseStreamMatchesPi", "TestBedrockRequestBodyMatchesPi"}
	cmd := exec.CommandContext(t.Context(), executable, "-test.count=1", "-test.v", "-test.run", "^("+strings.Join(oracles, "|")+")$")
	cmd.Env = append(withoutAWSEnvironment(os.Environ()), ambientAWSEnvironment...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("oracle tests under the live-providers environment: %v\n%s", err, output)
	}
	for _, name := range oracles {
		if !strings.Contains(string(output), "--- PASS: "+name+" ") {
			t.Errorf("%s did not run and pass in the child test binary:\n%s", name, output)
		}
	}
}
