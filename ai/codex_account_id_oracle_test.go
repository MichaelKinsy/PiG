package ai

// pi: packages/ai/src/auth/oauth/openai-codex.ts

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// codexTokenInputs wrap payloads that carry (or lack) the account claim in JWT shapes Pi's atob-based decodeJwt reads differently from a base64url
// decoder: standard and URL-safe alphabets, with and without padding, a length that is 1 mod 4, whitespace, and non-ASCII characters in the claim.
func codexTokenInputs() []string {
	payloads := []string{
		`{"https://api.openai.com/auth":{"chatgpt_account_id":"acct_123"}}`,
		`{"https://api.openai.com/auth":{"chatgpt_account_id":""}}`,
		`{"https://api.openai.com/auth":{"chatgpt_account_id":7}}`,
		`{"https://api.openai.com/auth":{"chatgpt_account_id":"é€"}}`,
		`{"https://api.openai.com/auth":{"chatgpt_account_id":"acct>>>???~~~"}}`,
		`{"https://api.openai.com/auth":{"chatgpt_account_id":"a"},"x":"\u00ff\u00fe>>>"}`,
		`{"https://api.openai.com/auth":null}`, `{"https://api.openai.com/auth":"x"}`, `{"https://api.openai.com/auth":[]}`, `{"other":{"chatgpt_account_id":"x"}}`,
		`{"https://api.openai.com/auth":{"chatgpt_account_id":"a"},"https://api.openai.com/auth":{"chatgpt_account_id":"b"}}`,
		`[]`, `null`, `"text"`, `7`, `{`, ``, ` {"https://api.openai.com/auth":{"chatgpt_account_id":"ws"}} `,
		"{\"https://api.openai.com/auth\":{\"chatgpt_account_id\":\"\u0100\"}}",
	}
	var tokens []string
	for _, payload := range payloads {
		raw := []byte(payload)
		forms := []string{
			base64.RawURLEncoding.EncodeToString(raw), base64.URLEncoding.EncodeToString(raw),
			base64.RawStdEncoding.EncodeToString(raw), base64.StdEncoding.EncodeToString(raw),
		}
		forms = append(forms, " "+forms[0], forms[0][:len(forms[0])/2]+"\n"+forms[0][len(forms[0])/2:], forms[1]+"=", forms[0]+"A", forms[0]+"==", "="+forms[0])
		// Each ASCII whitespace character atob strips, and \v and U+00A0, which it does not.
		for _, space := range []string{"\t", "\f", "\r", "\v", "\u00a0"} {
			forms = append(forms, forms[2][:len(forms[2])/2]+space+forms[2][len(forms[2])/2:])
		}
		for _, form := range forms {
			tokens = append(tokens, "h."+form+".s", "h."+form+".", "."+form+".")
		}
	}
	tokens = append(tokens, "", "a.b", "a.b.c.d", "...", "h..s", "h.!!!.s", "h.AAAA.s", "h.e30.s", "h.e30=.s", "h.e30==.s", "h.e3.s", "h.e.s", "plain")
	return tokens
}

// Pi's private decodeJwt and getAccountId run in Node from the pinned source against the same tokens: atob's alphabet (standard base64, not URL-safe),
// padding and whitespace rules, and its byte-per-character (Latin-1) reading of the decoded payload decide which tokens yield an account id and what it is.
func TestCodexAccountIDMatchesPi(t *testing.T) {
	inputs := codexTokenInputs()
	payload, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "ai", "src", "auth", "oauth", "openai-codex.ts")
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/codex_account_id.mjs", source)
	cmd.Stdin = bytes.NewReader(payload)
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
	if len(expected) != len(inputs) {
		t.Fatalf("Pi answered %d of %d tokens", len(expected), len(inputs))
	}
	found, differing := 0, 0
	for i, token := range inputs {
		if expected[i] != "" {
			found++
		}
		if got := CodexAccountID(token); got != expected[i] {
			if differing++; differing <= 12 {
				t.Errorf("token %q: Pig %q, Pi %q", strings.TrimSpace(token), got, expected[i])
			}
		}
	}
	if differing > 12 {
		t.Errorf("%d of %d tokens differ from Pi", differing, len(inputs))
	}
	if found < len(inputs)/10 || found > len(inputs)/2 {
		t.Errorf("tokens are one-sided: Pi found an account id in %d of %d", found, len(inputs))
	}
}
