package codingagent

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
)

// piUserAgent matches the Pi user agents: a bare "pi" or pi/<version> (...).
var piUserAgent = regexp.MustCompile(`^[Pp][Ii]([ /(]|$)`)

// A Node extension's request names its client identity (core/sdk.ts passes getClientIdentity for a Session from createAgentSession). The host must hand it to the provider:
// dropping it sends no attribution, and replacing it with a default would send Pi's.
func TestSubprocessStreamOptionsCarryTheRequestIdentity(t *testing.T) {
	wire := func(t *testing.T, text string) map[string]any {
		t.Helper()
		var request map[string]any
		if err := json.Unmarshal([]byte(text), &request); err != nil {
			t.Fatal(err)
		}
		return request
	}
	attribution := ai.ClientAttribution{Name: pigidentity.AttributionName, URL: pigidentity.AttributionURL, Categories: []string{pigidentity.AttributionCategory}}
	headers := map[string]string{"HTTP-Referer": pigidentity.AttributionURL, "X-OpenRouter-Title": pigidentity.AttributionName, "X-OpenRouter-Categories": pigidentity.AttributionCategory}

	t.Run("attribution reaches the OpenRouter headers", func(t *testing.T) {
		request := wire(t, `{"identity":{"userAgent":"detailed","version":"9.9.9","attribution":{"name":"`+attribution.Name+`","url":"`+attribution.URL+`","categories":["`+pigidentity.AttributionCategory+`"]}}}`)
		options, err := subprocessStreamOptions(request, ai.StreamOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if options.Identity == nil || options.Identity.AttributionNull || options.Identity.Attribution == nil {
			t.Fatalf("identity = %+v, want the request's attribution", options.Identity)
		}
		got := ai.GetAttributionHeaders(ai.ClientIdentityTarget{Provider: "openrouter"}, options.Identity)
		for name, want := range headers {
			if got[name] != want {
				t.Errorf("%s = %q, want %q (PiG's attribution, not Pi's or empty)", name, got[name], want)
			}
		}
		if ua := ai.FormatUserAgent(options.Identity); ua == "" || piUserAgent.MatchString(ua) {
			t.Errorf("User-Agent = %q, want PiG's with the request's version", ua)
		}
	})

	t.Run("an explicit null attribution turns attribution off", func(t *testing.T) {
		options, err := subprocessStreamOptions(wire(t, `{"identity":{"userAgent":"detailed","attribution":null}}`), ai.StreamOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if options.Identity == nil || !options.Identity.AttributionNull {
			t.Fatalf("identity = %+v, want AttributionNull", options.Identity)
		}
		if got := ai.GetAttributionHeaders(ai.ClientIdentityTarget{Provider: "openrouter"}, options.Identity); len(got) != 0 {
			t.Errorf("attribution headers = %v, want none", got)
		}
	})

	t.Run("no identity leaves the provider defaults", func(t *testing.T) {
		for _, text := range []string{`{}`, `{"identity":null}`} {
			options, err := subprocessStreamOptions(wire(t, text), ai.StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if options.Identity != nil {
				t.Errorf("%s: identity = %+v, want nil", text, options.Identity)
			}
		}
	})

	t.Run("a malformed identity is an error", func(t *testing.T) {
		for _, text := range []string{`{"identity":"pi"}`, `{"identity":{"userAgent":"loud"}}`} {
			if _, err := subprocessStreamOptions(wire(t, text), ai.StreamOptions{}); err == nil {
				t.Errorf("%s: want an error", text)
			}
		}
	})
}
