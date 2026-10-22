package nodeurl

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/rand"
	"os/exec"
	"strings"
	"testing"

	"golang.org/x/net/idna"
)

// fileURLCases covers hosts (IDNA, case, percent-encoding, IPv4 shorthand,
// IPv6, localhost, invalid), drive letters (including C| and a drive where
// the host goes), dot segments (plain and encoded), encoded separators,
// escapes, a query and fragment, whitespace, backslashes and other schemes.
var fileURLCases = []string{
	"file://xn--mnich-kva/share/zsh.exe", "file://münich/share/zsh.exe", "file://m%C3%BCnich/x",
	"file://SERVER/Share/x.exe", "file://a%41b/x", "file://ab--c/x", "file://xn--a.b/x", "file://xn--/x", "file://a.xn--.b/x", "file://XN--a/x", "file://xn--ls8h/x", "file://xn--a-ecp.ru/x",
	"file://0x7f.1/share/x", "file://1.2.3.4.5/x", "file://[::1]/share/x", "file://[fe80::1%25eth0]/x",
	"file://localhost/C:/x/y", "file://LOCALHOST/C:/x/y", "file://server", "file://server/C:/x",
	"file://ser ver/x", "file://server:8080/x", "file://user@server/x", "file://%/x",
	"file:///C:/Program%20Files/x", "file:///c:/x", "file:///C|/x", "file:///c%7C/x", "file://C:/x", "file:/C:/x",
	"file:///C:/a/../b", "file:///C:/a/..", "file:///C:/..", "file:///C:/a/./b/.", "file:///C:/%2e%2e/x",
	"file://server/share/%2e%2E/x", "file:///C:/a/b/../../..", "file:///C://x",
	"file://server/share/a%2Fb", "file://server/share/a%5Cb", "file:///C:/a%2fb",
	"file:///C:/%E2%82%AC", "file:///C:/100%", "file:///C:/%E2%82", "file:///C:/a%20b?q=1#f",
	"file:\\\\server\\share\\x", "FILE:///C:/x", " file:///C:/x ", "file:///C:/a\tb",
	"file:///x/y", "file://", "file:///", "http://server/x", "not a url",
}

// outcome is a path, or the code of the error Node throws (URIError for
// decodeURIComponent).
func outcome(path string, err error) string {
	if err == nil {
		return "path:" + path
	}
	var nodeErr *Error
	if errors.As(err, &nodeErr) && nodeErr.Code != "" {
		return "throw:" + nodeErr.Code
	}
	return "throw:URIError"
}

// FileURLToPath matches Node's url.fileURLToPath on every case, for both
// platforms, when Node is installed.
func TestFileURLToPathMatchesNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	input, err := json.Marshal(fileURLCases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "--input-type=module", "--eval",
		`import { fileURLToPath } from "node:url"; import { readFileSync } from "node:fs"; const run = (u, windows) => { try { return "path:" + fileURLToPath(u, { windows }); } catch (e) { return "throw:" + (e.code || e.name); } }; process.stdout.write(JSON.stringify(JSON.parse(readFileSync(0, "utf8")).map((u) => [run(u, true), run(u, false)])));`)
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("node fileURLToPath: %v", err)
	}
	var want [][2]string
	if err := json.Unmarshal(output, &want); err != nil || len(want) != len(fileURLCases) {
		t.Fatalf("node fileURLToPath output %q: %v", output, err)
	}
	for i, raw := range fileURLCases {
		for j, windows := range []bool{true, false} {
			if got := outcome(FileURLToPath(raw, windows)); got != want[i][j] {
				t.Errorf("FileURLToPath(%q, windows=%t) = %s, want Node's %s", raw, windows, got, want[i][j])
			}
		}
	}
}

// DomainToUnicode matches Node's url.domainToUnicode on hosts with "xn--" labels: valid Punycode, Punycode that decodes to ASCII or to a label that is unstable or invalid under UTS #46, and random payloads. Node 24.22.1 and 26.12.1 (ada 4) keep any label they cannot decode and validate; Node 22.19 and 24.19 (ada 2 and 3) refuse the host instead, so this runs on the qualified Node (.node-version).
func TestDomainToUnicodeMatchesNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	hosts := []string{"xn--mnich-kva.example", "xn--ls8h", "xn--a", "xn--", "a.xn--", "xn--a-ecp.ru", "xn--Mnchen-3ya.de", "xn--bcher-kva.de", "xn--zzzzzz", "xn--a-", "xn--abc", "xn--mgbh0fb", "xn--mnchen-3ya.xn--a.b", "XN--LS8H", "a.b", "127.0.0.1"}
	rng := rand.New(rand.NewSource(20261021))
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789-"
	for range 400 {
		payload := make([]byte, 1+rng.Intn(8))
		for i := range payload {
			payload[i] = alphabet[rng.Intn(len(alphabet))]
		}
		hosts = append(hosts, "xn--"+string(payload))
	}
	// Labels the encoder produced from characters UTS #46 maps, disallows or restricts, which decode to a label that is not stable or not valid.
	pool := []string{"é", "ü", "日本", "ß", "İ", "ǅ", "Ａ", "\u0301", "\u200d", "\u200c", "ا", "ب", "1", "a", "-", "A", "😀", "\u00ad", "\u0378", "·", "\u30fb", "ς", "\u0640", "\u094d", "ٰ", "ℌ", "Å", "a\u030a", "\ufb01", "\u3002"}
	for range 600 {
		var b strings.Builder
		for range 1 + rng.Intn(5) {
			b.WriteString(pool[rng.Intn(len(pool))])
		}
		if label, err := idna.Punycode.ToASCII(b.String()); err == nil && strings.HasPrefix(label, "xn--") && !strings.Contains(label, ".") {
			hosts = append(hosts, label)
		}
	}
	input, err := json.Marshal(hosts)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "--input-type=module", "--eval",
		`import { domainToUnicode } from "node:url"; import { readFileSync } from "node:fs"; process.stdout.write(JSON.stringify(JSON.parse(readFileSync(0, "utf8")).map((h) => domainToUnicode(h))));`)
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("node domainToUnicode: %v", err)
	}
	var want []string
	if err := json.Unmarshal(output, &want); err != nil || len(want) != len(hosts) {
		t.Fatalf("node domainToUnicode output %q: %v", output, err)
	}
	decoded := 0
	for i, raw := range hosts {
		host, err := SpecialHost(raw)
		if err != nil {
			t.Errorf("SpecialHost(%q): %v", raw, err)
			continue
		}
		got := DomainToUnicode(host)
		if got != want[i] {
			t.Errorf("DomainToUnicode(%q) = %q, Node %q", host, got, want[i])
		}
		if !strings.EqualFold(got, raw) {
			decoded++
		}
	}
	if decoded < 20 {
		t.Errorf("only %d hosts decoded; the corpus no longer exercises the decoder", decoded)
	}
}

// A fixed sample of Node 24's answers, so the conversion is checked where
// Node is not installed.
func TestFileURLToPathNodeSamples(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		windows bool
		want    string
	}{
		{"file://xn--mnich-kva/share/zsh.exe", true, `path:\\münich\share\zsh.exe`},
		{"file://SERVER/Share/x.exe", true, `path:\\server\Share\x.exe`},
		{"file://xn--a.b/x", true, `path:\\xn--a.b\x`},
		{"file://xn--/x", true, `path:\\xn--\x`},
		{"file://XN--LS8H/x", true, `path:\\💩\x`},
		{"file://xn---wit2j/x", true, `path:\\岙崊\x`},
		{"file://xn--a.b/x", false, "throw:ERR_INVALID_FILE_URL_HOST"},
		{"file://0x7f.1/share/x", true, `path:\\127.0.0.1\share\x`},
		{"file://localhost/C:/x/y", true, `path:C:\x\y`},
		{"file:///C|/x", true, `path:C:\x`},
		{"file:///C:/a/../b", true, `path:C:\b`},
		{"file://server/share/%2e%2E/x", true, `path:\\server\x`},
		{"file:///C:/Program%20Files/x", true, `path:C:\Program Files\x`},
		{"file://server", true, `path:\\server\`},
		{"file://server/share/a%2Fb", true, "throw:ERR_INVALID_FILE_URL_PATH"},
		{"file:///x/y", true, "throw:ERR_INVALID_FILE_URL_PATH"},
		{"file://server:8080/x", true, "throw:ERR_INVALID_URL"},
		{"file:///C:/100%", true, "throw:URIError"},
		{"http://server/x", true, "throw:ERR_INVALID_URL_SCHEME"},
		{"file:///x/y", false, "path:/x/y"},
		{"file://server/x", false, "throw:ERR_INVALID_FILE_URL_HOST"},
		{"file:///C:/a%2fb", false, "throw:ERR_INVALID_FILE_URL_PATH"},
		{"file://", false, "path:/"},
	} {
		if got := outcome(FileURLToPath(tc.raw, tc.windows)); got != tc.want {
			t.Errorf("FileURLToPath(%q, windows=%t) = %s, want %s", tc.raw, tc.windows, got, tc.want)
		}
	}
}

// Node's String(e) of an error fileURLToPath throws: the name, the code in brackets and the message, and no code for decodeURIComponent's URIError.
func TestErrorThrownIsNodesStringOfTheError(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		windows bool
		want    string
	}{
		{"file:///a.txt", true, "TypeError [ERR_INVALID_FILE_URL_PATH]: File URL path must be absolute"},
		{"file:///a%2Fb", false, "TypeError [ERR_INVALID_FILE_URL_PATH]: File URL path must not include encoded / characters"},
		{"file://host/a", false, `TypeError [ERR_INVALID_FILE_URL_HOST]: File URL host must be "localhost" or empty on ` + nodePlatform()},
		{"file:///%E0%A4%A", false, "URIError: URI malformed"},
	} {
		_, err := FileURLToPath(tc.raw, tc.windows)
		thrown, ok := errors.AsType[*Error](err)
		if !ok || thrown.Thrown() != tc.want {
			t.Errorf("FileURLToPath(%q, %v) = %v; Thrown() want %q", tc.raw, tc.windows, err, tc.want)
		}
	}
}
