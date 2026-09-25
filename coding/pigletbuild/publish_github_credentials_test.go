package pigletbuild

import (
	"strings"
	"testing"
)

func TestPublishGitHubSourceRefDoesNotEchoCredentials(t *testing.T) {
	const secret = "publish-credential-must-not-appear"
	for name, tc := range map[string]struct{ ref, reason string }{
		"password":                          {"git:https://user:" + secret + "@github.com/acme/porter@v1.2.3", "Git Piglet source URLs must not include credentials; configure a Git credential helper or use SSH"},
		"username token":                    {"git:https://" + secret + "@github.com/acme/porter@v1.2.3", "Git Piglet source URLs must not include credentials; configure a Git credential helper or use SSH"},
		"query token":                       {"git:https://github.com/acme/porter?token=" + secret, "Git Piglet source URLs must not include query parameters"},
		"password missing repository":       {"git:https://user:" + secret + "@github.com/acme", "invalid remote Piglet source: use an explicit scheme (npm: or git:); malformed references and unsupported source schemes are refused"},
		"username token missing repository": {"git:https://" + secret + "@github.com/acme", "invalid remote Piglet source: use an explicit scheme (npm: or git:); malformed references and unsupported source schemes are refused"},
		"query token missing repository":    {"git:https://github.com/acme?token=" + secret, "invalid remote Piglet source: use an explicit scheme (npm: or git:); malformed references and unsupported source schemes are refused"},
		"npm registry password":             {"npm:@acme/porter?registry=https%3A%2F%2Fuser%3A" + secret + "%40registry.example", "invalid remote Piglet source: use an explicit scheme (npm: or git:); malformed references and unsupported source schemes are refused"},
	} {
		t.Run(name, func(t *testing.T) {
			gh, tmp := publishTestEnv(t)
			source := writePublishPiglet(t, releasedPorter)
			keyPath, _, _ := writePublishKey(t)
			var stdout, stderr strings.Builder
			code := RunPigletPublishCommand([]string{source, "--to", "github", "--repo", "acme/porter", "--sign-key", keyPath, "--source-ref", tc.ref, "--yes"}, &stdout, &stderr)
			if code != 1 || stdout.Len() != 0 || stderr.String() != "error: --source-ref: "+tc.reason+"\n" {
				t.Errorf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			for stream, text := range map[string]string{"stdout": stdout.String(), "stderr": stderr.String()} {
				if strings.Contains(text, secret) {
					t.Errorf("%s exposes rejected source credentials: %s", stream, text)
				}
			}
			if calls := gh.calls(); calls != nil {
				t.Errorf("invalid source ran gh: %q", ghArgs(calls))
			}
			assertNoStagedRelease(t, tmp)
			builders := func() ([]BuilderBackend, error) {
				t.Error("rejected source started builder discovery")
				return nil, nil
			}
			code, out, errOut := runPublishForTest(t, builders, source, "--to", "github", "--repo", "acme/porter", "--sign-key", keyPath, "--source-ref", tc.ref, "--yes")
			if code != 1 || out != "" || errOut != "error: --source-ref: "+tc.reason+"\n" {
				t.Errorf("injected builder path: code=%d stdout=%s stderr=%s", code, out, errOut)
			}
		})
	}
}
