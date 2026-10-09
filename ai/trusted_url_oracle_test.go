package ai

// pi: packages/ai/src/auth/oauth/kimi-coding.ts
// pi: packages/ai/src/auth/oauth/meta.ts
// pi: packages/ai/src/auth/oauth/xai.ts

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type trustedURLAnswer struct {
	Kimi *string `json:"kimi"`
	Meta *string `json:"meta"`
	Xai  *string `json:"xai"`
}

// trustedURLInputs are verification URIs a server could send: schemes and cases, missing or extra slashes, backslashes, userinfo, ports, IDNA and
// numeric hosts, spaces and control characters, default ports, dot segments, queries and fragments with characters the parser encodes.
func trustedURLInputs() []string {
	return []string{
		"https://example.com/verify", "http://example.com/verify", "HTTPS://EXAMPLE.COM/Verify", "https://example.com", "https://example.com/", "https://example.com:443/", "http://example.com:80/x", "https://example.com:8443/x",
		"https://example.com:99999/", "https://example.com:65535/", "https://example.com:65536/", "https://example.com:0/", "https://example.com:/", "https://example.com:abc/", "https:example.com", "https:/example.com", "https:///example.com", "https:////example.com/x", "https:\\\\example.com\\x",
		"https://", "https://:443", "http://", "https://user:pass@example.com/", "https://user@example.com", "https://@example.com", "https://a@b@example.com", "https://[::1]/", "https://[::1]:8080/x", "https://[::1", "https://[::g]/",
		"https://127.0.0.1/x", "https://0x7f.1/", "https://1.2.3/", "https://192.168.0.256/", "https://1.2.3.4.5/", "https://münchen.example/", "https://xn--mnchen-3ya.example/", "https://xn--.example/", "https://exa mple.com/",
		"https://example.com/a b", "https://example.com/a\tb", "\thttps://example.com/\n", " https://example.com/ ", "https://example.com/\u00e9", "https://example.com/a?b c#d e", "https://example.com/a?q=\"<>`", "https://example.com/a#\"<>`",
		"https://example.com/a/../b/./c", "https://example.com/%2e%2e/b", "https://example.com/a%zz", "https://example.com//a//b", "https://example.com\\a\\b", "https://example.com?x=1", "https://example.com#frag", "https://example.com/?",
		"https://example.com/#", "https://EXAMPLE.com./", "https://ex%41mple.com/", "https://example.com%2fpath", "https://exam_ple.com/", "https://-example.com/", "https://example..com/", "https://exa<mple.com/",
		"ftp://example.com/", "ws://example.com/", "wss://example.com/", "file:///etc/passwd", "javascript:alert(1)", "data:text/html,x", "mailto:a@b.c", "about:blank", "blob:https://example.com/x", "x://y", "://example.com",
		"example.com", "example.com/verify", "//example.com/verify", "/verify", "verify", "?x", "#x", "", " ", "HtTp://example.com/", "http:example.com", "http:/\\example.com", "http:\\\\example.com", "https://\u0000example.com/",
		"https://example.com/\u2028", "https://example.com:0080/", "https://example.com:00443/", "http://example.com:443/", "https://日本.example/日本?日=本#日",
	}
}

// Pi's private trustedHttpUrl (kimi-coding.ts and meta.ts) and validateVerificationUri (xai.ts) run in Node from the pinned source against the same
// values: which URLs each accepts and, where Pi returns it, the href it returns (the URL the user's browser opens).
func TestTrustedVerificationURLsMatchPi(t *testing.T) {
	inputs := trustedURLInputs()
	payload, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "ai", "src", "auth", "oauth")
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/trusted_urls.mjs", directory)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []trustedURLAnswer
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(inputs) {
		t.Fatalf("Pi answered %d of %d inputs", len(expected), len(inputs))
	}
	str := func(p *string) string {
		if p == nil {
			return "<refused>"
		}
		return *p
	}
	accepted, differing := 0, 0
	for i, input := range inputs {
		var got trustedURLAnswer
		if trustedHTTPURL(input) {
			got.Kimi = &input
		}
		if href := metaTrustedHTTPURL(input); href != "" {
			got.Meta = &href
		}
		if href, err := xaiValidateHTTPSURL(input); err == nil {
			got.Xai = &href
		}
		want := expected[i]
		if want.Meta != nil {
			accepted++
		}
		// Pi's kimi function returns an href that its caller only tests for null; Go reports acceptance.
		kimiDiffers := (got.Kimi != nil) != (want.Kimi != nil)
		if kimiDiffers || str(got.Meta) != str(want.Meta) || str(got.Xai) != str(want.Xai) {
			if differing++; differing <= 15 {
				t.Errorf("%q differs from Pi:\n  Pig kimi accepts %v, meta %s, xai %s\n  Pi  kimi accepts %v, meta %s, xai %s", input, got.Kimi != nil, str(got.Meta), str(got.Xai), want.Kimi != nil, str(want.Meta), str(want.Xai))
			}
		}
	}
	if differing > 15 {
		t.Errorf("%d of %d inputs differ from Pi", differing, len(inputs))
	}
	if accepted < len(inputs)/4 || accepted > len(inputs)*3/4 {
		t.Errorf("inputs are one-sided: Pi's meta accepts %d of %d", accepted, len(inputs))
	}
}
