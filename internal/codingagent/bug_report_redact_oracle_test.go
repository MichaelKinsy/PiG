package codingagent

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/coding-agent/src/core/bug-report.ts redactUrl, redactJsonValue

// The credential redaction of a bug report against pinned Pi: URLs (schemes, nested schemes, userinfo, ports, IPv6 and IDN hosts, dot segments,
// percent-encoding, repeated and mixed-case query keys, fragments, strings that are not URLs) and JSON values (sensitive keys in snake, kebab and
// camel case, nested objects and arrays, null members, strings that are URLs).
func TestBugReportRedactionMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(179))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	quote := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	keys := []string{"api_key", "apiKey", "API-KEY", "api-key", "token", "access_token", "accessToken", "x-token", "secret", "client_secret", "password", "passwd", "credential", "credentials", "authorization", "cookie", "Cookie", "mytoken", "tokens", "key", "name", "q", "page", "auth", "session", "apikey", "Token", "a b", "é", "token_", "_token", "ToKeN"}
	values := []string{"v", "", "a b", "a+b", "%41", "x&y", "é", "😀", "1", "=", "a=b"}
	url := func() string {
		scheme := pick("https", "http", "ftp", "ws", "file", "git+https", "ssh", "custom", "HTTPS")
		var b strings.Builder
		b.WriteString(scheme + "://")
		if rng.Intn(3) == 0 {
			b.WriteString(pick("user", "u:p", "user:pa%40ss", ":pw", "u:"))
			b.WriteString("@")
		}
		b.WriteString(pick("example.com", "EXAMPLE.com", "api.example.com:8443", "localhost:80", "127.0.0.1", "[::1]:3000", "bücher.de", "a.b.c", "example.com:443"))
		b.WriteString(pick("", "/", "/a/b", "/a/../b", "/a/./b", "/a%20b", "/é", "/a b", "//x"))
		if rng.Intn(10) > 1 {
			var parts []string
			for range 1 + rng.Intn(5) {
				key := keys[rng.Intn(len(keys))]
				if rng.Intn(4) == 0 {
					parts = append(parts, key)
				} else {
					parts = append(parts, key+"="+pick(values...))
				}
			}
			b.WriteString("?" + strings.Join(parts, pick("&", "&", ";")))
		}
		if rng.Intn(6) == 0 {
			b.WriteString("#" + pick("frag", "token=x", ""))
		}
		return b.String()
	}
	var urls []string
	for range 1500 {
		u := url()
		switch rng.Intn(14) {
		case 0:
			u = pick("git:", "npm:", "jdbc:", "x+y:") + u
		case 1:
			u = pick("not a url", "", "C:\\path\\x", "a:b", "//host/x?token=1", "/relative?api_key=1", "https://", "https://[bad", "mailto:a@b.c?token=1", "data:text/plain,token=1", "http://a b/", " https://example.com/?token=1", "https://example.com/?token=1 ")
		}
		urls = append(urls, u)
	}
	var jsons []string
	var gen func(depth int) string
	gen = func(depth int) string {
		switch r := rng.Intn(10); {
		case r < 4 && depth < 4:
			var members []string
			for range rng.Intn(5) {
				members = append(members, quote(keys[rng.Intn(len(keys))])+":"+gen(depth+1))
			}
			return "{" + strings.Join(members, ",") + "}"
		case r < 5 && depth < 4:
			var items []string
			for range rng.Intn(4) {
				items = append(items, gen(depth+1))
			}
			return "[" + strings.Join(items, ",") + "]"
		case r < 7:
			return quote(url())
		case r < 8:
			return quote(pick("plain", "", "secret"))
		case r < 9:
			return pick("null", "true", "false", "0", "1.5", "-0", "1e21", "123456789012345678901234567890")
		default:
			return "null"
		}
	}
	for range 800 {
		jsons = append(jsons, gen(0))
	}
	input, _ := json.Marshal(map[string]any{"urls": urls, "values": jsons})
	cmd := exec.CommandContext(t.Context(), "node", "testdata/bug_report_redact.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want struct {
		URLs   []string          `json:"urls"`
		Values []json.RawMessage `json:"values"`
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	failures, changed := 0, 0
	for i, u := range urls {
		got := RedactBugReportURL(u)
		if got != u {
			changed++
		}
		if got != want.URLs[i] {
			if failures++; failures <= 8 {
				t.Errorf("url %q:\n PiG %q\n Pi  %q", u, got, want.URLs[i])
			}
		}
	}
	for i, text := range jsons {
		var value any
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			t.Fatal(err)
		}
		redacted, err := RedactBugReportJSON(value)
		if err != nil {
			t.Fatal(err)
		}
		gotJSON, _ := json.Marshal(redacted)
		// Both sides decode with UseNumber, so a number's spelling (1e+21 against 1e21, 0 against -0) is compared, not only its value.
		if !reflect.DeepEqual(decodeNumbers(t, gotJSON), decodeNumbers(t, want.Values[i])) {
			if failures++; failures <= 8 {
				t.Errorf("value %s:\n PiG %s\n Pi  %s", text, gotJSON, want.Values[i])
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d differences from Pi", failures)
	}
	if changed < 300 {
		t.Fatalf("Pi redacted only %d of %d URLs", changed, len(urls))
	}
}

// redactJsonValue is JSON.parse(JSON.stringify(value)), so each number passes through a double.
func TestBugReportRedactionNormalizesNumbersLikeJSONStringify(t *testing.T) {
	value, err := RedactBugReportJSON(map[string]any{"a": json.Number("-0"), "b": json.Number("1e21"), "c": json.Number("123456789012345678901234567890"), "d": json.Number("1.50"), "e": json.Number("7")})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(value)
	if string(encoded) != `{"a":0,"b":1e+21,"c":1.2345678901234568e+29,"d":1.5,"e":7}` {
		t.Fatalf("%s", encoded)
	}
}

func decodeNumbers(t *testing.T, raw []byte) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}
