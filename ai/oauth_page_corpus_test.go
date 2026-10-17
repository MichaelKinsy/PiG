package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"strings"
	"testing"
)

type oauthPageCase struct {
	Message string `json:"message"`
	Details string `json:"details"`
}

// oauthPageCorpus is the fixed cases of the upstream comparison and 300 seeded strings built from the characters escapeHtml rewrites, already-escaped
// entities, newlines, tabs, non-ASCII text, astral characters and long runs, each used as the message and as the details.
func oauthPageCorpus() []oauthPageCase {
	cases := []oauthPageCase{
		{Message: "Signed in to Anthropic. You may now close this page."},
		{Message: "Anthropic authorization failed.", Details: `<script>alert(1)</script>"'&`},
		{Message: `bad <tag> & "quote" 'apos'`, Details: "line1\nline2 & more"},
		{Message: "State mismatch."},
		{Message: "already escaped &#34; &quot; &#39;", Details: "&amp;#34;"},
		{Message: ""},
		{Message: "", Details: "0"},
	}
	rng := rand.New(rand.NewSource(20261012))
	pieces := []string{"&", "<", ">", `"`, "'", "&amp;", "&lt;", "&#39;", "&#34;", "&quot;", "\n", "\r\n", "\t", " ", "text", "日本語", "😀", "e\u0301", "\u2028", "${x}", "`", "\\", "</div>", "<script>", strings.Repeat("a", 200), "\x00", "\u00a0"}
	random := func() string {
		var b strings.Builder
		for range rng.Intn(9) {
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
		return b.String()
	}
	for range 300 {
		cases = append(cases, oauthPageCase{Message: random(), Details: random()})
	}
	return cases
}

// TestOAuthPageCorpusMatchesThePinnedPackage renders the corpus with Pi's oauth-page.ts and with PiG: the success page of each message and the error page of each message and details agree byte for byte.
func TestOAuthPageCorpusMatchesThePinnedPackage(t *testing.T) {
	cases := oauthPageCorpus()
	input, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	const script = `(async () => {
  const root = process.argv[1], input = require('node:fs').readFileSync(0, 'utf8');
  const page = await import(require('node:url').pathToFileURL(require('node:path').join(root, 'node_modules/@earendil-works/pi-ai/dist/utils/oauth-page.js')).href);
  console.log(JSON.stringify(JSON.parse(input).flatMap((c) => [page.oauthSuccessHtml(c.message), page.oauthErrorHtml(c.message, c.details)])));
})()`
	// The corpus goes on stdin: as an argument it overflows Windows' 32 KiB command line.
	cmd := exec.CommandContext(t.Context(), "node", "-e", script, piCodingAgentPackage(t))
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("render Pi's pages: %v\n%s", err, out)
	}
	var want []string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatalf("decode Pi's pages: %v", err)
	}
	if len(want) != 2*len(cases) {
		t.Fatalf("Pi rendered %d pages for %d cases", len(want), len(cases))
	}
	failures := 0
	for i, c := range cases {
		if got := OAuthSuccessHTML(c.Message); got != want[2*i] {
			if failures++; failures <= 3 {
				t.Errorf("OAuthSuccessHTML(%q):\n got %q\nwant %q", c.Message, got, want[2*i])
			}
		}
		if got := OAuthErrorHTML(c.Message, c.Details); got != want[2*i+1] {
			if failures++; failures <= 3 {
				t.Errorf("OAuthErrorHTML(%q, %q):\n got %q\nwant %q", c.Message, c.Details, got, want[2*i+1])
			}
		}
	}
	if failures > 3 {
		t.Errorf("%d pages differ from Pi", failures)
	}
}

// TestOAuthPageCorpusDump prints the corpus for the Pi side of the oauth/32 parity scenario.
func TestOAuthPageCorpusDump(t *testing.T) {
	line, err := json.Marshal(oauthPageCorpus())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("oauth-page-probes:%s\n", line)
}

// TestOAuthPageCorpusParity prints PiG's pages, one JSON line per page, for the oauth/32 parity scenario.
func TestOAuthPageCorpusParity(t *testing.T) {
	for _, c := range oauthPageCorpus() {
		for _, page := range []string{OAuthSuccessHTML(c.Message), OAuthErrorHTML(c.Message, c.Details)} {
			fmt.Printf("oauth-page-observation:%s\n", jsonLine(page))
		}
	}
}

// jsonLine is value as one JSON line the way JSON.stringify writes it, except that U+2028 and U+2029 stay escaped (the Pi script escapes them too).
func jsonLine(value string) string {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		panic(err)
	}
	return strings.TrimSuffix(out.String(), "\n")
}
