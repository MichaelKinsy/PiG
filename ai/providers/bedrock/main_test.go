package bedrock

import (
	"os"
	"strings"
	"testing"
)

// liveAWSEnvironment is the caller's AWS_ variables as the test process started, name=value. TestMain removes them from the process; the live tests, which
// call the real service with the caller's credentials, put them back (useLiveAWSEnvironment).
var liveAWSEnvironment []string

// keepAWSEnvironmentVariable tells TestMain to leave the process's AWS variables alone. TestBedrockOraclesIgnoreAmbientAWSEnvironment sets it for its child so
// that the oracles meet the workflow's environment, which TestMain would otherwise remove before they run.
const keepAWSEnvironmentVariable = "PIG_TEST_BEDROCK_KEEP_AWS_ENVIRONMENT"

// TestMain starts a package run without the caller's AWS variables. Every other test here talks to a loopback server or builds a request, and an ambient variable
// changes what PiG's client does: under skip-auth over http:// a configured AWS_BEARER_TOKEN_BEDROCK is refused (D196), so a wire test waits for a request that never
// arrives, and a profile or static keys change which credentials sign. The live-providers workflow exports every AWS secret, empty when it is not configured and the
// real value when it is. The tests that need a variable set it themselves.
func TestMain(m *testing.M) {
	if os.Getenv(keepAWSEnvironmentVariable) == "" {
		for _, entry := range os.Environ() {
			if name, _, _ := strings.Cut(entry, "="); strings.HasPrefix(name, "AWS_") {
				liveAWSEnvironment = append(liveAWSEnvironment, entry)
				if err := os.Unsetenv(name); err != nil {
					panic(err)
				}
			}
		}
	}
	os.Exit(m.Run())
}
