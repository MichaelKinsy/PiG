package tui

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// D645, the file extension row. Pi's getLanguageFromPath looks the lowercased extension up in the plain object extToLang
// (packages/coding-agent/src/modes/interactive/theme/theme.ts:1062), so for the extensions constructor and __proto__ it returns a member inherited from
// Object.prototype (the Object function, Object.prototype itself), and highlightCode throws a TypeError for that value. PiG's table is a Go map: such a path has no
// language, like any other unknown extension. Every other member name of Object.prototype holds an upper-case letter, which the lowercased extension never
// equals. Each row holds PiG's language and whether Pi finds an inherited member, which the pinned Pi must do in this run.
var prototypeNamedPaths = []struct {
	path       string
	lang       string
	piInherits bool
}{
	{"notes.constructor", "", true},
	{"x.__proto__", "", true},
	{"a/b/c.constructor", "", true},
	{"Constructor.CONSTRUCTOR", "", true},
	{"NOTES.__PROTO__", "", true},
	{".constructor", "", true},
	{"constructor", "", true},
	{"__proto__", "", true},
	{"a.ts.constructor", "", true},
	{"a.constructor.ts", "typescript", false},
	{"constructor.go", "go", false},
	{"dir.constructor/file", "", false},
	{"a/b/constructor", "", false},
	{"notes.constructor ", "", false},
	{"notes.constructors", "", false},
	{"notes.proto", "protobuf", false},
	{"notes.toString", "", false},
	{"notes.tostring", "", false},
	{"notes.valueOf", "", false},
	{"notes.hasOwnProperty", "", false},
	{"notes.isPrototypeOf", "", false},
	{"notes.propertyIsEnumerable", "", false},
	{"notes.toLocaleString", "", false},
	{"notes.__defineGetter__", "", false},
	{"notes.__defineSetter__", "", false},
	{"notes.__lookupGetter__", "", false},
	{"notes.__lookupSetter__", "", false},
	{"notes.prototype", "", false},
	{"a.unknownext", "", false},
	{"a.ts", "typescript", false},
	{"", "", false},
}

// LanguageFromPath against pinned Pi over the rows above and over every member name of Object.prototype as written, lowercased, uppercased and capitalized, in each
// place of a path: where Pi returns a language, PiG returns the same one; where Pi finds an inherited member and its highlighter throws, PiG has no language.
func TestAPathNamedLikeAnObjectPrototypeMemberHasNoLanguage(t *testing.T) {
	recorded := map[string]bool{}
	var paths []string
	for _, tc := range prototypeNamedPaths {
		paths = append(paths, tc.path)
		recorded[tc.path] = tc.piInherits
		if got := LanguageFromPath(tc.path); got != tc.lang {
			t.Errorf("LanguageFromPath(%q) = %q, want %q", tc.path, got, tc.lang)
		}
	}
	for _, member := range []string{"constructor", "__proto__", "__defineGetter__", "__defineSetter__", "__lookupGetter__", "__lookupSetter__", "hasOwnProperty", "isPrototypeOf", "propertyIsEnumerable", "toLocaleString", "toString", "valueOf"} {
		for _, name := range []string{member, strings.ToLower(member), strings.ToUpper(member), strings.ToUpper(member[:1]) + member[1:]} {
			paths = append(paths, name, "file."+name, "."+name, "a/b/"+name, "a/b/c."+name, "dir."+name+"/file", name+".ts", "a."+name+".go", "x."+name+" ", "x. "+name, "x.."+name, name+".")
		}
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)
	input, err := json.Marshal(paths)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/language_from_path_throws.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []struct {
		Language  string `json:"language"`
		Inherited bool   `json:"inherited"`
		Throws    string `json:"throws"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(paths) {
		t.Fatalf("Pi answered %d paths of %d", len(expected), len(paths))
	}
	thrown := 0
	for i, path := range paths {
		pi := expected[i]
		if pi.Inherited != (pi.Throws != "") {
			t.Errorf("Pi: getLanguageFromPath(%q) inherited = %v, but highlightCode throws %q", path, pi.Inherited, pi.Throws)
		}
		if want, ok := recorded[path]; ok && want != pi.Inherited {
			t.Errorf("Pi: getLanguageFromPath(%q) finds an inherited member = %v, the record says %v", path, pi.Inherited, want)
		}
		if got := LanguageFromPath(path); got != pi.Language {
			t.Errorf("LanguageFromPath(%q) = %q, Pi %q", path, got, pi.Language)
		}
		if pi.Throws != "" {
			thrown++
			if !strings.Contains(pi.Throws, "TypeError") {
				t.Errorf("Pi: highlightCode for %q throws %q, not a TypeError", path, pi.Throws)
			}
		}
	}
	if thrown == 0 {
		t.Fatalf("Pi threw for none of %d paths", len(paths))
	}
}

func BenchmarkLanguageFromPath(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		for _, path := range [...]string{"a.ts", "notes.constructor", "x.__proto__", "a.unknownext"} {
			LanguageFromPath(path)
		}
	}
}
