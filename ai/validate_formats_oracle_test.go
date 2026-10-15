package ai

// pi: packages/ai/src/utils/validation.ts

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/internal/typebox"
)

type formatProbe struct {
	Format string `json:"format"`
	Value  string `json:"value"`
}

// formatProbes mutate seed strings of each ported format (valid ones and near misses) by deleting, inserting and replacing characters from a pool of separators, digits, letters, percent escapes, whitespace and
// non-ASCII letters (including U+017F and U+212A, which a case-insensitive Go expression would fold into ASCII letters).
func formatProbes() []formatProbe {
	random := rand.New(rand.NewSource(20260954))
	seeds := map[string][]string{
		"date":                      {"2024-02-29", "2023-02-28", "1900-02-29", "2000-02-29", "2024-01-00", "2024-00-10", "2024-04-31", "2024-12-31", "0000-01-01", "2024-1-01"},
		"time":                      {"10:00:00Z", "23:59:60Z", "23:59:60+00:00", "22:59:60-01:00", "00:59:60+01:00", "01:00:60-00:00", "12:00:60+12:00", "23:59:60-23:59", "00:00:00.5+23:59", "12:30:45z", "12:30:45", "24:00:00Z", "10:60:00Z", "10:00:61Z", "10:00:00+24:00"},
		"date-time":                 {"2024-02-29T10:00:00Z", "2024-02-29t10:00:00+01:00", "2024-02-29 10:00:00Z", "2024-02-29T23:59:60Z", "2024-02-29T10:00:00", "2024-02-29T10:00:00T", "2024-02-29T10:00:00ZT"},
		"duration":                  {"P1Y2M3DT4H5M6S", "P1W", "PT1H", "P1Y", "P1M2D", "P1Y2D", "PT", "P", "P1W2D", "pt1h", "P1.5Y"},
		"email":                     {"a@b.co", "A.B+c@example.com", "\"a b\"@x.y", "a@[1.2.3.4]", "a@[IPv6:::1]", "a@b", "a..b@c.d", ".a@b.c", "a@-b.c", "a@b-.c", "a@" + string(bytes.Repeat([]byte("a"), 64)) + ".c"},
		"ipv4":                      {"1.2.3.4", "255.255.255.255", "256.1.1.1", "01.2.3.4", "1.2.3", "1.2.3.4.5", "0.0.0.0"},
		"ipv6":                      {"::1", "2001:db8::1", "::ffff:1.2.3.4", "1:2:3:4:5:6:7:8", "1::2::3", ":::", "fe80::1%eth0", "::", "1:2:3:4:5:6:1.2.3.4", "1:2:3:4:5:6:7:8:9"},
		"iri":                       {"https://example.com/a", "http://exämple.com/ü", "mailto:a@b", "http://[v1.x]/", "http://a b", "http://x/%zz", "http://x/%41", "a/b", "//host", "http://x/{y}", "http://x/<y>", "file:///a"},
		"iri-reference":             {"/a/b?c#d", "a/b", "//host/p", "http://example.com", "httpx//example.com", "http://a b", "a%zz", "?q", "#f", "", "../x", "http://[::1]/"},
		"json-pointer":              {"", "/", "/a/b", "/a~0b", "/a~1b", "/a~2", "a/b", "/ü", "//"},
		"json-pointer-uri-fragment": {"#", "#/a/b", "#/a%20b", "#/a~0", "#/a~2", "/a", "#/ü", "#/a%2"},
		"relative-json-pointer":     {"0", "1/a/b", "2#", "01/a", "0#/a", "10", "1~", "1/~0"},
		"uri":                       {"https://example.com/a?b=c#d", "mailto:a@b", "urn:isbn:1", "//host/p", "/rel", "a/b", "http://[::1]:80/", "http://[v1.x]/", "http://a b", "http://x/%41", "http://x/%4", "HTTP://X.Y", "ftp://u:p@h:21/x", "a:b", "1a:b", "http://x:/"},
		"uri-reference":             {"/rel/path", "a/b", "//host/p", "https://x/y?z#f", "?q", "#f", "", "a b", "http://[::1]", "%zz", "a:b/c", "./x:y"},
		"uri-template":              {"{var}", "{+path}/here{?q*}", "{ok", "a{b}c", "{a,b}", "{a:3}", "{a:0}", "{a.b}", "{}", "{/a,b*}", "x%41y", "x%4y", "x y", "{ſ}", "é"},
		"url":                       {"https://example.com/a", "mailto:a@b", "//host", "a/b", "http://a b", "http://x:99999/", "file:///a", "foo://h", "http://[::1]/", "http://0x7f.1/", "javascript:0"},
		"uuid":                      {"123e4567-e89b-12d3-a456-426614174000", "123E4567-E89B-12D3-A456-426614174000", "123e4567e89b12d3a456426614174000", "123e4567-e89b-12d3-a456-42661417400", "g23e4567-e89b-12d3-a456-426614174000"},
	}
	pool := []string{"", "-", ".", ":", "/", "@", "#", "?", "%", "%41", "%zz", "~", "{", "}", "[", "]", " ", "\t", "0", "1", "9", "a", "f", "g", "Z", "T", "P", "\u00e9", "\u017f", "\u212a", "\u00a0", "\\", "<", "\"", "😀"}
	formats := make([]string, 0, len(seeds))
	for name := range seeds {
		formats = append(formats, name)
	}
	slices.Sort(formats)
	var probes []formatProbe
	for _, name := range formats {
		for _, seed := range seeds[name] {
			probes = append(probes, formatProbe{name, seed})
			for range 120 {
				runes := []rune(seed)
				for range 1 + random.Intn(3) {
					position := random.Intn(len(runes) + 1)
					switch random.Intn(3) {
					case 0:
						if position < len(runes) {
							runes = append(runes[:position], runes[position+1:]...)
						}
					case 1:
						runes = append(runes[:position], append([]rune(pool[random.Intn(len(pool))]), runes[position:]...)...)
					default:
						if position < len(runes) {
							replacement := []rune(pool[random.Intn(len(pool))])
							runes = append(runes[:position], append(replacement, runes[position+1:]...)...)
						}
					}
				}
				probes = append(probes, formatProbe{name, string(runes)})
			}
		}
	}
	return probes
}

// typebox's format registry runs in Node through Value.Check on the same strings: each ported format in typebox.Formats must accept exactly the strings TypeBox accepts.
func TestTypeboxFormatsMatchPiOnSeededStrings(t *testing.T) {
	probes := formatProbes()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/typebox_formats.mjs", testenv.ModuleRoot(t), pinnedTypeboxVersion)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle (npm ci --ignore-scripts in internal/typebox/testdata/npm first): %v\n%s", err, &stderr)
	}
	var expected []bool
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("typebox answered %d of %d probes", len(expected), len(probes))
	}
	accepted := map[string]int{}
	differing := 0
	for i, probe := range probes {
		got := typebox.Formats()[probe.Format](probe.Value)
		if expected[i] {
			accepted[probe.Format]++
		}
		if got != expected[i] {
			if differing++; differing <= 12 {
				t.Errorf("format %s %q: Pig %v, typebox %v", probe.Format, probe.Value, got, expected[i])
			}
		}
	}
	if differing > 12 {
		t.Errorf("%d of %d probes differ from typebox", differing, len(probes))
	}
	for name := range typebox.Formats() {
		if accepted[name] < 3 || accepted[name] > len(probes)-3 {
			t.Errorf("format %s probes are one-sided: %d accepted", name, accepted[name])
		}
	}
}
