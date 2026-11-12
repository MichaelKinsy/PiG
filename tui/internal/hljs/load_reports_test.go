package hljs

import "testing"

// LoadAllLanguages reports whether a lookup made before it can answer differently afterwards, so a screen that drew highlighted code is
// drawn again only then.
func TestLoadAllLanguagesReportsEarlierLookupsThatDiffer(t *testing.T) {
	cases := []struct {
		name   string
		lookup func(r *Registry)
		want   bool
	}{
		{"no lookup", func(*Registry) {}, false},
		{"an eager language", func(r *Registry) { _, _ = r.Highlight("const x = 1", "javascript", false) }, false},
		{"an eager language by alias", func(r *Registry) { r.SupportsLanguage("js") }, false},
		{"a language the load adds", func(r *Registry) { r.SupportsLanguage("ada") }, true},
		{"a language the load adds, highlighted", func(r *Registry) { _, _ = r.Highlight("x", "ada", false) }, true},
		{"a name no language has", func(r *Registry) { r.SupportsLanguage("no-such-language") }, true},
		{"auto-detection over the languages so far", func(r *Registry) { _, _ = r.HighlightAuto("const x = 1", nil) }, true},
		{"auto-detection over named eager languages", func(r *Registry) { _, _ = r.HighlightAuto("const x = 1", []string{"javascript", "json"}) }, false},
		{"the language list", func(r *Registry) { r.Languages() }, true},
	}
	for _, c := range cases {
		r := NewRegistry()
		c.lookup(r)
		if got := r.LoadAllLanguages(); got != c.want {
			t.Errorf("%s: LoadAllLanguages reports %v, want %v", c.name, got, c.want)
		}
		if again := r.LoadAllLanguages(); again != c.want {
			t.Errorf("%s: a second LoadAllLanguages reports %v, want the first's %v", c.name, again, c.want)
		}
		// After the load nothing differs from what it was: a lookup made later is not an earlier one.
		r2 := NewRegistry()
		r2.LoadAllLanguages()
		r2.SupportsLanguage("ada")
		if r2.LoadAllLanguages() {
			t.Errorf("%s: a lookup after the load was reported as one before it", c.name)
		}
	}
}
