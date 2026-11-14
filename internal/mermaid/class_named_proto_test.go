package mermaid

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// classNameDiagrams are diagrams that style a node through a class; NAME stands for the class's name.
var classNameDiagrams = []struct{ name, source string }{
	{"flowchart, class statement", "flowchart LR\n  A[One] --> B[Two]\n  classDef NAME fill:#ff0000,color:#ffffff\n  class A NAME\n"},
	{"flowchart, inline class", "flowchart LR\n  A[One]:::NAME --> B[Two]\n  classDef NAME fill:#ff0000,color:#ffffff\n"},
	{"class diagram", "classDiagram\n  class Animal\n  classDef NAME fill:#ff0000\n  cssClass \"Animal\" NAME\n"},
	{"state diagram", "stateDiagram-v2\n  [*] --> Idle\n  Idle --> Done\n  classDef NAME fill:#00ff00\n  class Done NAME\n"},
	{"style property named __proto__", "flowchart LR\n  A[One] --> B[Two]\n  classDef NAME __proto__:x,fill:#0000ff\n  class B NAME\n"},
}

func defaultAnsi(t *testing.T, source string) []string {
	t.Helper()
	art, ok := Render(source)
	if !ok {
		t.Fatalf("no diagram for %q", source)
	}
	return ToAnsi(art, DefaultTheme)
}

// A class named __proto__ is a class like any other: the nodes that carry it take its fill, exactly as they do when the class has another name.
func TestAClassNamedProtoStylesItsNodes(t *testing.T) {
	for _, diagram := range classNameDiagrams {
		named := defaultAnsi(t, strings.ReplaceAll(diagram.source, "NAME", "__proto__"))
		control := defaultAnsi(t, strings.ReplaceAll(diagram.source, "NAME", "hot"))
		if !strings.Contains(strings.Join(control, "\n"), ";48;2;") {
			t.Fatalf("%s: the control class draws no fill\n%q", diagram.name, control)
		}
		if !slices.Equal(named, control) {
			t.Errorf("%s: a class named __proto__ draws\n%q\nwant what a class named hot draws\n%q", diagram.name, named, control)
		}
	}
}

// The same diagrams against lovely-mermaid, the library Pi draws diagrams with. Its class table is a plain object: assigning the class named __proto__ makes the
// class's properties the table's prototype, and the lookup of that name reads the prototype back, so Pi draws the style too.
func TestAClassNamedProtoIsDrawnAsLovelyMermaidDrawsIt(t *testing.T) {
	var sources []string
	for _, diagram := range classNameDiagrams {
		sources = append(sources, strings.ReplaceAll(diagram.source, "NAME", "__proto__"), strings.ReplaceAll(diagram.source, "NAME", "hot"))
	}
	var pi [][]string
	pioracle.Run(t, `
const { render, toAnsi } = await load("../lovely-mermaid/dist/index.js");
emit(input.map((source) => toAnsi(render(source))));`, sources, &pi)
	if len(pi) != len(sources) {
		t.Fatalf("lovely-mermaid drew %d of %d diagrams", len(pi), len(sources))
	}
	for i, source := range sources {
		if got := defaultAnsi(t, source); !slices.Equal(got, pi[i]) {
			t.Errorf("%q:\n  PiG            %q\n  lovely-mermaid %q", source, got, pi[i])
		}
	}
}
