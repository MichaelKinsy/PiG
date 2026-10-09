package main

import (
	"encoding/json"
	"maps"
	"strings"
	"testing"
)

// T9n: a Pi parameter typed as a class whose Go package would import-cycle is satisfied by a consumer-owned interface that lists exactly
// the Pi members the consumer uses, when the Go class implements it (compile-time assertion in the producer package) and a test passes
// the real producer. Every missing piece leaves the row a gap.
func TestNarrowInterfaceStandsForAClassParameter(t *testing.T) {
	const showID = "pkg:fx/.#show::call:0"
	const iface = `package lib2

// Reader is what Show reads of a Widget.
type Reader interface{ Name() string }

// Show is the Go form of upstream show.
func Show(r Reader) string { return r.Name() }
`
	const assertion = "package lib\n\nimport \"fixture/lib2\"\n\nvar _ lib2.Reader = (*Widget)(nil)\n"
	const passTest = `package lib

import (
	"testing"

	"fixture/lib2"
)

func TestShowReadsAWidget(t *testing.T) {
	var w *Widget = NewWidget("a")
	if lib2.Show(w) != "a" {
		t.Fatal("show")
	}
}
`
	entry := func(mutate func(m)) string {
		e := m{"interface": "lib2#Reader", "members": m{"name": "Name"}, "test": "lib#TestShowReadsAWidget", "reason": "import cycle"}
		if mutate != nil {
			mutate(e)
		}
		b, _ := json.Marshal(m{"fx:Widget": []m{e}})
		return string(b)
	}
	tweak := func(inv []m) []m {
		return append(inv,
			m{"id": "pkg:fx/.#Widget::property:size", "parentId": "pkg:fx/.#Widget", "role": "property", "name": "Widget.size", "kind": "property", "shape": m{"name": "size", "type": "number"}},
			m{"id": "pkg:fx/.#show", "name": "show", "kind": "function", "shape": m{"type": "(w: Widget) => string", "calls": []m{{"parameters": []m{prm("w", "Widget", false)}, "returns": "string"}}}},
			m{"id": showID, "parentId": "pkg:fx/.#show", "role": "call-overload", "name": "show call 0", "kind": "call-overload", "shape": m{"parameters": []m{prm("w", "Widget", false)}, "returns": "string"}})
	}
	base := func(extra map[string]string) map[string]string {
		files := map[string]string{"lib2/lib2.go": iface, "lib/assert.go": assertion, "lib/narrow_test.go": passTest, narrowFile: entry(nil)}
		maps.Copy(files, extra)
		return files
	}
	reach := map[string]bool{"lib2/lib2.go#Show": true}
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  bool // the call row is closed
	}{
		{"verified narrow interface", base(nil), true},
		{"no reviewed entry", base(map[string]string{narrowFile: "{}"}), false},
		{"interface lists an extra method", base(map[string]string{"lib2/lib2.go": strings.Replace(iface, "Name() string }", "Name() string; Extra() }", 1), "lib/extra.go": "package lib\n\nfunc (*Widget) Extra() {}\n"}), false},
		{"interface misses a listed member", base(map[string]string{narrowFile: entry(func(e m) { e["members"] = m{"name": "Name", "size": "Size"} }), "lib/extra.go": "package lib\n\nfunc (*Widget) Size() int { return 0 }\n"}), false},
		{"entry lists a member the Pi class lacks", base(map[string]string{narrowFile: entry(func(e m) { e["members"] = m{"nope": "Name"} })}), false},
		{"the Go class does not implement the interface", base(map[string]string{"lib2/lib2.go": strings.Replace(iface, "Name() string }", "Name() string; Size() int }", 1), narrowFile: entry(func(e m) { e["members"] = m{"name": "Name", "size": "Size"} })}), false},
		{"no compile-time assertion", base(map[string]string{"lib/assert.go": "package lib\n"}), false},
		{"assertion names another type", base(map[string]string{"lib/assert.go": strings.Replace(assertion, "(*Widget)(nil)", "(*Other)(nil)", 1) + "\ntype Other struct{}\n\nfunc (*Other) Name() string { return \"\" }\n"}), false},
		{"the test never names the producer", base(map[string]string{"lib/narrow_test.go": strings.Replace(passTest, "var w *Widget = NewWidget(\"a\")", "var w lib2.Reader", 1)}), false},
		{"the named test does not exist", base(map[string]string{narrowFile: entry(func(e m) { e["test"] = "lib#TestNothing" })}), false},
	} {
		ds := fixtureRun(t, tc.files, tweak, reach, nil)
		if got := !ds[showID].Gap; got != tc.want {
			t.Errorf("%s: call row closed = %v (%s), want %v", tc.name, got, ds[showID].Reason+" "+ds[showID].Detail, tc.want)
		}
	}
}
