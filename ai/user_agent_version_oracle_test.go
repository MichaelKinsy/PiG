package ai

// pi: packages/ai/src/utils/version.ts

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"regexp"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type userAgentVersionCase struct {
	Name     string  `json:"name"`
	Short    bool    `json:"short"`
	Custom   *string `json:"custom"`
	Detailed bool    `json:"detailed"`
	Version  *string `json:"version"`
}

// Pi's detailed User-Agent is `pi/<version> (...)` where <version> is identity.version when the caller gives one (an empty string included, since the
// default applies only to null and undefined, `identity?.version ?? PI_AI_VERSION`), and the PI_AI_VERSION constant of version.ts otherwise. The short
// and custom forms carry no version. PiG's product name and its composite release version stand in for `pi` and PI_AI_VERSION (D65), so the version
// token is compared as a field: an explicit version is Pi's token, and the default is ProductVersion, which is not Pi's constant.
func TestUserAgentVersionFieldFollowsPi(t *testing.T) {
	custom := "acme/1.0"
	empty := ""
	nine := "9.9.9"
	cases := []userAgentVersionCase{
		{Name: "none"},
		{Name: "version", Version: &nine},
		{Name: "empty version", Version: &empty},
		{Name: "explicit detailed", Detailed: true},
		{Name: "explicit detailed with version", Detailed: true, Version: &nine},
		{Name: "short", Short: true},
		{Name: "short with version", Short: true, Version: &nine},
		{Name: "custom", Custom: &custom},
		{Name: "custom with version", Custom: &custom, Version: &nine},
		{Name: "custom empty", Custom: &empty},
	}
	var pi []struct {
		UserAgent string `json:"userAgent"`
		Default   string `json:"default"`
	}
	payload, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/user_agent_version.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	if err := json.Unmarshal(output, &pi); err != nil {
		t.Fatal(err)
	}
	if len(pi) != len(cases) {
		t.Fatalf("Pi answered %d of %d cases", len(pi), len(cases))
	}
	piToken := regexp.MustCompile(`^pi/(.*?) \(`)
	pigToken := regexp.MustCompile(`^` + regexp.QuoteMeta(pigidentity.UserAgentProduct) + `/(.*?) \(`)
	for i, c := range cases {
		identity := &ClientIdentity{Version: c.Version}
		switch {
		case c.Short:
			identity.UserAgent = new(UserAgentShort)
		case c.Detailed:
			identity.UserAgent = new(UserAgentDetailed)
		case c.Custom != nil:
			identity.UserAgent = new(UserAgentCustom(*c.Custom))
		}
		got := FormatUserAgent(identity)
		want := pi[i].UserAgent
		switch {
		case c.Short:
			if want != "pi" || got != pigidentity.UserAgentProduct {
				t.Errorf("%s: short form PiG %q, Pi %q", c.Name, got, want)
			}
		case c.Custom != nil:
			if got != want {
				t.Errorf("%s: custom form PiG %q, Pi %q", c.Name, got, want)
			}
		default:
			piMatch, pigMatch := piToken.FindStringSubmatch(want), pigToken.FindStringSubmatch(got)
			if piMatch == nil || pigMatch == nil {
				t.Fatalf("%s: user agents PiG %q, Pi %q have no version token", c.Name, got, want)
			}
			wantVersion := piMatch[1]
			if c.Version == nil {
				if wantVersion != pi[i].Default {
					t.Fatalf("%s: Pi's default token %q is not PI_AI_VERSION %q", c.Name, wantVersion, pi[i].Default)
				}
				wantVersion = ProductVersion
			}
			if pigMatch[1] != wantVersion {
				t.Errorf("%s: version token PiG %q, want %q (Pi %q)", c.Name, pigMatch[1], wantVersion, want)
			}
		}
	}
}
