package hljs

import (
	"slices"
	"strings"
	"testing"
)

// eagerRegistry is the registry as LoadAllLanguages left it before languages were decoded on first use: every grammar of the full snapshot decoded and registered at once. It is the reference the deferred path must match.
func eagerRegistry() *Registry {
	r := NewRegistry()
	r.allLoaded, r.indexed = true, true
	r.registerSnapshot(decodeSnapshot(allGrammars, snapshotGrammars))
	return r
}

// loadedRegistry is a registry after LoadAllLanguages, which has decoded no grammar of the full snapshot.
func loadedRegistry() *Registry {
	r := NewRegistry()
	r.LoadAllLanguages()
	return r
}

func languageName(l *Mode) string {
	if l == nil {
		return "<none>"
	}
	return l.lang().name
}

// deferredSamples is one code sample for every language: the corpus sample of that language when it has one, else a generic program.
func deferredSamples(t *testing.T, names []string) map[string]string {
	t.Helper()
	samples := map[string]string{}
	for _, c := range resumeCorpus(t) {
		if _, ok := samples[c.Lang]; !ok {
			samples[c.Lang] = c.Code
		}
	}
	for _, name := range names {
		if _, ok := samples[name]; !ok {
			samples[name] = "function f(x) {\n  // note\n  return x + 1; /* c */ \"s\" 12 <a href='#'>t</a>\n}\n"
		}
	}
	return samples
}

// A language of the full snapshot is found by name and by alias, with the registration order and aliases of the eager load, whichever language is looked up first.
func TestDeferredLookupsMatchEagerLoad(t *testing.T) {
	reference := eagerRegistry()
	lazy := loadedRegistry()
	if got, want := lazy.Languages(), reference.Languages(); !slices.Equal(got, want) {
		t.Fatalf("languages\n got %q\nwant %q", got, want)
	}
	keys := map[string]bool{"": true, "nope": true, "JS": true, "Rust": true, "HTML": true}
	for name := range reference.languages {
		keys[name] = true
		keys[strings.ToUpper(name)] = true
	}
	for alias := range reference.aliases {
		keys[alias] = true
	}
	if len(keys) < 400 {
		t.Fatalf("only %d lookup keys: the check proves nothing", len(keys))
	}
	for _, key := range slices.Sorted(func(yield func(string) bool) {
		for key := range keys {
			if !yield(key) {
				return
			}
		}
	}) {
		if got, want := languageName(lazy.getLanguage(key)), languageName(reference.getLanguage(key)); got != want {
			t.Errorf("getLanguage(%q) = %s, eager load %s", key, got, want)
		}
	}
}

// The eager languages' names and aliases resolve to themselves in the full set too, so a lookup of one needs no index of the full snapshot.
func TestDeferredLookupsMatchEagerLoadBeforeTheIndex(t *testing.T) {
	reference := eagerRegistry()
	r := loadedRegistry()
	checked := 0
	for key := range r.languages {
		if got, want := languageName(r.getLanguage(key)), languageName(reference.getLanguage(key)); got != want {
			t.Errorf("getLanguage(%q) = %s, eager load %s", key, got, want)
		}
		checked++
	}
	for key := range r.aliases {
		if got, want := languageName(r.getLanguage(key)), languageName(reference.getLanguage(key)); got != want {
			t.Errorf("getLanguage(%q) = %s, eager load %s", key, got, want)
		}
		checked++
	}
	if checked < 40 {
		t.Fatalf("only %d lookups: the check proves nothing", checked)
	}
	if r.indexed {
		t.Fatal("looking up an eager language indexed the full snapshot")
	}
}

// Nothing of the full snapshot is decoded until a language outside the eager set is looked up, and then only that language.
func TestDeferredDecodesOnFirstUse(t *testing.T) {
	r := loadedRegistry()
	if r.indexed || r.deferred != nil {
		t.Fatal("LoadAllLanguages indexed the full snapshot")
	}
	eagerNodes := len(r.nodes)
	if _, err := r.Highlight("let x = 1;", "javascript", true); err != nil {
		t.Fatal(err)
	}
	if r.indexed {
		t.Fatal("highlighting an eager language indexed the full snapshot")
	}
	if !r.SupportsLanguage("yml") {
		t.Fatal("the yaml alias yml is not supported")
	}
	if !r.indexed || r.deferred == nil {
		t.Fatal("an alias of the full set did not index it")
	}
	decoded := 0
	for _, node := range r.nodes[eagerNodes:] {
		if node != nil {
			decoded++
		}
	}
	if decoded == 0 || decoded > 100 || decoded > len(r.nodes)-eagerNodes {
		t.Fatalf("looking up one language decoded %d of %d nodes", decoded, len(r.nodes)-eagerNodes)
	}
	if _, ok := r.deferred.roots["yaml"]; ok || r.languages["yaml"] == nil {
		t.Fatal("yaml was not registered by its lookup")
	}
	if _, ok := r.deferred.roots["sql"]; !ok || r.languages["sql"] != nil {
		t.Fatal("looking up yaml decoded sql")
	}
	html, err := r.Highlight("key: true", "yaml", true)
	if err != nil || !strings.Contains(html, "hljs-attr") {
		t.Fatalf("yaml highlight %q, %v", html, err)
	}
}

// Every language highlights the same through the deferred path as through the eager load, whatever order the languages are first used in, and an auto-detection over every language picks the same one.
func TestDeferredHighlightMatchesEagerLoad(t *testing.T) {
	reference := eagerRegistry()
	names := reference.Languages()
	samples := deferredSamples(t, names)
	for _, name := range names {
		samples[name+" (generic)"] = "function f(x) {\n  // note\n  return x + 1; /* c */ \"s\" 12 <a href='#'>t</a>\n}\n"
	}
	forward := loadedRegistry()
	backward := loadedRegistry()
	check := func(r *Registry, label, name, sample, want string) {
		t.Helper()
		got, err := r.Highlight(sample, name, true)
		if err != nil {
			got = "THROW:" + err.Error()
		}
		if got != want {
			t.Errorf("%s: %s highlighted differently from the eager load", label, name)
		}
	}
	want := map[string]string{}
	for _, name := range names {
		got, err := reference.Highlight(samples[name], name, true)
		if err != nil {
			got = "THROW:" + err.Error()
		}
		want[name] = got
	}
	for _, name := range names {
		check(forward, "forward", name, samples[name], want[name])
	}
	for _, name := range slices.Backward(names) {
		check(backward, "backward", name, samples[name], want[name])
	}
	corpus := resumeCorpus(t)
	checked := 0
	for i, c := range corpus {
		if !reference.SupportsLanguage(c.Lang) {
			continue
		}
		wantHTML, wantErr := reference.Highlight(c.Code, c.Lang, true)
		for _, r := range []*Registry{forward, backward} {
			gotHTML, gotErr := r.Highlight(c.Code, c.Lang, true)
			if gotHTML != wantHTML || (gotErr == nil) != (wantErr == nil) {
				t.Errorf("corpus %d %s (%s) highlighted differently from the eager load", i, c.ID, c.Lang)
			}
		}
		checked++
	}
	if checked < 400 {
		t.Fatalf("%d corpus samples checked: the check proves nothing", checked)
	}
}

func TestDeferredAutoDetectMatchesEagerLoad(t *testing.T) {
	reference := eagerRegistry()
	lazy := loadedRegistry()
	corpus := resumeCorpus(t)
	subset := []string{"yaml", "sql", "xml", "markdown"}
	partial := loadedRegistry()
	for i, c := range corpus {
		if i%10 != 0 {
			continue
		}
		got, err := partial.HighlightAuto(c.Code, subset)
		want, wantErr := reference.HighlightAuto(c.Code, subset)
		if got != want || (err == nil) != (wantErr == nil) {
			t.Errorf("auto-detection among %q of %s differs from the eager load", subset, c.ID)
		}
	}
	if partial.deferred == nil || len(partial.deferred.roots) < len(reference.order)-40 {
		t.Fatal("auto-detection among a few languages decoded most of the full set")
	}
	checked := 0
	// Every sample takes about a minute of eager and lazy auto-detection; -short keeps one in ten.
	stride := 1
	if testing.Short() {
		stride = 10
	}
	for i, c := range corpus {
		if i%stride != 0 {
			continue
		}
		got, err := lazy.HighlightAuto(c.Code, nil)
		want, wantErr := reference.HighlightAuto(c.Code, nil)
		if got != want || (err == nil) != (wantErr == nil) {
			t.Errorf("auto-detection of %s differs from the eager load", c.ID)
		}
		checked++
	}
	if want := (len(corpus) + stride - 1) / stride; checked != want || want < 65 {
		t.Fatalf("%d of %d corpus samples auto-detected: the check proves nothing", checked, len(corpus))
	}
	if lazy.deferred != nil {
		t.Fatal("auto-detection over every language left languages undecoded")
	}
	for _, name := range reference.Languages() {
		if lazy.languages[name] == nil {
			t.Fatalf("%s is not registered after auto-detection", name)
		}
	}
}
