package frontmatter

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseAgentFrontmatter(t *testing.T) {
	src := `---
name: worker
description: Implements tasks from todos
tools: read,bash,write,edit
model: claude-sonnet-4.6
auto-exit: true
---

# Worker Agent

Body text here.
`
	d := Parse(src)
	if d.String("name") != "worker" {
		t.Errorf("name: %q", d.String("name"))
	}
	if d.String("description") != "Implements tasks from todos" {
		t.Errorf("description: %q", d.String("description"))
	}
	want := []string{"read", "bash", "write", "edit"}
	if got := d.StringList("tools"); !reflect.DeepEqual(got, want) {
		t.Errorf("tools: got %v want %v", got, want)
	}
	if !d.Bool("auto-exit") {
		t.Errorf("auto-exit should be true")
	}
	if !strings.Contains(d.Body, "# Worker Agent") {
		t.Errorf("body should contain heading, got %q", d.Body)
	}
}

func TestParseFlowList(t *testing.T) {
	src := `---
tags: [alpha, "beta gamma", delta]
---
body
`
	d := Parse(src)
	want := []string{"alpha", "beta gamma", "delta"}
	if got := d.StringList("tags"); !reflect.DeepEqual(got, want) {
		t.Errorf("tags: got %v want %v", got, want)
	}
}

func TestParseUsesYAMLScalars(t *testing.T) {
	src := `---
description: Use when reading, writing, or editing PDFs
folded: >
  First line
  second line
---
body
`
	d := Parse(src)
	if d.Err != nil {
		t.Fatal(d.Err)
	}
	if got := d.String("description"); got != "Use when reading, writing, or editing PDFs" {
		t.Fatalf("description = %q", got)
	}
	if got := d.String("folded"); got != "First line second line" {
		t.Fatalf("folded = %q", got)
	}
	if d.Body != "body" {
		t.Fatalf("body = %q", d.Body)
	}
}

func TestParseEmptyAndScalarYAMLFrontmatter(t *testing.T) {
	for name, input := range map[string]string{
		"empty":  "---\n---\nbody",
		"scalar": "---\njust text\n---\nbody",
	} {
		t.Run(name, func(t *testing.T) {
			d := Parse(input)
			if d.Err != nil || len(d.Frontmatter) != 0 || d.Body != "body" {
				t.Fatalf("Parse() = %+v", d)
			}
		})
	}
}

func TestParseReportsMalformedYAML(t *testing.T) {
	d := Parse("---\ndescription: [unterminated\n---\nbody")
	if d.Err == nil {
		t.Fatal("malformed YAML was accepted")
	}
}

func TestNoFrontmatter(t *testing.T) {
	src := "# Just markdown\n\nNo header.\n"
	d := Parse(src)
	if len(d.Frontmatter) != 0 {
		t.Errorf("expected empty fm, got %v", d.Frontmatter)
	}
	if d.Body != src {
		t.Errorf("body should pass through, got %q", d.Body)
	}
}

func TestUnclosedFrontmatter(t *testing.T) {
	src := "---\nname: x\n\nno close\n"
	d := Parse(src)
	if len(d.Frontmatter) != 0 {
		t.Errorf("unclosed fm should be ignored, got %v", d.Frontmatter)
	}
}
