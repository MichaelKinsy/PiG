package nodeurl

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"
)

type nodeURLAnswer struct {
	OK       bool   `json:"ok"`
	Protocol string `json:"protocol"`
	Hostname string `json:"hostname"`
	Search   string `json:"search"`
}

// parseInputs cross schemes (special, file, non-special, invalid) with authorities (none, empty, userinfo, ports in and out of range, IPv6, IPv4
// and numeric hosts, IDN, percent escapes, forbidden code points, backslashes) and tails (path, query with quotes and non-ASCII, fragment), plus
// whitespace the parser strips or removes.
func parseInputs() []string {
	schemes := []string{"http:", "HTTPS:", "ws:", "wss:", "ftp:", "file:", "x:", "Ssh:", "a+b-c.d:", "1x:", "", ":", "x y:"}
	authorities := []string{
		"", "//", "///", "//host", "//Host.Example", "//user@host", "//user:pw@host", "//u@", "//@host", "//host:", "//host:0", "//host:21", "//host:65535", "//host:65536",
		"//host:99999", "//host:-1", "//host:0x1", "//host:1:2", "//:1", "//:", "//[::1]", "//[::1]:8", "//[::1", "//[::1]x", "//[zz]", "//[fe80::1%25eth0]", "//1.2.3", "//1.2.3.256",
		"//0x7f.1", "//münchen", "//xn--", "//ex%41mple", "//%zz", "//a b", "//a<b", "//a^b", "//a|b", "//a%00b", "//a\x01b", "//\\host", "//host\\x", "//localhost", "host", "/host", "\\\\host",
	}
	tails := []string{"", "/", "/p a/t", "?code=1&state=2", "/cb?code=a%20b&state=s+t", "?q='x'\"<>", "?é=ü#frag", "#h?x", "?a#b?c", "?code=%zz;a", "/%zz?code=1"}
	var out []string
	for i, scheme := range schemes {
		for j, authority := range authorities {
			out = append(out, scheme+authority+tails[(i+j)%len(tails)])
		}
	}
	for _, tail := range tails {
		out = append(out, "x://h"+tail, "http://h"+tail, "file:///c:/x"+tail)
	}
	return append(out, " \thttp://h/?code=1\n", "x:y?code=a\tb", "x://h\n:1/", "javascript:alert(1)", "data:text/plain,code=1", "mailto:x@y.z?code=1", "x:%zz?c=1")
}

// Node's `new URL(raw)` (testdata/url.mjs, one process) decides each probe: ParseURL must fail where Node throws and otherwise report Node's protocol,
// hostname and search.
func TestParseMatchesNodeURL(t *testing.T) {
	inputs := parseInputs()
	payload, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/url.mjs")
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, &stderr)
	}
	var expected []nodeURLAnswer
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(inputs) {
		t.Fatalf("node answered %d of %d inputs", len(expected), len(inputs))
	}
	differing := 0
	for i, input := range inputs {
		parsed, err := ParseURL(input)
		got := nodeURLAnswer{OK: err == nil, Protocol: parsed.Protocol, Hostname: parsed.Hostname}
		if parsed.Query != "" {
			got.Search = "?" + parsed.Query
		}
		if got != expected[i] {
			if differing++; differing <= 15 {
				t.Errorf("ParseURL(%q) = %+v, Node %+v", input, got, expected[i])
			}
		}
	}
	if differing > 15 {
		t.Errorf("%d of %d inputs differ from Node", differing, len(inputs))
	}
}
