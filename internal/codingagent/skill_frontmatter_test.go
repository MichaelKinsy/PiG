package codingagent

import "testing"

// core/skills.ts:67-72 SkillFrontmatter: name, description and disable-model-invocation are typed; every other key stays available.
func TestSkillFrontmatterKeepsTheTypedFieldsAndUnknownKeys(t *testing.T) {
	front, body, err := ParseSkillFrontmatter("---\nname: reviewer\ndescription: Reviews code\ndisable-model-invocation: true\nlicense: MIT\n---\nBody text\n")
	if err != nil {
		t.Fatal(err)
	}
	if front.Name() != "reviewer" || front.Description() != "Reviews code" || !front.DisableModelInvocation() {
		t.Fatalf("typed fields = %q %q %v", front.Name(), front.Description(), front.DisableModelInvocation())
	}
	if front["license"] != "MIT" {
		t.Fatalf("unknown key lost: %v", front)
	}
	if body != "Body text" {
		t.Fatalf("body = %q", body)
	}
}

func TestSkillFrontmatterOfADocumentWithoutAHeaderIsEmpty(t *testing.T) {
	front, body, err := ParseSkillFrontmatter("just text")
	if err != nil || len(front) != 0 || front.Name() != "" || front.DisableModelInvocation() || body != "just text" {
		t.Fatalf("got %v %q %v", front, body, err)
	}
}

func TestSkillFrontmatterReportsMalformedYAML(t *testing.T) {
	if _, _, err := ParseSkillFrontmatter("---\nname: [unclosed\n---\nbody"); err == nil {
		t.Fatal("malformed frontmatter must fail")
	}
}
