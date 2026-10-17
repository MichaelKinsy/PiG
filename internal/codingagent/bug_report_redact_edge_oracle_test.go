package codingagent

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Hand-picked edge spellings for Pi's redactUrl and redactJsonValue (core/bug-report.js), alongside TestBugReportRedactionMatchesPi's random corpus: credentials and secret-looking query parameters must be removed with the same URL serialization (WHATWG) for every URL spelling, and secret-looking JSON keys must be redacted at every depth.
func TestBugReportRedactionMatchesPiOracle(t *testing.T) {
	urls := []string{
		"", "plain text", "https://example.com", "https://example.com/", "https://example.com/path?x=1", "https://user:pw@example.com/path", "https://user@example.com", "https://:pw@example.com",
		"https://example.com/p?api_key=SECRET", "https://example.com/p?apiKey=SECRET", "https://example.com/p?API-KEY=SECRET&x=1", "https://example.com?token=a&token=b&c=d", "https://example.com/?a=1&token=x&b=2",
		"https://example.com/?x=%20y&password=p%40ss", "https://example.com/?x=a+b&secret=s", "https://example.com/?x=é&auth=1&authorization=Bearer+abc", "https://example.com/?cookie=1;a=b", "https://example.com/?=v&token", "https://example.com/?token", "https://example.com/?token=",
		"https://example.com/?my_token_value=1", "https://example.com/?mytoken=1", "https://example.com/?x-token-y=1", "https://example.com/?accessToken=1", "https://example.com/?clientSecret=1",
		"HTTPS://USER:PW@EXAMPLE.COM:443/Path", "https://example.com:8080/a?token=1#frag", "https://example.com/a b?token=1", "https://example.com/é?token=1", "https://exämple.com/?token=1",
		"http://u:p@[::1]:80/x", "http://u:p@127.0.0.1:8080/", "ftp://u:p@example.com/file", "ws://u:p@h/x", "wss://u:p@h:443", "file:///etc/passwd?token=1", "file://u:p@host/x",
		"git:https://user:pw@github.com/a/b.git", "git+https://user:pw@github.com/a/b.git", "npm:https://u:p@x/y", "git:git@github.com:a/b.git", "ssh://git:pw@host/repo", "ssh://git@host:22/repo?token=1",
		"mailto:user@example.com?token=1", "data:text/plain,token=1", "javascript:alert(1)", "custom://u:p@h/x", "custom:u:p@h", "a:b:c", "x:https://y:z@h/?token=1",
		"https://example.com\\path?token=1", "https:\\\\u:p@example.com\\x", " https://u:p@example.com ", "https://u:p@exa mple.com", "https://u:p@example.com/%zz?token=%zz", "https://u%40x:p%3Aw@example.com/",
		"https://example.com/?token=1&token=2&Token=3", "https://example.com/?a=1&a=2&token=3&a=4", "//u:p@example.com/x", "/relative?token=1", "example.com?token=1", "https://[::1]/?token=1", "https://example.com/.%2e/x?token=1",
		"https://u:p@example.com/?q=\u2028", "https://u:p@example.com/\t\n?token=1", "https://example.com/?token=%E0%A4%A", "https://u:p@0x7f.1/?token=1", "https://u:p@exämple.com/", "https://u:p@EXAMPLE.com./x", "http://u:p@example.com:80/", "http://u:p@example.com:8080/", "https://u:p@example.com/?%74oken=1", "https://u:p@example.com/?to%6Ben=1&a=%zz", "https://u:p@example.com/?a=1&&b=2&token=3", "https://u:@example.com/", "https://:@example.com/", "https://u:p@example.com/#token=1", "https://example.com/?x=1#frag", "https://example.com/a/../b/./c?token=1", "git:file://u:p@h/x", "x:y://u:p@h/?token=1",
	}
	values := []any{
		nil, "str", float64(1), true, map[string]any{}, []any{},
		map[string]any{"apiKey": "k", "name": "n", "nested": map[string]any{"password": "p", "token": nil, "ok": 1.0, "authorization": []any{"a"}}},
		map[string]any{"API_KEY": 1.0, "api-key": "x", "apikey": "y", "x_api_key_y": "z", "myApiKey": "q", "MY_SECRET_VALUE": "s", "secretive": "no", "tokens": "no", "tokenValue": "yes", "cookies": "no", "Cookie": "yes"},
		[]any{map[string]any{"credential": "c"}, "https://u:p@h/x?token=1", []any{"password", map[string]any{"passwd": 1.0}}},
		map[string]any{"url": "https://u:p@example.com/?token=1", "list": []any{"git:https://u:p@h/r.git"}, "n": map[string]any{"deep": map[string]any{"deeper": map[string]any{"secret": "s", "u": "http://a:b@c/"}}}},
		map[string]any{"camelCaseToken": "t", "snake_case_token": "t", "kebab-case-token": "t", "UPPERToken": "t", "tokenUPPER": "t", "a1Token": "t", "9Token": "t", "Token": "t", "TOKEN": "t"},
		map[string]any{"": "empty key", "token ": "trailing space", " token": "leading", "to ken": "mid", "apiKey2": "x", "api key": "x", "apiKEY": "x"},
		map[string]any{"password": map[string]any{"nested": "kept?"}, "token": []any{1.0, 2.0}, "secret": false, "cookie": 0.0, "authorization": ""},
	}
	type input struct {
		URLs   []string `json:"urls"`
		Values []any    `json:"values"`
	}
	var want struct {
		URLs   []string `json:"urls"`
		Values []any    `json:"values"`
	}
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/bug-report.js");
emit({ urls: input.urls.map((u) => mod.redactUrl(u)), values: input.values.map((v) => mod.redactJsonValue(v) ?? null) });`, input{urls, values}, &want)
	for i, raw := range urls {
		if got := RedactBugReportURL(raw); got != want.URLs[i] {
			t.Errorf("redactUrl(%q):\n Go %q\n Pi %q", raw, got, want.URLs[i])
		}
	}
	for i, value := range values {
		got, err := RedactBugReportJSON(value)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(normalizeRedacted(got), want.Values[i]) {
			t.Errorf("redactJsonValue(%v):\n Go %v\n Pi %v", value, got, want.Values[i])
		}
	}
}

// normalizeRedacted turns json.Number leaves (UseNumber) into float64 so the decoded Pi value compares equal.
func normalizeRedacted(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[key] = normalizeRedacted(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = normalizeRedacted(child)
		}
		return out
	case interface{ Float64() (float64, error) }:
		f, _ := typed.Float64()
		return f
	default:
		return value
	}
}
