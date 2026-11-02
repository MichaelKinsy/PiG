//go:build !pig_strip_bedrock_converse_stream

package bedrock

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The Bedrock oracle tests compare PiG with Pi's own bedrock-converse-stream running under Node. Both sides read the process environment, and an ambient AWS variable
// changes what each does. Pi's side: the AWS SDK selects bearer auth when AWS_BEARER_TOKEN_BEDROCK is merely present (@aws-sdk/core NODE_AUTH_SCHEME_PREFERENCE_OPTIONS,
// `key in env`), even empty, and the request then fails signing before it is sent (PiG's D195 is the deliberate difference). PiG's side: under skip-auth a token in the
// environment keeps the Go SDK's HTTPS-only signer, so no request reaches the loopback server (D196). The live-providers workflow maps every unset AWS secret to an empty
// variable, and a configured one to a real value, so its environment differs from CI's. The oracle tests are therefore a function of the checkout on both sides:
// withoutAWSEnvironment starts Pi's client without the caller's AWS variables, and clearAWSEnvironment unsets them for PiG's.

// ambientAWSEnvironments are what the live-providers workflow exports to its test step: each variable present, empty when no secret is configured, and a bearer token, a
// profile and static keys of the real-looking kind when one is.
var ambientAWSEnvironments = []struct {
	name        string
	environment []string
}{
	{"no secret configured", []string{
		"AWS_PROFILE=", "AWS_ACCESS_KEY_ID=", "AWS_SECRET_ACCESS_KEY=", "AWS_SESSION_TOKEN=", "AWS_BEARER_TOKEN_BEDROCK=",
		"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI=", "AWS_CONTAINER_CREDENTIALS_FULL_URI=", "AWS_WEB_IDENTITY_TOKEN_FILE=", "AWS_REGION=", "AWS_DEFAULT_REGION=",
	}},
	{"secrets configured", []string{
		"AWS_PROFILE=live", "AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE", "AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "AWS_SESSION_TOKEN=IQoJb3JpZ2luX2VjEXAMPLE",
		"AWS_BEARER_TOKEN_BEDROCK=ABSKQmVkcm9ja0FQSUtleS1leGFtcGxlLXRva2Vu", "AWS_REGION=eu-west-1", "AWS_DEFAULT_REGION=eu-west-1", "AWS_BEDROCK_SKIP_AUTH=0",
	}},
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

// clearAWSEnvironment unsets every AWS_ variable of the test process until the test ends. Variables the test needs are set after it.
func clearAWSEnvironment(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(name, "AWS_") {
			continue
		}
		t.Setenv(name, "") // registers the restore; the variable must then be unset, not empty
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
}

// bedrockResponseProbeCountVariable lets a caller shorten the response oracle; the full 1000 probes take about 15 s.
const bedrockResponseProbeCountVariable = "PIG_TEST_BEDROCK_RESPONSE_PROBES"

func bedrockResponseProbeCount() int {
	if n, err := strconv.Atoi(os.Getenv(bedrockResponseProbeCountVariable)); err == nil && n > 0 {
		return n
	}
	return 1000
}

// Each oracle test runs again in a child test binary whose environment carries the live-providers workflow's AWS variables, empty and set; it must pass there as it does
// here. The response oracle runs 20 of its 1000 probes: the environment decides whether Pi answers any of them.
func TestBedrockOraclesIgnoreAmbientAWSEnvironment(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	oracles := []string{"TestBedrockUserAgentMatchesPi", "TestBedrockResponseStreamMatchesPi", "TestBedrockRequestBodyMatchesPi"}
	for _, ambient := range ambientAWSEnvironments {
		t.Run(ambient.name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), executable, "-test.count=1", "-test.v", "-test.timeout", (2 * time.Minute).String(), "-test.run", "^("+strings.Join(oracles, "|")+")$")
			cmd.Env = append(withoutAWSEnvironment(os.Environ()), ambient.environment...)
			cmd.Env = append(cmd.Env, bedrockResponseProbeCountVariable+"=20", keepAWSEnvironmentVariable+"=1")
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("oracle tests under the live-providers environment: %v\n%s", err, output)
			}
			for _, name := range oracles {
				if !strings.Contains(string(output), "--- PASS: "+name+" ") {
					t.Errorf("%s did not run and pass in the child test binary:\n%s", name, output)
				}
			}
		})
	}
}

// A package run starts without the caller's AWS variables (TestMain), so a configured AWS_BEARER_TOKEN_BEDROCK, profile or static keys cannot change what a
// hermetic test does: under skip-auth over http:// PiG refuses to send a token (D196), and a wire test then waits for a request that never arrives.
func TestBedrockPackageRunsWithoutAmbientAWSEnvironment(t *testing.T) {
	if os.Getenv(keepAWSEnvironmentVariable) != "" {
		t.Skip("the caller's AWS environment is kept on purpose")
	}
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "AWS_") {
			t.Errorf("%s is set in the test process", strings.SplitN(entry, "=", 2)[0])
		}
	}
}

// The same test in a child test binary that is started with every AWS variable the workflow can export, real-looking values included.
func TestBedrockTestMainClearsConfiguredAWSEnvironment(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, ambient := range ambientAWSEnvironments {
		t.Run(ambient.name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), executable, "-test.count=1", "-test.v", "-test.timeout", (2 * time.Minute).String(), "-test.run", "^TestBedrockPackageRunsWithoutAmbientAWSEnvironment$")
			cmd.Env = append(withoutAWSEnvironment(os.Environ()), ambient.environment...)
			output, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(output), "--- PASS: TestBedrockPackageRunsWithoutAmbientAWSEnvironment ") {
				t.Fatalf("child test binary: %v\n%s", err, output)
			}
		})
	}
}
