package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/host"
)

func TestLocaleCompareOrdersAsICUDoes(t *testing.T) {
	names := []string{"web", "chat-mode", "chat", "Chat", "10x", "9x", "_a", "a.b", "a-b"}
	slices.SortFunc(names, localeCompare)
	// ICU: punctuation < digits < letters; digits compare one by one, so "10x" sorts before "9x".
	want := []string{"_a", "10x", "9x", "a-b", "a.b", "chat", "Chat", "chat-mode", "web"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("sorted = %v, want %v", names, want)
	}
}

// Keyed registries compose through a real host: contributions from facets by plugin name, a duplicate key reported with
// the first plugin kept, and the composer expanders (composer.ts) applying in key order.
func TestKeyedRegistryComposesAndComposerExpandsInKeyOrder(t *testing.T) {
	var conflicts []host.HostError
	h, err := host.CreateHost(host.HostOptions{OnError: func(err host.HostError) { conflicts = append(conflicts, err) }})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(context.Background())
	upper := func(prefix string) ComposerExpander {
		return func(_ host.Host, _ string, _ int, text string) (*string, error) {
			expanded := prefix + text
			return &expanded, nil
		}
	}
	var unchanged ComposerExpander = func(_ host.Host, _ string, _ int, _ string) (*string, error) { return nil, nil }
	serve := host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error { return ComposerExpanders.Serve(facet) })
	contribute := func(entries ...Keyed[ComposerExpander]) host.Facet {
		return host.DefineFacet(func(_ host.FacetHost, self host.FacetSelf) error {
			return ComposerExpanders.Contribute(self, entries[0]...)
		})
	}
	report, err := h.Load([]host.HostFacet{
		{Name: "a-core", Facet: serve},
		{Name: "b-first", Facet: contribute(Keyed[ComposerExpander]{Entry("z", upper("Z:")), Entry("a", unchanged)})},
		{Name: "c-second", Facet: contribute(Keyed[ComposerExpander]{Entry("z", upper("DUP:")), Entry("b", upper("B:"))})},
	}, context.Background())
	if err != nil || len(report.Failed) != 0 {
		t.Fatalf("load: %v %v", err, report.Failed)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got, err := ExpandComposerText(ctx, h, "s", 1, "hi")
	if err != nil {
		t.Fatal(err)
	}
	// Keys in order: a (unchanged), b (changes): b wins over z.
	if got != "B:hi" {
		t.Fatalf("expanded = %q, want B:hi", got)
	}
	source, err := h.Registry(ctx, ComposerExpanders.Registry, host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	table, _ := ComposerExpanders.Table(source)
	if keys := table.Keys(); !reflect.DeepEqual(keys, []string{"z", "a", "b"}) {
		t.Fatalf("keys = %v", keys)
	}
	z, _ := table.Get("z")
	if text, _ := z(h, "s", 1, "x"); *text != "Z:x" {
		t.Fatalf("a duplicate key must keep the first plugin by name: %q", *text)
	}
}

func TestParseDevCommand(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want DevCommand
		bad  bool
	}{
		{in: map[string]any{"action": "restart"}, want: DevCommand{Action: "restart"}},
		{in: map[string]any{"action": "reload"}, want: DevCommand{Action: "reload"}},
		{in: map[string]any{"action": "reload", "plugins": []any{"chat", "chat", "web-shell"}}, want: DevCommand{Action: "reload", Plugins: []string{"chat", "web-shell"}}},
		{in: map[string]any{"action": "reload", "plugins": []any{"Bad"}}, bad: true},
		{in: map[string]any{"action": "other"}, bad: true},
		{in: "x", bad: true},
	} {
		got, err := ParseDevCommand(tc.in)
		if (err != nil) != tc.bad || (!tc.bad && !reflect.DeepEqual(got, tc.want)) {
			t.Errorf("ParseDevCommand(%v) = %v, %v", tc.in, got, err)
		}
	}
	hash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	modules, ok := ParseDevModules(map[string]any{"type": "dev-modules", "modules": []any{map[string]any{"file": "a.ts", "hash": hash}}})
	if !ok || len(modules) != 1 || modules[0].File != "a.ts" {
		t.Errorf("modules = %v, %v", modules, ok)
	}
	if _, ok := ParseDevModules(map[string]any{"type": "dev-modules", "modules": []any{map[string]any{"file": "a.ts", "hash": "short"}}}); ok {
		t.Error("a short hash must be refused")
	}
}

func TestStripJSONCommentsKeepsStrings(t *testing.T) {
	in := "{\n// a comment\n\"url\": \"http://x//y\", // tail\n\"a\": [1, 2,],\n}"
	want := "{\n\n\"url\": \"http://x//y\", \n\"a\": [1, 2]\n}"
	if got := StripJSONComments(in); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEnvDefaultModelNeedsBoth(t *testing.T) {
	env := func(values map[string]string) func(string) (string, bool) {
		return func(name string) (string, bool) { v, ok := values[name]; return v, ok }
	}
	if _, err := EnvDefaultModel(env(map[string]string{"P2_PROVIDER": "faux"})); err == nil {
		t.Error("a provider without a model must be refused")
	}
	model, err := EnvDefaultModel(env(map[string]string{"PI_MOBILE_PROVIDER": "faux", "P2_MODEL": "faux-1"}))
	if err != nil || model == nil || *model != (EnvModel{Provider: "faux", ModelID: "faux-1"}) {
		t.Errorf("model = %v, %v", model, err)
	}
	if model, err := EnvDefaultModel(env(nil)); model != nil || err != nil {
		t.Errorf("none = %v, %v", model, err)
	}
}

func TestCookieValueAndFromOrigin(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Cookie", "other=1; p2_owner_8788=value")
	if value, ok := CookieValue(request, "p2_owner_8788"); !ok || value != "value" {
		t.Errorf("cookie = %q, %v", value, ok)
	}
	if _, ok := CookieValue(request, "absent"); ok {
		t.Error("absent cookie found")
	}
	request.Header.Set("Origin", "http://127.0.0.1:1")
	if !FromOrigin(request, "http://127.0.0.1:1") || FromOrigin(request, "http://127.0.0.1:2") {
		t.Error("FromOrigin")
	}
}

func TestTakeProcessLockRefusesASecondHolder(t *testing.T) {
	name := "p2:test:" + t.Name() + time.Now().Format("150405.000")
	release, err := TakeProcessLock(name, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TakeProcessLock(name, 50*time.Millisecond); err == nil {
		t.Fatal("a second holder must be refused")
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	again, err := TakeProcessLock(name, 2*time.Second)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	_ = again()
}
