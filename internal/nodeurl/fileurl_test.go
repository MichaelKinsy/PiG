package nodeurl

import (
	"bytes"
	"encoding/json"
	"errors"
	"os/exec"
	"testing"
)

// fileURLCases covers hosts (IDNA, case, percent-encoding, IPv4 shorthand,
// IPv6, localhost, invalid), drive letters (including C| and a drive where
// the host goes), dot segments (plain and encoded), encoded separators,
// escapes, a query and fragment, whitespace, backslashes and other schemes.
var fileURLCases = []string{
	"file://xn--mnich-kva/share/zsh.exe", "file://münich/share/zsh.exe", "file://m%C3%BCnich/x",
	"file://SERVER/Share/x.exe", "file://a%41b/x", "file://ab--c/x", "file://xn--a.b/x",
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
