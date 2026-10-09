package cli

import (
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// apiStripTag is the build tag that compiles api out of a Piglet Binary.
func apiStripTag(t *testing.T, api ai.API) string {
	t.Helper()
	if !slices.Contains(ai.StrippableAPIs(), api) {
		t.Fatalf("%s is not a strippable API", api)
	}
	return pigstrip.Tag(string(api))
}

// countLinkedWithPrefix counts the packages in deps whose import path starts with one of prefixes.
func countLinkedWithPrefix(deps []string, prefixes ...string) int {
	n := 0
	for _, dep := range deps {
		for _, prefix := range prefixes {
			if strings.HasPrefix(dep, prefix) {
				n++
				break
			}
		}
	}
	return n
}

// A pig_strip_bedrock_converse_stream build compiles ai/bedrock*.go out, so cmd/pig links neither the AWS SDK nor smithy.
func TestStripBedrockConverseStreamBuildOmitsAWSSDK(t *testing.T) {
	tag := apiStripTag(t, ai.APIBedrockConverseStream)
	if tag != "pig_strip_bedrock_converse_stream" {
		t.Fatalf("tag = %q", tag)
	}
	stock := goListCmdPig(t, "", "-deps")
	for _, pkg := range []string{"github.com/aws/aws-sdk-go-v2/service/bedrockruntime", "github.com/aws/smithy-go/transport/http"} {
		if !slices.Contains(stock, pkg) {
			t.Errorf("the stock cmd/pig does not link %s", pkg)
		}
	}
	if n := countLinkedWithPrefix(goListCmdPig(t, tag, "-deps"), "github.com/aws/"); n != 0 {
		t.Errorf("a %s build of cmd/pig links %d github.com/aws/ packages", tag, n)
	}
}

// A pig_strip_google_vertex build compiles ai/google_vertex.go out, so cmd/pig links neither golang.org/x/oauth2 (Application
// Default Credentials) nor the GCE metadata client.
func TestStripGoogleVertexBuildOmitsOAuth2(t *testing.T) {
	tag := apiStripTag(t, ai.APIGoogleVertex)
	stock := goListCmdPig(t, "", "-deps")
	for _, pkg := range []string{"golang.org/x/oauth2/google", "cloud.google.com/go/compute/metadata"} {
		if !slices.Contains(stock, pkg) {
			t.Errorf("the stock cmd/pig does not link %s", pkg)
		}
	}
	if n := countLinkedWithPrefix(goListCmdPig(t, tag, "-deps"), "golang.org/x/oauth2", "cloud.google.com/go/compute/metadata"); n != 0 {
		t.Errorf("a %s build of cmd/pig links %d oauth2/metadata packages", tag, n)
	}
}

// Mistral uses no package of its own, so a pig_strip_mistral_conversations build drops files: the ai package compiles
// mistral_stripped.go instead of the provider's mistral.go, mistral_reader.go and mistral_response_body.go.
func TestStripMistralConversationsBuildOmitsMistralFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("go list in -short mode")
	}
	tag := apiStripTag(t, ai.APIMistralConversations)
	goFiles := func(tags ...string) []string {
		args := append(append([]string{"list"}, tags...), "-f", `{{join .GoFiles "\n"}}`, "github.com/MichaelKinsy/PiG/ai")
		out, err := exec.Command("go", args...).Output()
		if err != nil {
			t.Fatalf("go %s: %v", strings.Join(args, " "), err)
		}
		return strings.Fields(string(out))
	}
	provider := []string{"mistral.go", "mistral_reader.go", "mistral_response_body.go"}
	stock, stripped := goFiles(), goFiles("-tags", tag)
	for _, file := range provider {
		if !slices.Contains(stock, file) {
			t.Errorf("the stock ai package does not compile %s", file)
		}
		if slices.Contains(stripped, file) {
			t.Errorf("a %s build of ai compiles %s", tag, file)
		}
	}
	if slices.Contains(stock, "mistral_stripped.go") || !slices.Contains(stripped, "mistral_stripped.go") {
		t.Errorf("mistral_stripped.go: stock %v, stripped %v", slices.Contains(stock, "mistral_stripped.go"), slices.Contains(stripped, "mistral_stripped.go"))
	}
}

// The stripped Binary reports a stripped API when a print-mode run selects one of its catalog models, and exits
// non-zero before any request. Its --list-models lists none of those models, though their provider is authenticated.
func TestStrippedBinaryReportsStrippedAPIs(t *testing.T) {
	for _, tc := range []struct {
		model, provider string
		api             ai.API
		env             map[string]string
	}{
		{"amazon-bedrock/amazon.nova-2-lite-v1:0", "amazon-bedrock", ai.APIBedrockConverseStream, map[string]string{"AWS_ACCESS_KEY_ID": "x", "AWS_SECRET_ACCESS_KEY": "y", "AWS_REGION": "us-east-1"}},
		{"google-vertex/gemini-2.5-flash", "google-vertex", ai.APIGoogleVertex, map[string]string{"GOOGLE_CLOUD_API_KEY": "x"}},
		{"mistral/codestral-latest", "mistral", ai.APIMistralConversations, map[string]string{"MISTRAL_API_KEY": "x"}},
	} {
		t.Run(string(tc.api), func(t *testing.T) {
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			stdout, stderr, code := runStrippedPig(t, "", "-p", "--model", tc.model, "hi")
			want := "Provider " + tc.provider + ": API " + string(tc.api) + " is stripped from this Piglet (strip.apis: " + string(tc.api) + ")"
			if code == 0 || !strings.Contains(stdout+stderr, want) {
				t.Fatalf("exit %d, stdout %q, stderr %q; want non-zero exit reporting %q", code, stdout, stderr, want)
			}
			stdout, stderr, code = runStrippedPig(t, "", "--list-models")
			if code != 0 {
				t.Fatalf("--list-models: exit %d, stderr %q", code, stderr)
			}
			for line := range strings.SplitSeq(stdout, "\n") {
				if provider, _, _ := strings.Cut(line, " "); provider == tc.provider {
					t.Fatalf("--list-models lists a model of stripped %s: %q", tc.api, line)
				}
			}
		})
	}
}
