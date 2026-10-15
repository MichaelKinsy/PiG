package hljs

import (
	"reflect"
	"testing"
)

// The language and compiled properties sit behind pointers. inherit must copy them into the new mode's own properties, a mode without them must read them as absent, and no write may reach the shared zero values.
func TestModePropertiesStayWithTheirMode(t *testing.T) {
	r := NewRegistry()
	r.LoadAllLanguages()

	modes, withLanguage := 0, 0
	for _, node := range r.nodes {
		mode, ok := node.(*Mode)
		if !ok {
			continue
		}
		modes++
		if mode.language != nil {
			withLanguage++
		}
		if mode.compiled != nil {
			t.Fatal("a loaded mode has compiled properties before any highlight")
		}
	}
	if withLanguage == 0 || withLanguage*10 > modes {
		t.Fatalf("%d of %d modes carry language properties; only languages should", withLanguage, modes)
	}
	for name, language := range r.languages {
		if language.lang().name == "" {
			t.Fatalf("language %q has no name", name)
		}
	}

	golang := r.getLanguage("go")
	wantName, wantAliases := golang.lang().name, golang.lang().aliases
	copied := inherit(golang)
	if copied.language == golang.language {
		t.Fatal("inherit shares the language properties of its source")
	}
	if copied.lang().name != wantName || !reflect.DeepEqual(copied.lang().aliases, wantAliases) {
		t.Fatalf("inherit copied name %q aliases %v, want %q %v", copied.lang().name, copied.lang().aliases, wantName, wantAliases)
	}
	copied.setLang().name = "changed"
	if golang.lang().name != wantName {
		t.Fatalf("renaming an inherited copy renamed the language to %q", golang.lang().name)
	}

	for _, name := range []string{"go", "python", "xml", "bash", "markdown"} {
		if _, err := r.Highlight("x := `a` + \"b\" // c\n<a>$y</a>\n", name, true); err != nil {
			t.Fatalf("highlight %s: %v", name, err)
		}
	}
	compiled := r.getLanguage("go")
	if compiled.comp().matcher == nil {
		t.Fatal("a highlighted language has no matcher")
	}
	variant := inherit(compiled)
	if variant.compiled == compiled.compiled || variant.comp().matcher != compiled.comp().matcher {
		t.Fatal("inherit shares the compiled properties, or loses the matcher")
	}
	variant.setComp().terminatorEnd = "changed"
	if compiled.comp().terminatorEnd == "changed" {
		t.Fatal("a write to an inherited copy reached the compiled language")
	}

	if !reflect.ValueOf(noLanguageProps).IsZero() || !reflect.ValueOf(noCompiledProps).IsZero() {
		t.Fatal("a write reached the shared zero properties")
	}
	plain := &Mode{}
	if plain.lang().name != "" || plain.comp().matcher != nil || plain.language != nil || plain.compiled != nil {
		t.Fatal("reading a plain mode's properties added them")
	}
}
