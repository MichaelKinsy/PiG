package tui

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// testAuthCheck is a provider's configured auth for the selector tests.
type testAuthCheck struct{ typ, source string }

func (c testAuthCheck) AuthCheckType() string   { return c.typ }
func (c testAuthCheck) AuthCheckSource() string { return c.source }

type oauthStatusProbe struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	AuthType     string           `json:"authType"`
	Subscription *bool            `json:"subscription,omitempty"`
	Status       *oauthStatusJSON `json:"status,omitempty"`
}

type oauthStatusJSON struct {
	Type   string `json:"type"`
	Source string `json:"source,omitempty"`
}

// oauth-selector.ts:36-53 formatAuthSelectorProviderStatus against pinned Pi: no status, a status of the other auth type
// (subscription, account or API key), a stored credential, an OAuth source, environment variable names and any other source
// (runtime, stored, models_json_key, fallback) print exactly as Pi prints them.
func TestFormatAuthSelectorProviderStatusMatchesPi(t *testing.T) {
	notSubscription, subscription := false, true
	var probes []oauthStatusProbe
	for _, authType := range []string{"oauth", "api_key"} {
		for _, subscription := range []*bool{nil, &notSubscription, &subscription} {
			probes = append(probes, oauthStatusProbe{ID: "p", Name: "P", AuthType: authType, Subscription: subscription})
			for _, statusType := range []string{"oauth", "api_key"} {
				for _, source := range []string{"", "OAuth", "stored credential", "stored", "runtime", "fallback", "models_json_key", "models_json_command", "OPENAI_API_KEY", "A_B, C_D", "lower_case", "A_B,C_D", "key in models.json"} {
					probes = append(probes, oauthStatusProbe{ID: "p", Name: "P", AuthType: authType, Subscription: subscription, Status: &oauthStatusJSON{Type: statusType, Source: source}})
				}
			}
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/oauth_status.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []string
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("Pi returned %d results for %d probes", len(expected), len(probes))
	}
	// The Pi script sets trueColor capabilities before it loads the theme; without them Pig's theme follows the host
	// terminal's COLORTERM and TERM, so a 256-colour host such as TERM=screen printed other colour codes.
	withTrueColor(t, true)
	SetTheme("dark")
	for i, probe := range probes {
		provider := OAuthProvider{ID: probe.ID, Name: probe.Name, AuthType: probe.AuthType, Subscription: probe.Subscription}
		if probe.Status != nil {
			provider.Status = testAuthCheck{typ: probe.Status.Type, source: probe.Status.Source}
		}
		if got, want := FormatAuthSelectorProviderStatus(provider), expected[i]; got != want {
			t.Errorf("probe %d %+v status %+v: Pig %q, Pi %q", i, probe, probe.Status, got, want)
		}
	}
}
