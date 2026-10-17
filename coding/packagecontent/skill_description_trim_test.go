package packagecontent

import (
	"path/filepath"
	"testing"
)

// loadSkillFromFile (core/skills.ts) skips a Markdown file other than SKILL.md whose description is empty after String.prototype.trim: U+FEFF and U+00A0 are
// trimmed, U+0085 is not.
func TestDefinesSkillTrimsTheDescriptionLikeJS(t *testing.T) {
	root := t.TempDir()
	for name, tc := range map[string]struct {
		description string
		want        bool
	}{
		"bom.md":     {"\ufeff", false},
		"nbsp.md":    {"\u00a0 \u00a0", false},
		"spaces.md":  {"   ", false},
		"nel.md":     {"\u0085", true},
		"text.md":    {"x", true},
		"bomtext.md": {"\ufeffx", true},
	} {
		path := filepath.Join(root, name)
		writeTestFile(t, path, "---\ndescription: \""+tc.description+"\"\n---\nbody\n")
		if got := definesSkill(path); got != tc.want {
			t.Errorf("%s: definesSkill = %v, want %v", name, got, tc.want)
		}
	}
}
