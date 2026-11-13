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

// prototypeNamedPaths are paths around the member names of Object.prototype, with what pinned Pi answers for each: the language theme.ts getLanguageFromPath
// returns, and whether highlightCode throws for the value it returns. getLanguageFromPath reads a plain object, so the lowercased extensions constructor and
// __proto__ find an inherited member (the Object function, Object.prototype) and highlightCode throws a TypeError for it; every other member name holds an
// upper-case letter, which the lowercased extension never equals. TestLanguageFromPathThrowsLikePi runs these rows against Pi.
var prototypeNamedPaths = []struct {
	path   string
	lang   string
	throws bool
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

func TestLanguageFromPathThrowsForAnInheritedMember(t *testing.T) {
	for _, tc := range prototypeNamedPaths {
		if got := LanguageFromPath(tc.path); got != tc.lang {
			t.Errorf("LanguageFromPath(%q) = %q, want %q", tc.path, got, tc.lang)
		}
		if got := LanguageFromPathThrows(tc.path); got != tc.throws {
			t.Errorf("LanguageFromPathThrows(%q) = %v, want %v", tc.path, got, tc.throws)
		}
	}
}

// LanguageFromPath and LanguageFromPathThrows against pinned Pi: getLanguageFromPath's value and whether highlightCode throws for it, over the rows above and over
// every member name of Object.prototype as written, lowercased, uppercased and capitalized, in each place of a path.
func TestLanguageFromPathThrowsLikePi(t *testing.T) {
	var paths []string
	for _, tc := range prototypeNamedPaths {
		paths = append(paths, tc.path)
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
		want := expected[i]
		if want.Inherited != (want.Throws != "") {
			t.Errorf("Pi: getLanguageFromPath(%q) inherited = %v, but highlightCode throws %q", path, want.Inherited, want.Throws)
		}
		if got := LanguageFromPath(path); got != want.Language {
			t.Errorf("LanguageFromPath(%q) = %q, Pi %q", path, got, want.Language)
		}
		if got := LanguageFromPathThrows(path); got != (want.Throws != "") {
			t.Errorf("LanguageFromPathThrows(%q) = %v, Pi throws %q", path, got, want.Throws)
		}
		if want.Throws != "" {
			thrown++
			if !strings.Contains(want.Throws, "TypeError") {
				t.Errorf("Pi: highlightCode for %q throws %q, not a TypeError", path, want.Throws)
			}
		}
	}
	if thrown == 0 {
		t.Fatalf("Pi threw for none of %d paths", len(paths))
	}
}
