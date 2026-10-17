package llama

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// normalizeLlamaServerUrl and llamaInferenceUrl against Pi's client.js over server URL spellings: scheme case and other schemes, user info, default and explicit
// ports, IPv4 and IPv6 hosts, IDN and upper-case hosts, repeated, encoded and backslash slashes, /v1 suffixes, dot segments, query and fragment, surrounding JS whitespace, and strings
// that are not URLs.
func TestNormalizeLlamaServerURLMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(15))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	var inputs []string
	for range 6000 {
		var b strings.Builder
		b.WriteString(pick("", "", " ", "\t", "\ufeff", "\u00a0", "\u0085", "\n"))
		b.WriteString(pick("http://", "https://", "HTTP://", "HtTpS://", "http:/", "http:", "ftp://", "ws://", "file:///", "localhost:", "//", "", "http:\\\\", "https:/\\"))
		b.WriteString(pick("", "", "user@", "user:pw@", "us%40er:p%3Aw@", "a:b:c@", "@"))
		b.WriteString(pick("localhost", "LOCALHOST", "127.0.0.1", "0x7f.1", "[::1]", "[0:0:0:0:0:0:0:1]", "[::ffff:1.2.3.4]", "example.com", "EXAMPLE.com", "bücher.example", "xn--bcher-kva.example", "a b", "host.", "", "1.2.3", "256.1.1.1", "ex%41mple.com", "ex_ample.com", "a..b"))
		b.WriteString(pick("", "", ":8080", ":80", ":443", ":0", ":65535", ":65536", ":080", ":", ":abc"))
		b.WriteString(pick("", "/", "//", "/v1", "/v1/", "/api", "/api/", "/api/v1", "/api/v1//", "/a/../b", "/./a", "/%2e%2e/a", "/a/%2E", "/a b", "/é", "/a%2Fb", "/a\\b", "/v1/v1", "/V1", "/a?x=1", "/v1?x", "/a#f", "?q=1", "#frag", "/a/./", "/a/..", "/%", "/%zz", "/a|b", "/{x}", "/a\tb", "/a\nb"))
		b.WriteString(pick("", "", "/", "?k=v", "#h", "/v1", "//"))
		b.WriteString(pick("", "", " ", "\t", "\n", "\ufeff"))
		inputs = append(inputs, b.String())
	}
	inputs = append(inputs, "", " ", "not a url", "http://", "http://:80", "https://[::1", "mailto:x@y", "data:,x", "javascript:alert(1)", "HTTP://EXAMPLE.COM:80/V1/")
	type result struct {
		Out  string `json:"out"`
		Err  string `json:"err"`
		Inf  string `json:"inf"`
		IErr string `json:"ierr"`
	}
	var want []result
	pioracle.Run(t, `
const m = await load("pi-coding-agent/extensions/llama/client.js");
emit(input.map((value) => {
	const r = { out: "", err: "", inf: "", ierr: "" };
	try { r.out = m.normalizeLlamaServerUrl(value); } catch (e) { r.err = e.message; }
	try { r.inf = m.llamaInferenceUrl(value); } catch (e) { r.ierr = e.message; }
	return r;
}));
`, inputs, &want)
	bad := 0
	for i, in := range inputs {
		out, err := NormalizeLlamaServerURL(in)
		inf, ierr := LlamaInferenceURL(in)
		w := want[i]
		got := result{Out: out, Inf: inf}
		if err != nil {
			got.Err = err.Error()
		}
		if ierr != nil {
			got.IErr = ierr.Error()
		}
		if got != w {
			if bad++; bad <= 12 {
				t.Errorf("%q:\n got %+v\nwant %+v", in, got, w)
			}
		}
	}
	if bad > 12 {
		t.Errorf("%d of %d URLs differ from Pi", bad, len(inputs))
	}
}

// formatBytes over byte counts around every unit boundary, ties at the toFixed digit, negative and fractional values.
func TestFormatBytesMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	values := []float64{0, -1, -1500, 1, 1023, 1023.5, 1024, 1025, 1536, 10239, 10240, 10 * 1024 * 1024, 1048575, 1048576, 1 << 30, 1<<40 - 1, 1 << 40, 1 << 50, 1e21, 1e300, 0.5, 5e-324}
	for range 4000 {
		values = append(values, float64(rng.Int63n(1<<45))/float64(int(1)<<rng.Intn(12)), rng.Float64()*float64(int(1)<<rng.Intn(50)))
	}
	var want []string
	pioracle.Run(t, `
const m = await load("pi-coding-agent/extensions/llama/client.js");
emit(input.map((x) => m.formatBytes(x)));
`, values, &want)
	for i, v := range values {
		if got := FormatBytes(v); got != want[i] {
			t.Fatalf("FormatBytes(%v) = %q, Pi %q", v, got, want[i])
		}
	}
}
