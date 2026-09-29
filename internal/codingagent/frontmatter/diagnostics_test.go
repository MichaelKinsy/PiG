package frontmatter

import (
	"testing"
)

// Pi 0.87.1 utils/frontmatter.ts delegates diagnostics to yaml's compact-mapping validator.
func TestCompactMappingDiagnosticMatchesPi(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		{"description: Broken: unquoted colon", "Nested mappings are not allowed in compact mappings at line 1, column 14:\n\ndescription: Broken: unquoted colon\n             ^\n"},
		{"root:\n  description: Broken: value", "Nested mappings are not allowed in compact mappings at line 2, column 16:\n\n  description: Broken: value\n               ^\n"},
		{"\"a: b\": x: y", "Nested mappings are not allowed in compact mappings at line 1, column 9:\n\n\"a: b\": x: y\n        ^\n"},
	} {
		t.Run(tc.source, func(t *testing.T) {
			doc := Parse("---\n" + tc.source + "\n---\nbody")
			if doc.Err == nil || doc.Err.Error() != tc.want {
				t.Fatalf("error = %v; want %q", doc.Err, tc.want)
			}
		})
	}
}
