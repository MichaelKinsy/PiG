package harness

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// section(key, render, {}) leaves tag absent, so the section stays tagged; only an explicit false unwraps it (define.ts:20-26).
func TestSectionOptionsWithoutTagStayTagged(t *testing.T) {
	if section := Section("cwd", text("/repo"), SectionOptions{}); section.Tag != nil {
		t.Fatalf("empty options set tag %v", *section.Tag)
	}
	if section := Section("cwd", text("/repo")); section.Tag != nil {
		t.Fatalf("no options set tag %v", *section.Tag)
	}
	if section := Section("preamble", text("p"), SectionOptions{Tag: new(false)}); section.Tag == nil || *section.Tag {
		t.Fatalf("tag false not kept: %v", section.Tag)
	}
	if section := Section("cwd", text("/repo"), SectionOptions{Tag: new(true)}); section.Tag == nil || !*section.Tag {
		t.Fatalf("tag true not kept: %v", section.Tag)
	}
	desired, err := RenderSections(testContext, []*durable.PromptSection{Section("cwd", text("/repo"), SectionOptions{})}, durable.PromptInput{}, NewOrderedMap[string](), func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := desired.Get("cwd"); value != "<cwd>\n/repo\n</cwd>" {
		t.Fatalf("rendered %q", value)
	}
}
