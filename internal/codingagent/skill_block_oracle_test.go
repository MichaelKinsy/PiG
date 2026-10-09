package codingagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// TestParseSkillBlockMatchesPi runs parseSkillBlock of Pi 1.0.4 and ParseSkillBlock over texts that differ in the block's delimiters, trailing user message and JavaScript whitespace.
func TestParseSkillBlockMatchesPi(t *testing.T) {
	texts := []string{
		"", "plain", `<skill name="a" location="/p">` + "\nbody\n</skill>", `<skill name="a" location="/p">` + "\nbody\n</skill>\n\nhello",
		`<skill name="a" location="/p">` + "\nbody\n</skill>\n\n  spaced  \n", `<skill name="a" location="/p">` + "\nbody\n</skill>\n\n\u00a0\u2003\ufeff",
		`<skill name="a" location="/p">` + "\nbody\n</skill>\n\n\u0085x\u0085", `<skill name="a" location="/p">` + "\nbody\n</skill>\n\n\n", `<skill name="a" location="/p">` + "\nbody\n</skill>\n",
		`<skill name="a" location="/p">` + "\nline1\n</skill>\nline2\n</skill>", `<skill name="a" location="/p">` + "\n\n</skill>", `<skill name="a" location="/p">` + "\n</skill>",
		`<skill name="" location="/p">` + "\nb\n</skill>", `<skill name="a" location="">` + "\nb\n</skill>", `<skill name="a\nb" location="/p q">` + "\nb\n</skill>",
		" " + `<skill name="a" location="/p">` + "\nb\n</skill>", `<skill name="a" location="/p">` + "\r\nb\r\n</skill>", `<skill name="é😀" location="/é">` + "\nb\n</skill>\n\n😀",
		`<skill name="a" location="/p">` + "\nb\n</skill>\n\nfirst\n\nsecond", `<skill name="a"  location="/p">` + "\nb\n</skill>",
	}
	var want []*struct {
		Name        string  `json:"name"`
		Location    string  `json:"location"`
		Content     string  `json:"content"`
		UserMessage *string `json:"userMessage"`
	}
	pioracle.Run(t, `
const mod = await import(new URL("file://" + root + "/pi-coding-agent/core/agent-session.js").href);
emit(input.map((t) => mod.parseSkillBlock(t) ?? null));`, texts, &want)
	for i, text := range texts {
		got := ParseSkillBlock(text)
		w := want[i]
		switch {
		case (got == nil) != (w == nil):
			t.Errorf("%q: parsed = %v, Pi %v", text, got != nil, w != nil)
		case got != nil:
			wantMessage := ""
			if w.UserMessage != nil {
				wantMessage = *w.UserMessage
			}
			if got.Name != w.Name || got.Location != w.Location || got.Content != w.Content || got.UserMessage != wantMessage {
				t.Errorf("%q:\n  Pig %+v\n  Pi  %+v (message %q)", text, *got, *w, wantMessage)
			}
		}
	}
}

// skills.ts:305 (`description.trim() !== ""`) and the skill command's `body.trim()` use JavaScript's trim: U+0085 is not
// whitespace there, U+FEFF is. A root document whose description is only U+0085 is a skill; one with only U+FEFF is not.
func TestSkillFilesTrimWithJavaScriptWhitespace(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	nel := write("nel.md", "---\ndescription: \"\\u0085\"\n---\n\u0085body\u0085\n")
	bom := write("bom.md", "---\ndescription: \"\\uFEFF\"\n---\nbody\n")
	skill, err := loadSkillFile(nel)
	if err != nil || skill == nil {
		t.Fatalf("U+0085 description: skill = %v, err = %v; want a skill", skill, err)
	}
	if skill.Body != "\u0085body\u0085" {
		t.Fatalf("body = %q, want U+0085 kept", skill.Body)
	}
	if skill, err := loadSkillFile(bom); err != nil || skill != nil {
		t.Fatalf("U+FEFF description: skill = %v, err = %v; want none", skill, err)
	}
}
