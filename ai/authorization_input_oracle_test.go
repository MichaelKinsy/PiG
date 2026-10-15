package ai

// pi: packages/ai/src/auth/oauth/anthropic.ts
// pi: packages/ai/src/auth/oauth/openai-codex.ts
// pi: packages/ai/src/auth/oauth/openrouter.ts

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type authorizationPair struct {
	Code  *string `json:"code"`
	State *string `json:"state"`
}

type authorizationAnswer struct {
	Anthropic  authorizationPair `json:"anthropic"`
	Codex      authorizationPair `json:"codex"`
	OpenRouter *string           `json:"openrouter"`
}

func orEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// authorizationInputs combine pasted values the three parseAuthorizationInput functions treat differently: full callback URLs (with and without a
// port, fragment, repeated or escaped parameters), scheme-like and scheme-less text, code#state pastes with extra '#', bare query strings with '+',
// bad percent escapes and ';', and JavaScript whitespace around the value.
func authorizationInputs() []string {
	bases := []string{
		"http://localhost:1455/auth/callback?code=abc&state=xyz", "https://platform.claude.com/oauth/code/callback?code=a%20b&state=s+t",
		"http://localhost:53692/callback?code=&state=", "http://localhost/callback?state=only", "http://localhost/callback", "http://localhost/?code=a&code=b&state=1&state=2",
		"http://localhost/?code=%zz&state=%", "http://localhost/?code=a;b&state=c", "http://localhost/?code=a#frag", "http://localhost/#code=a&state=b",
		"localhost:1455/auth/callback?code=abc&state=xyz", "abc:def", "a:b?code=1", "javascript:alert(1)", "file:///tmp/x?code=1", "data:text/plain,code=1", "mailto:x@y.z?code=1",
		"HTTP://LOCALHOST/?code=1", "http://[::1]:80/?code=1", "http://user:pass@host/?code=1&state=2", "http://exa mple.com/?code=1", "//host/?code=1", "http:/host?code=1", "http:host?code=1",
		"https://localhost:99999/?code=1", "https://x/?code=%zz;a&state=%", "ftp://x/?code=%zz;a&state=%", "x://y?code=a;b&state=%41+b",
		"abc", "abc#def", "abc#def#ghi", "#def", "abc#", "#", "a#b#", "code=1", "code=1&state=2", "state=2&code=1", "code=a+b%20c&state=%41", "code=1;state=2", "x&code=1", "?code=1&state=2",
		"code=%zz", "code=%", "code=1&code=2", "code", "co de=1", "code=1#state", "abc code=1", "é#ü", "code=é&state=ü", "code=%C3%A9", "code=%ff&state=%e9",
		"", " ", "\t\n", "\u00a0x\u00a0", "\ufeffcode=1\u2028", "\u200bx", "\u180ex", " http://localhost/?code=1 ", "http://localhost/?code=1\n", "http://localhost/?code=\u00e9",
		"http://localhost:99999/?code=1", "http://localhost:0/?code=1", "https://a/?code=1&state=2&state", "ftp://x/?code=1", "x://y?code=1&state=2#z", "1http://x?code=1", "+http://x?code=1", "-a.b+c://x?code=1",
		"ws://x:99999/?code=1", "ftp://x:99999/?code=1", "x://y:99999/?code=1", "x://y:65536/?code=1", "wss://x:70000/?code=1&state=2", "file://host:99/?code=1", "file://localhost/x?code=1",
		"x://a<b/?code=1", "x://%zz/?code=1", "x://h/%zz?code=1", "ws://h\\a?code=1", "ws://ex%41mple/?code=1", "ws://1.2.3.256/?code=1", "x://1.2.3.256/?code=1", "ws://user@/?code=1",
		"x://user@/?code=1", "x://:1/?code=1", "x://[::1]:8/?code=1", "x://[zz]/?code=1", "x:y?code=a\tb", "x:y?code=a\nb", "x://münchen/?code=ü&state=é", "code=1&state=", "abc#",
	}
	out := append([]string{}, bases...)
	for _, base := range bases {
		out = append(out, "  "+base+"\t")
	}
	return out
}

// Pi's three private parseAuthorizationInput functions run in Node from the pinned source (testdata/authorization_input.mjs cuts each one out of its file)
// against the same pasted values. An undefined code and Codex or OpenRouter value equals Go's empty one, since their callers test truthiness; Anthropic's
// state is compared with its presence, because the login sends a defined empty state as "" and an undefined one as the verifier (anthropic.ts:189, :230).
func TestAuthorizationInputParsersMatchPi(t *testing.T) {
	inputs := authorizationInputs()
	payload, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "ai", "src", "auth", "oauth")
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/authorization_input.mjs", directory)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []authorizationAnswer
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(inputs) {
		t.Fatalf("Pi answered %d of %d inputs", len(expected), len(inputs))
	}
	differing := 0
	for i, input := range inputs {
		want := expected[i]
		code, state, hasState := parseAuthorizationInput(input)
		codexCode, codexState := parseCodexAuthorizationInput(input)
		router := parseOpenRouterAuthorizationInput(input)
		type answer struct {
			anthropicCode, anthropicState string
			anthropicHasState             bool
			codexCode, codexState, router string
		}
		got := answer{code, state, hasState, codexCode, codexState, router}
		pi := answer{orEmpty(want.Anthropic.Code), orEmpty(want.Anthropic.State), want.Anthropic.State != nil, orEmpty(want.Codex.Code), orEmpty(want.Codex.State), orEmpty(want.OpenRouter)}
		if got != pi {
			if differing++; differing <= 12 {
				t.Errorf("input %q differs from Pi:\n  Pig %+v\n  Pi  %+v", strings.TrimSpace(input), got, pi)
			}
		}
	}
	if differing > 12 {
		t.Errorf("%d of %d inputs differ from Pi", differing, len(inputs))
	}
}
