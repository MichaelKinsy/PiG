package ai

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	smithymiddleware "github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

func isolateBedrockConfig(t *testing.T) {
	t.Helper()
	for _, key := range []string{"AWS_REGION", "AWS_DEFAULT_REGION", "AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_BEARER_TOKEN_BEDROCK", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_BEDROCK_SKIP_AUTH"} {
		t.Setenv(key, "")
	}
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	path := filepath.Join(dir, "credentials")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", path)
	text := ""
	for _, profile := range []string{"explicit-profile", "scoped-profile", "ambient-profile", "bedrock-profile", "scoped-bedrock-profile", "ambient-bedrock-profile"} {
		text += "[" + profile + "]\naws_access_key_id = " + profile + "\naws_secret_access_key = profile-secret\n"
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func bedrockHeaderRegistration(t *testing.T, headers ProviderHeaders) (*smithymiddleware.Stack, smithymiddleware.BuildMiddleware) {
	t.Helper()
	stack := smithymiddleware.NewStack("test", smithyhttp.NewStackRequest)
	if err := withBedrockHeaders(headers)(stack); err != nil {
		t.Fatal(err)
	}
	registration, _ := stack.Build.Get("pi-ai-custom-headers")
	return stack, registration
}

func applyBedrockHeaderRegistration(t *testing.T, registration smithymiddleware.BuildMiddleware, request any) {
	t.Helper()
	if registration == nil {
		t.Fatal("missing custom-headers build middleware")
	}
	calls := 0
	_, _, err := registration.HandleBuild(t.Context(), smithymiddleware.BuildInput{Request: request}, smithymiddleware.BuildHandlerFunc(func(_ context.Context, input smithymiddleware.BuildInput) (smithymiddleware.BuildOutput, smithymiddleware.Metadata, error) {
		calls++
		if input.Request != request {
			t.Error("next received a different request")
		}
		return smithymiddleware.BuildOutput{}, smithymiddleware.Metadata{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("next calls=%d, want 1", calls)
	}
}

func TestBedrockUpstreamCustomHeaders(t *testing.T) {
	for _, name := range []string{
		// .upstream/v0.87.1/packages/ai/test/bedrock-custom-headers.test.ts:90
		"VC1: registers a build-step middleware that injects the caller header (happy path)",
		// .upstream/v0.87.1/packages/ai/test/bedrock-custom-headers.test.ts:185
		"VC4: streamSimpleBedrock forwards headers end-to-end (regression guard)",
	} {
		t.Run(name, func(t *testing.T) {
			stack, registration := bedrockHeaderRegistration(t, ProviderHeadersFromStrings(map[string]string{"x-custom": "v"}))
			if got := stack.Build.List(); !reflect.DeepEqual(got, []string{"pi-ai-custom-headers"}) {
				t.Fatalf("build registrations=%v", got)
			}
			request := smithyhttp.NewStackRequest().(*smithyhttp.Request)
			applyBedrockHeaderRegistration(t, registration, request)
			if request.Header.Get("x-custom") != "v" {
				t.Fatalf("headers=%v", request.Header)
			}
			isolateBedrockConfig(t)
			server, requests := rejectingProviderServer(t)
			options := StreamOptions{Headers: ProviderHeadersFromStrings(map[string]string{"x-custom": "v"}), Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1"}}
			transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}})
			var stream *AssistantMessageEventStream
			var err error
			if strings.HasPrefix(name, "VC4:") {
				model := cloneGeneratedModel(t, "amazon-bedrock/us.anthropic.claude-opus-4-8").ToModel()
				model.ProviderMeta.BaseURL = server.URL
				stream, err = StreamSimple(t.Context(), model, transcript, options)
			} else {
				stream, err = NewBedrockProvider("us.anthropic.claude-opus-4-8", server.URL).Stream(t.Context(), transcript, options)
			}
			if stream != nil {
				if result := stream.Result(); result.StopReason != StopReasonError {
					t.Fatalf("expected mock send rejection, got %+v", result)
				}
			} else if err == nil {
				t.Fatal("expected mock send rejection")
			}
			select {
			case request := <-requests:
				if got := request.header.Get("x-custom"); got != "v" {
					t.Fatalf("wire x-custom = %q", got)
				}
			default:
				t.Fatalf("request did not reach transport: %v", err)
			}
		})
	}
	// .upstream/v0.87.1/packages/ai/test/bedrock-custom-headers.test.ts:110
	t.Run("VC2: skips reserved headers case-insensitively while applying allowed ones", func(t *testing.T) {
		_, registration := bedrockHeaderRegistration(t, ProviderHeadersFromStrings(map[string]string{"authorization": "evil", "x-amz-date": "evil", "x-allowed": "ok", "Authorization": "evil2", "X-Amz-Date": "evil2", "HOST": "evil3"}))
		request := smithyhttp.NewStackRequest().(*smithyhttp.Request)
		request.Header = http.Header{"Authorization": []string{"real-auth"}, "X-Amz-Date": []string{"real-date"}, "Host": []string{"real-host"}}
		applyBedrockHeaderRegistration(t, registration, request)
		want := http.Header{"Authorization": []string{"real-auth"}, "X-Amz-Date": []string{"real-date"}, "Host": []string{"real-host"}, "X-Allowed": []string{"ok"}}
		if !reflect.DeepEqual(request.Header, want) {
			t.Fatalf("headers=%v, want %v", request.Header, want)
		}
	})
	for _, tc := range []struct {
		name    string
		headers ProviderHeaders
	}{
		// .upstream/v0.87.1/packages/ai/test/bedrock-custom-headers.test.ts:154
		{"VC3: registers no middleware when headers is undefined", nil},
		// .upstream/v0.87.1/packages/ai/test/bedrock-custom-headers.test.ts:160
		{"VC3: registers no middleware when headers is empty", ProviderHeaders{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stack, registration := bedrockHeaderRegistration(t, tc.headers)
			if registration != nil || len(stack.Build.List()) != 0 {
				t.Fatalf("unexpected registration %v", stack.Build.List())
			}
		})
	}
	// .upstream/v0.87.1/packages/ai/test/bedrock-custom-headers.test.ts:166
	t.Run("VC3 (structural guard): passes through unchanged when the request has no headers", func(t *testing.T) {
		_, registration := bedrockHeaderRegistration(t, ProviderHeadersFromStrings(map[string]string{"x-custom": "v"}))
		request := smithyhttp.NewStackRequest().(*smithyhttp.Request)
		request.Header = nil
		applyBedrockHeaderRegistration(t, registration, request)
		applyBedrockHeaderRegistration(t, registration, nil)
	})
}
