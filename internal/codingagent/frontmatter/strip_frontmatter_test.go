package frontmatter

import "testing"

// Pi test/frontmatter.test.ts "stripFrontmatter" (lines 50-59), plus the throw on malformed YAML that stripFrontmatter inherits from parseFrontmatter.
func TestStripFrontmatterMatchesUpstream(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"removes frontmatter and trims body", "---\nkey: value\n---\n\nBody\n", "Body"},
		{"returns body when no frontmatter present", "\n  No frontmatter body  \n", "\n  No frontmatter body  \n"},
		{"CRLF and BOM", "\ufeff---\r\nkey: value\r\n---\r\nBody\r\n", "Body"},
		{"unclosed block is the whole input", "---\nkey: value\n", "---\nkey: value\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Strip(tc.in)
			if err != nil || got != tc.want {
				t.Fatalf("Strip(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
			}
		})
	}
	if _, err := Strip("---\nkey: [unclosed\n---\nBody"); err == nil {
		t.Fatal("malformed YAML must be an error, as Pi's yaml parse throws")
	}
}
