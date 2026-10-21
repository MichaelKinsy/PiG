package export

// Ports packages/coding-agent/test/export-html-cjk.test.ts (Pi 26293e74, #10154, #11168): the exported page renders emphasis next to
// CJK punctuation and keeps CommonMark flanking rules for other text. The page's scripts run under Node, as Pi's test runs them in a VM.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestExportHTMLRendersCJKEmphasisNextToPunctuation(t *testing.T) {
	sd, err := FromJSONL([]byte(`{"type":"session","version":3,"id":"cjk-test","timestamp":"1970-01-01T00:00:00.000Z","cwd":"/tmp"}`))
	if err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(t.TempDir(), "out.html")
	if err := os.WriteFile(page, []byte(ToHTML(sd)), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := [][2]string{
		{"**这是测试。**后面", "<p><strong>这是测试。</strong>后面</p>\n"},
		{"**一句话说清：**一台机器", "<p><strong>一句话说清：</strong>一台机器</p>\n"},
		{"**要点到此为止。**20 小时", "<p><strong>要点到此为止。</strong>20 小时</p>\n"},
		{"字**「引用」**字", "<p>字<strong>「引用」</strong>字</p>\n"},
		{"*斜体。*后面", "<p><em>斜体。</em>后面</p>\n"},
		// Non-CJK text keeps the CommonMark flanking rules.
		{"**end.**Then next", "<p>**end.**Then next</p>\n"},
	}
	var sources []string
	for _, c := range cases {
		sources = append(sources, c[0])
	}
	input, _ := json.Marshal(sources)
	cmd := exec.CommandContext(t.Context(), "node", "testdata/export_html_cjk.mjs", page)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, &stderr)
	}
	var got []string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	for i, c := range cases {
		if got[i] != c[1] {
			t.Errorf("%q rendered %q, want %q", c[0], got[i], c[1])
		}
	}
}
