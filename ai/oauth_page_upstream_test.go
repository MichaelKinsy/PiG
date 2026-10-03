package ai

import (
	"encoding/json"
	"os/exec"
	"testing"
)

// Pi's oauth-page.ts serves one fixed HTML template and escapes the title, heading, message and details with its escapeHtml (& < > " ' as &amp; &lt; &gt; &quot; &#39;); a falsy details value omits the details block. The callback servers send these bytes to the browser, so PiG's pages equal the pinned package's pages byte for byte.
func TestOAuthPageMatchesThePinnedPackage(t *testing.T) {
	type pageCase struct {
		Message string `json:"message"`
		Details string `json:"details"`
	}
	cases := []pageCase{
		{Message: "Signed in to Anthropic. You may now close this page."},
		{Message: "Anthropic authorization failed.", Details: `<script>alert(1)</script>"'&`},
		{Message: `bad <tag> & "quote" 'apos'`, Details: "line1\nline2 & more"},
		{Message: "State mismatch."},
		{Message: "already escaped &#34; &quot; &#39;", Details: "&amp;#34;"},
	}
	input, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	const script = `(async () => {
  const [root, input] = process.argv.slice(1);
  const page = await import(require('node:url').pathToFileURL(require('node:path').join(root, 'node_modules/@earendil-works/pi-ai/dist/utils/oauth-page.js')).href);
  console.log(JSON.stringify(JSON.parse(input).flatMap((c) => [page.oauthSuccessHtml(c.message), page.oauthErrorHtml(c.message, c.details)])));
})()`
	out, err := exec.CommandContext(t.Context(), "node", "-e", script, piCodingAgentPackage(t), string(input)).CombinedOutput()
	if err != nil {
		t.Fatalf("render Pi's pages: %v\n%s", err, out)
	}
	var want []string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatalf("decode Pi's pages: %v\n%s", err, out)
	}
	if len(want) != 2*len(cases) {
		t.Fatalf("Pi rendered %d pages for %d cases", len(want), len(cases))
	}
	for index, tc := range cases {
		if got := OAuthSuccessHTML(tc.Message); got != want[2*index] {
			t.Errorf("OAuthSuccessHTML(%q):\n got %q\nwant %q", tc.Message, got, want[2*index])
		}
		if got := OAuthErrorHTML(tc.Message, tc.Details); got != want[2*index+1] {
			t.Errorf("OAuthErrorHTML(%q, %q):\n got %q\nwant %q", tc.Message, tc.Details, got, want[2*index+1])
		}
	}
}
