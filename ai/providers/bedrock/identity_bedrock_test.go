//go:build !pig_strip_bedrock_converse_stream

package bedrock

import (
	"github.com/MichaelKinsy/PiG/ai"

	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
)

// Pi adc5fa9f: bedrock-converse-stream.ts bedrockUserAgent sends the client identity as AWS SDK user agent pairs: name and
// version for the detailed form, the name alone for the short form, and the custom string for a custom identity.
func TestBedrockUserAgentCarriesTheClientIdentity(t *testing.T) {
	cases := []struct {
		name     string
		identity *ai.ClientIdentity
		want     string
		absent   string
	}{
		{"default is the detailed form", nil, pigidentity.UserAgentProduct + "/" + ai.ProductVersion, ""},
		{"version override", &ai.ClientIdentity{Version: new("9.9.9")}, pigidentity.UserAgentProduct + "/9.9.9", ai.ProductVersion},
		{"short form carries the name alone", &ai.ClientIdentity{UserAgent: new(ai.UserAgentShort), Version: new("9.9.9")}, pigidentity.UserAgentProduct, "9.9.9"},
		{"custom string", &ai.ClientIdentity{UserAgent: new(ai.UserAgentCustom("acme/1.0"))}, "acme/1.0", pigidentity.UserAgentProduct + "/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateBedrockConfig(t)
			server, requests := rejectingProviderServer(t)
			options := ai.StreamOptions{Identity: tc.identity, Env: ai.ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1"}}
			stream, err := NewBedrockProvider("us.anthropic.claude-opus-4-8", server.URL).Stream(t.Context(), providerWireTranscript(), options)
			if err != nil {
				t.Fatal(err)
			}
			stream.Result()
			select {
			case request := <-requests:
				userAgent := request.header.Get("User-Agent")
				if !strings.Contains(userAgent, tc.want) {
					t.Errorf("User-Agent = %q, want it to contain %q", userAgent, tc.want)
				}
				if tc.absent != "" && strings.Contains(userAgent, tc.absent) {
					t.Errorf("User-Agent = %q, must not contain %q", userAgent, tc.absent)
				}
			default:
				t.Fatal("no request reached the server")
			}
		})
	}
}
