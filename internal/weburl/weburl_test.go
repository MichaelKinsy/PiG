package weburl

// pi: packages/coding-agent/src/utils/web-url.ts (Pi has no test file for it; login-dialog and auth-login use it)

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type webURLCase struct {
	name  string
	value string
	// href is the normalized web URL, empty when WebURL refuses value.
	href string
	// display is what DisplayURL returns.
	display string
}

// The expectations are Node's: testdata/web_url.mjs runs Pi's webUrl and displayUrl on each value and TestWebURLMatchesNode compares both functions to it.
func webURLCases() []webURLCase {
	return []webURLCase{
		{name: "https without a path gets one", value: "https://example.com", href: "https://example.com/"},
		{name: "scheme and host case fold, default port drops, space in the path encodes", value: "HTTP://EXAMPLE.com:80/a b", href: "http://example.com/a%20b"},
		{name: "default https port drops", value: "https://example.com:443/", href: "https://example.com/"},
		{name: "a non-default port stays", value: "http://example.com:8080", href: "http://example.com:8080/"},
		{name: "control characters percent-encode in the path and vanish from an empty query value", value: "http://a/\x1b[31m?x=\x07", href: "http://a/%1B[31m?x="},
		{name: "userinfo, query and fragment spaces encode", value: "https://user:pa ss@host/p?q=a b#f c", href: "https://user:pa%20ss@host/p?q=a%20b#f%20c"},
		{name: "surrounding whitespace is stripped", value: "  https://example.com/x  ", href: "https://example.com/x"},
		{name: "leading control characters are stripped", value: "  \x1bhttps://x", href: "https://x/"},
		{name: "tabs and newlines inside are removed", value: "https://example.com/a\nb", href: "https://example.com/ab"},
		{name: "an IDN host is punycoded and the path percent-encoded as UTF-8", value: "https://éxample.com/ü", href: "https://xn--xample-9ua.com/%C3%BC"},
		{name: "an IPv6 host with a port", value: "https://[::1]:3000/x", href: "https://[::1]:3000/x"},
		{name: "backslashes are path separators and a single slash is accepted", value: "HTTPS:/\\example.com/a\\b", href: "https://example.com/a/b"},
		{name: "query characters Node encodes", value: "https://example.com/?q=\"<>`{}|", href: "https://example.com/?q=%22%3C%3E`{}|"},
		{name: "DEL and a C1 control encode as UTF-8", value: "https://example.com/\x7f\u0085", href: "https://example.com/%7F%C2%85"},
		{name: "a host with a space does not parse", value: "https://exa mple.com", display: "https://exa mple.com"},
		{name: "an empty authority does not parse", value: "http://", display: "http://"},
		{name: "a scheme without an authority does not parse", value: "https:", display: "https:"},
		{name: "file: parses for URL.canParse but is not a web URL", value: "file:///etc/passwd", display: "file:///etc/passwd"},
		{name: "ftp: is not a web URL", value: "ftp://host/x", display: "ftp://host/x"},
		{name: "javascript: is not a web URL", value: "javascript:alert(1)", display: "javascript:alert(1)"},
		{name: "a custom protocol is not a web URL", value: "myapp://callback?code=1", display: "myapp://callback?code=1"},
		{name: "mailto: is not a web URL", value: "mailto:a@b.c", display: "mailto:a@b.c"},
		{name: "text that is no URL is kept", value: "not a url", display: "not a url"},
		{name: "the empty string is no URL", value: "", display: ""},
		{name: "a newline and an escape in a non-web value become single spaces", value: "line1\nline2\x1b[0m", display: "line1 line2 [0m"},
		{name: "a run of C0, DEL and C1 controls becomes one space", value: "file:///a\x00\x1b\x07\x7f\u0085\u009fb", display: "file:///a b"},
		{name: "a control character ending a non-web value becomes a trailing space", value: "x-custom:y\r\n", display: "x-custom:y "},
		{name: "U+00A0, just above the C1 range, is kept", value: "no url\u00a0here", display: "no url\u00a0here"},
	}
}

func TestWebURL(t *testing.T) {
	for _, tt := range webURLCases() {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := WebURL(tt.value)
			if got != tt.href || ok != (tt.href != "") {
				t.Fatalf("WebURL(%q) = %q, %v; want %q, %v", tt.value, got, ok, tt.href, tt.href != "")
			}
		})
	}
}

func TestDisplayURL(t *testing.T) {
	for _, tt := range webURLCases() {
		t.Run(tt.name, func(t *testing.T) {
			want := tt.display
			if tt.href != "" {
				want = tt.href
			}
			if got := DisplayURL(tt.value); got != want {
				t.Fatalf("DisplayURL(%q) = %q; want %q", tt.value, got, want)
			}
		})
	}
}

func TestWebURLMatchesNode(t *testing.T) {
	cases := webURLCases()
	values := make([]string, len(cases))
	for i, tt := range cases {
		values[i] = tt.value
	}
	payload, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), testenv.RequireQualifiedNode(t), "testdata/web_url.mjs")
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, &stderr)
	}
	var answers []struct {
		WebURL     *string `json:"webUrl"`
		DisplayURL string  `json:"displayUrl"`
	}
	if err := json.Unmarshal(output, &answers); err != nil {
		t.Fatal(err)
	}
	if len(answers) != len(cases) {
		t.Fatalf("node answered %d of %d values", len(answers), len(cases))
	}
	for i, tt := range cases {
		href, ok := WebURL(tt.value)
		if (answers[i].WebURL != nil) != ok || (ok && *answers[i].WebURL != href) {
			t.Errorf("WebURL(%q) = %q, %v; Node answered %v", tt.value, href, ok, answers[i].WebURL)
		}
		if got := DisplayURL(tt.value); got != answers[i].DisplayURL {
			t.Errorf("DisplayURL(%q) = %q; Node answered %q", tt.value, got, answers[i].DisplayURL)
		}
	}
}
