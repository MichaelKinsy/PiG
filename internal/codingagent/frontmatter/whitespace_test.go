package frontmatter

import "testing"

// Pi utils/frontmatter.ts:25 applies String.trim: FEFF is whitespace, but NEXT LINE (0085) is not.
func TestFrontmatterBodyUsesJavaScriptWhitespace(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"next line retained", "\u0085body\u0085", "\u0085body\u0085"},
		{"byte order mark trimmed", "\ufeffbody\ufeff", "body"},
		{"ordinary whitespace trimmed", " \t\nbody\r\n ", "body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := Parse("---\nname: test\n---\n" + tc.body)
			if doc.Err != nil || doc.Body != tc.want {
				t.Fatalf("body=%q error=%v; want=%q", doc.Body, doc.Err, tc.want)
			}
		})
	}
}
