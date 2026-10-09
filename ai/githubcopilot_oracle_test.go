package ai

// pi: packages/ai/src/auth/oauth/github-copilot.ts

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type copilotHelperProbe struct {
	Input      string `json:"input"`
	Token      string `json:"token"`
	Enterprise string `json:"enterprise"`
}

type copilotHelperAnswer struct {
	Domain        string `json:"domain"`
	BaseFromToken string `json:"baseFromToken"`
	BaseURL       string `json:"baseUrl"`
}

// copilotHelperProbes pair pasted enterprise domains (schemes, ports, paths, userinfo, case, IDNA, IPv6 and IPv4 hosts, invalid ports and hosts,
// JavaScript whitespace) with Copilot tokens whose proxy-ep claim sits anywhere, repeats, is empty, holds whitespace, or lacks the proxy. prefix.
func copilotHelperProbes() []copilotHelperProbe {
	inputs := []string{
		"github.example.com", "GitHub.Example.COM", "https://github.example.com", "http://github.example.com/path?q=1#f", "https://github.example.com:8443", "https://github.example.com:99999",
		"github.example.com:8443", "github.example.com:99999", "github.example.com/path", "user:pass@github.example.com", "https://user@github.example.com", "ftp://github.example.com", "ssh://git@github.example.com:22/x",
		"file:///tmp/x", "file://host/x", "://x", "x://", "https://", "https:///", "https://[::1]:8080/", "[::1]", "[::1", "127.0.0.1", "0x7f.1", "1.2.3", "192.168.0.256", "https://exa mple.com", "exa mple.com",
		"https://example.com\\path", "example.com\\path", "https://münchen.example", "münchen.example", "xn--mnchen-3ya.example", "https://EXAMPLE.com.", "example..com", "ex%41mple.com", "https://ex%41mple.com/",
		"ftp://[::1]:21/x", "ftp://0x7f.1/", "FTP://München.Example:2121", "ws://1.2.3/", "wss://ex%41mple.com:99999", "http://0x7f.1/", "http://[::1]/", "http://1.2.3.4.5/", "https://:8080", "http://:80/",
		"a_b.example", "a b", "%", "https://%zz.example", "?x", "#x", "/path", "https://a.b:0", "https://a.b:", "a.b:", "\u00a0github.example.com\u00a0", "\ufeffgithub.example.com", "\u200bgithub.example.com",
		" ", "", "\t\n", "HTTPS://Github.Example.com", "javascript://x", "data:text/plain,x", "mailto:a@b.c", "http://a.b@c.d:80", "https://a.b:443", "http://a.b:80/", "https://a.b?x://y",
		"example.com?x=a://b", "example.com#a://b", "github.example.com:abc", "https://github.example.com:+80", "https://日本.example", "日本.example", "ＧＩＴ.example", "https://xn--.example",
		"ssh://Git.Example.com/x", "x://münchen/", "x://%zz/", "x://h:99999/", "x://u@/", "x://a<b/", "file://localhost/x", "file://Host/x", "file://host:1/x", "x://[::1]:8/", "x://[zz]/",
	}
	tokens := []string{
		"", "tid=1;exp=2;proxy-ep=proxy.individual.githubcopilot.com;x=y", "proxy-ep=proxy.business.githubcopilot.com", "proxy-ep=api.enterprise.example;x=1", "tid=1;proxy-ep=proxy.proxy.example",
		"proxy-ep=", "proxy-ep=;proxy-ep=proxy.second.example", "xproxy-ep=proxy.inside.example", "tid=1;proxy-ep= proxy.spaced.example", "tid=1;proxy-ep=proxy.example ", "tid=1; proxy-ep=proxy.after-space.example",
		"proxy-ep=proxy.", "proxy-ep=proxy", "proxy-ep=Proxy.upper.example", "PROXY-EP=proxy.upper-key.example", "proxy-ep=proxy.a.example;proxy-ep=proxy.b.example", "no claim here", "proxy-ep\u00a0=proxy.x", "proxy-ep=\u00a0proxy.nbsp.example",
		"proxy-ep=proxy.x.example\nnext=1", "proxy-ep=https://proxy.scheme.example", "proxy-ep=proxy.a.example,b",
	}
	enterprises := []string{"", "github.example.com", "corp.example"}
	var probes []copilotHelperProbe
	for i, input := range inputs {
		probes = append(probes, copilotHelperProbe{Input: input, Token: tokens[i%len(tokens)], Enterprise: enterprises[i%len(enterprises)]})
	}
	for i, token := range tokens {
		probes = append(probes, copilotHelperProbe{Input: "x.example", Token: token, Enterprise: enterprises[i%len(enterprises)]})
	}
	return probes
}

// Pi's private normalizeDomain, getBaseUrlFromToken and getGitHubCopilotBaseUrl run in Node from the pinned source against the same probes. Pi's
// null and "" both mean no domain (every caller tests truthiness), which Go reports as an empty domain with ok false.
func TestGitHubCopilotHelpersMatchPi(t *testing.T) {
	probes := copilotHelperProbes()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "ai", "src", "auth", "oauth", "github-copilot.ts")
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/github_copilot_helpers.mjs", source)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []copilotHelperAnswer
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("Pi answered %d of %d probes", len(expected), len(probes))
	}
	differing := 0
	for i, probe := range probes {
		domain, ok := normalizeDomain(probe.Input)
		got := copilotHelperAnswer{Domain: domain, BaseURL: getCopilotBaseURL(probe.Token, probe.Enterprise)}
		if ok != (domain != "") {
			t.Errorf("input %q: normalizeDomain reports ok %v for domain %q", probe.Input, ok, domain)
		}
		if got.Domain != expected[i].Domain || got.BaseURL != expected[i].BaseURL {
			if differing++; differing <= 15 {
				t.Errorf("probe %+v differs from Pi:\n  Pig %+v\n  Pi  %+v", probe, got, expected[i])
			}
		}
	}
	if differing > 15 {
		t.Errorf("%d of %d probes differ from Pi", differing, len(probes))
	}
}
