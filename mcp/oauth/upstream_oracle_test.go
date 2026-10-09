package oauth_test

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// The golden file holds Pi's packages/mcp OAuth functions (challenge parsing, discovery URLs, resource selection,
// scope step-up and the metadata parsers) run on a deterministic corpus and seeded-random inputs by
// ../testdata/upstream-oracle.mjs. Regenerate it from the repository root with:
//
//	node mcp/testdata/upstream-oracle.mjs > mcp/testdata/upstream-golden.json

type outcome struct {
	OK    json.RawMessage `json:"ok"`
	Error *string         `json:"error"`
}

type goldenFile struct {
	Challenge []struct {
		outcome
		Header string `json:"header"`
	} `json:"challenge"`
	DiscoveryURLs []struct {
		outcome
		URL string `json:"url"`
	} `json:"discoveryUrls"`
	Resource []struct {
		outcome
		ServerURL string  `json:"serverUrl"`
		Metadata  *string `json:"metadata"`
	} `json:"resource"`
	StepUp []struct {
		outcome
		Granted    *string `json:"granted"`
		Challenged *string `json:"challenged"`
	} `json:"stepUp"`
	Metadata   []valueCase `json:"metadata"`
	AuthServer []valueCase `json:"authServer"`
	Tokens     []valueCase `json:"tokens"`
	Client     []valueCase `json:"client"`
}

type valueCase struct {
	outcome
	Value string `json:"value"`
}

func loadGolden(t *testing.T) goldenFile {
	t.Helper()
	data, err := os.ReadFile("../testdata/upstream-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden goldenFile
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	return golden
}

// want renders an oracle outcome: the JSON-text result, or the error Pi threw.
func (o outcome) want() string {
	if o.Error != nil {
		return "error: " + *o.Error
	}
	var text string
	if err := json.Unmarshal(o.OK, &text); err != nil {
		return string(o.OK)
	}
	return text
}

func report(t *testing.T, kind string, total int, differences []string) {
	t.Helper()
	if len(differences) == 0 {
		return
	}
	shown := differences
	if len(shown) > 15 {
		shown = shown[:15]
	}
	t.Errorf("%s: %d of %d cases differ from Pi:\n%s", kind, len(differences), total, strings.Join(shown, "\n"))
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func jsonEqual(a, b string) bool {
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return a == b
	}
	return reflect.DeepEqual(x, y)
}

func TestParseWWWAuthenticateMatchesPi(t *testing.T) {
	golden := loadGolden(t)
	var differences []string
	for _, c := range golden.Challenge {
		challenge := oauth.ParseWWWAuthenticate(c.Header)
		var href *string
		if challenge.ResourceMetadataURL != nil {
			s := challenge.ResourceMetadataURL.String()
			href = &s
		}
		got, _ := json.Marshal(map[string]*string{
			"resourceMetadataUrl": href, "scope": nullable(challenge.Scope), "error": nullable(challenge.Error), "errorDescription": nullable(challenge.ErrorDescription),
		})
		if !jsonEqual(string(got), c.want()) {
			differences = append(differences, "header "+c.Header+"\n  got  "+string(got)+"\n  want "+c.want())
		}
	}
	report(t, "parseWwwAuthenticate", len(golden.Challenge), differences)
}

func TestBuildAuthorizationServerDiscoveryURLsMatchesPi(t *testing.T) {
	golden := loadGolden(t)
	var differences []string
	for _, c := range golden.DiscoveryURLs {
		urls, err := oauth.BuildAuthorizationServerDiscoveryURLs(c.URL)
		var got string
		if err != nil {
			got = "error"
		} else {
			list := []map[string]string{}
			for _, entry := range urls {
				list = append(list, map[string]string{"url": entry.URL.String(), "type": entry.Type})
			}
			encoded, _ := json.Marshal(list)
			got = string(encoded)
		}
		want := c.want()
		if (err != nil) != (c.Error != nil) || (err == nil && !jsonEqual(got, want)) {
			differences = append(differences, "url "+c.URL+"\n  got  "+got+"\n  want "+want)
		}
	}
	report(t, "buildAuthorizationServerDiscoveryUrls", len(golden.DiscoveryURLs), differences)
}

func TestSelectResourceMatchesPi(t *testing.T) {
	golden := loadGolden(t)
	var differences []string
	for _, c := range golden.Resource {
		var metadata *oauth.OAuthProtectedResourceMetadata
		if c.Metadata != nil {
			metadata = &oauth.OAuthProtectedResourceMetadata{}
			if err := json.Unmarshal([]byte(*c.Metadata), metadata); err != nil {
				t.Fatal(err)
			}
		}
		requested, err := oauth.ResourceURLFromServerURL(c.ServerURL)
		var got string
		if err == nil {
			var selected string
			selected, err = oauth.SelectResource(c.ServerURL, metadata)
			if err == nil {
				var sel *string
				if selected != "" {
					sel = &selected
				}
				encoded, _ := json.Marshal(map[string]any{"url": requested.String(), "selected": sel})
				got = string(encoded)
			}
		}
		if err != nil {
			got = "error: " + err.Error()
		}
		want := c.want()
		if c.Error != nil {
			// Go errors carry no JavaScript class name: compare the message.
			if got != "error: "+strings.TrimPrefix(strings.TrimPrefix(*c.Error, "TypeError: "), "Error: ") {
				differences = append(differences, "serverUrl "+c.ServerURL+" metadata "+deref(c.Metadata)+"\n  got  "+got+"\n  want "+want)
			}
			continue
		}
		if !jsonEqual(got, want) {
			differences = append(differences, "serverUrl "+c.ServerURL+" metadata "+deref(c.Metadata)+"\n  got  "+got+"\n  want "+want)
		}
	}
	report(t, "resourceUrlFromServerUrl/selectResource", len(golden.Resource), differences)
}

func deref(s *string) string {
	if s == nil {
		return "<none>"
	}
	return *s
}

func TestStepUpScopeMatchesPi(t *testing.T) {
	golden := loadGolden(t)
	var differences []string
	for _, c := range golden.StepUp {
		got := oauth.StepUpScope(deref2(c.Granted), deref2(c.Challenged))
		var want *string
		if c.OK != nil && string(c.OK) != "null" {
			var s string
			_ = json.Unmarshal(c.OK, &s)
			want = &s
		}
		// StepUpScope has no undefined: empty stands for it.
		if (want == nil && got != "") || (want != nil && got != *want) {
			differences = append(differences, "granted "+deref(c.Granted)+" challenged "+deref(c.Challenged)+"\n  got  "+got+"\n  want "+c.want())
		}
	}
	report(t, "stepUpScope", len(golden.StepUp), differences)
}

func deref2(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func runParser(t *testing.T, kind string, cases []valueCase, parse func([]byte) (any, error)) {
	t.Helper()
	var differences []string
	for _, c := range cases {
		value, err := parse([]byte(c.Value))
		want := c.want()
		if err != nil {
			if c.Error == nil || "error: "+err.Error() != "error: "+strings.TrimPrefix(*c.Error, "Error: ") {
				differences = append(differences, "value "+c.Value+"\n  got  error: "+err.Error()+"\n  want "+want)
			}
			continue
		}
		if c.Error != nil {
			differences = append(differences, "value "+c.Value+"\n  got  ok\n  want "+want)
			continue
		}
		encoded, _ := json.Marshal(value)
		if !jsonEqual(string(encoded), want) {
			differences = append(differences, "value "+c.Value+"\n  got  "+string(encoded)+"\n  want "+want)
		}
	}
	report(t, kind, len(cases), differences)
}

func TestParseProtectedResourceMetadataMatchesPi(t *testing.T) {
	runParser(t, "parseProtectedResourceMetadata", loadGolden(t).Metadata, func(b []byte) (any, error) { return oauth.ParseProtectedResourceMetadata(b) })
}

func TestParseAuthorizationServerMetadataMatchesPi(t *testing.T) {
	runParser(t, "parseAuthorizationServerMetadata", loadGolden(t).AuthServer, func(b []byte) (any, error) { return oauth.ParseAuthorizationServerMetadata(b) })
}

func TestParseOAuthTokensMatchesPi(t *testing.T) {
	runParser(t, "parseOAuthTokens", loadGolden(t).Tokens, func(b []byte) (any, error) { return oauth.ParseOAuthTokens(b) })
}

func TestParseClientInformationMatchesPi(t *testing.T) {
	runParser(t, "parseClientInformation", loadGolden(t).Client, func(b []byte) (any, error) { return oauth.ParseClientInformation(b) })
}
