package hljs

import (
	"reflect"
	"testing"
)

// The rarer mode properties sit behind a pointer only the modes that set one carry. inherit must copy them into the new mode's own properties, a mode without them must read them as absent, and no write may reach the shared zero values.
func TestRareModePropertiesStayWithTheirMode(t *testing.T) {
	r := eagerRegistry()

	modes, withRare := 0, 0
	for _, node := range r.nodes {
		mode, ok := node.(*Mode)
		if !ok {
			continue
		}
		modes++
		if (mode.rarer != nil) != (mode.present&rareKeys != 0) {
			t.Fatalf("a mode with keys %b carries rare properties %v", mode.present, mode.rarer != nil)
		}
		if mode.rarer != nil {
			withRare++
		}
	}
	if withRare == 0 || withRare*2 > modes {
		t.Fatalf("%d of %d modes carry rare properties; most modes set none", withRare, modes)
	}

	source := &Mode{present: keyKeywords | keyIllegal | keySubLanguage}
	source.setRare().keywords = keywordsValue{kind: keywordsKindString, text: "if else"}
	source.setRare().illegal = stringPattern(`\$`)
	source.setRare().subLanguage = subLanguage{set: true, name: "xml"}
	copied := inherit(source)
	if copied.rarer == source.rarer {
		t.Fatal("inherit shares the rare properties of its source")
	}
	if copied.rare().keywords.text != "if else" || copied.rare().illegal.src != `\$` || copied.rare().subLanguage.name != "xml" {
		t.Fatalf("inherit copied keywords %q illegal %q subLanguage %q", copied.rare().keywords.text, copied.rare().illegal.src, copied.rare().subLanguage.name)
	}
	copied.setRare().keywords.text = "changed"
	if source.rare().keywords.text != "if else" {
		t.Fatalf("changing an inherited copy changed its source to %q", source.rare().keywords.text)
	}
	deleteKey(copied, keyIllegal)
	if copied.rare().illegal.truthy() || copied.present&keyIllegal != 0 || !source.rare().illegal.truthy() {
		t.Fatal("deleting illegal from a copy kept it, or reached the source")
	}

	plain := &Mode{present: keyBegin}
	plainCopy := inherit(plain)
	deleteKey(plainCopy, keyKeywords)
	for _, name := range []string{"go", "python", "xml", "bash", "markdown", "php"} {
		if _, err := r.Highlight("x := `a` + \"b\" // c\n<a>$y</a>\n<?php echo 1; ?>\n", name, true); err != nil {
			t.Fatalf("highlight %s: %v", name, err)
		}
	}
	if !reflect.ValueOf(noRareProps).IsZero() {
		t.Fatal("a write reached the shared zero rare properties")
	}
	if plain.rare().keywords.truthy() || plain.rarer != nil || plainCopy.rarer != nil {
		t.Fatal("reading, inheriting or deleting a property of a mode without rare properties added them")
	}
}
