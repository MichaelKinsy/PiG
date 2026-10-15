package frontmatter

import "testing"

// frontmatter.ts:40 stripFrontmatter(content) = parseFrontmatter(content).body: the trimmed body after a closed header, the whole normalized input without one, and the YAML error for a malformed header (parse throws).
func TestStripReturnsTheBodyLikePisStripFrontmatter(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"header":         {"---\nname: x\n---\n\n  body text \n", "body text"},
		"no header":      {"plain\r\ntext", "plain\ntext"},
		"unclosed":       {"---\nname: x\nbody", "---\nname: x\nbody"},
		"bom and header": {"\ufeff---\na: 1\n---\nok", "ok"},
	} {
		got, err := Strip(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("%s: Strip = %q, %v; want %q", name, got, err, tc.want)
		}
	}
	if _, err := Strip("---\na: [unclosed\n---\nbody"); err == nil {
		t.Error("a malformed header must be an error, as Pi's yaml parse throws")
	}
}
