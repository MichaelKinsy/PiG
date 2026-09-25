package packagecontent

import (
	"path/filepath"
	"reflect"
	"testing"
)

// Cases follow upstream pi-manifest.ts readPiManifest: null for unreadable,
// unparsable, or non-object package and "pi" values; per-field string arrays.
func TestReadPiManifest(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want *PiManifest
	}{
		{name: "invalid JSON", body: `{"pi":`, want: nil},
		{name: "array package", body: `[{"pi":{}}]`, want: nil},
		{name: "no pi field", body: `{"name":"pkg"}`, want: nil},
		{name: "null pi", body: `{"pi":null}`, want: nil},
		{name: "array pi", body: `{"pi":["./extensions"]}`, want: nil},
		{name: "string pi", body: `{"pi":"./extensions"}`, want: nil},
		{name: "empty pi", body: `{"pi":{}}`, want: &PiManifest{}},
		{
			name: "all resource fields",
			body: `{"pi":{"extensions":["./a.ts"],"skills":["./skills"],"prompts":["./p"],"themes":["./t.json"],"other":["x"]}}`,
			want: &PiManifest{Extensions: []string{"./a.ts"}, Skills: []string{"./skills"}, Prompts: []string{"./p"}, Themes: []string{"./t.json"}},
		},
		{
			name: "malformed field dropped, valid sibling kept",
			body: `{"pi":{"skills":"./skills","prompts":["./p"],"themes":["./t.json",1],"extensions":null}}`,
			want: &PiManifest{Prompts: []string{"./p"}},
		},
		{name: "empty array kept", body: `{"pi":{"extensions":[]}}`, want: &PiManifest{Extensions: []string{}}},
		{name: "non-string name ignored", body: `{"name":5,"pi":{"skills":["./s"]}}`, want: &PiManifest{Skills: []string{"./s"}}},
		{name: "byte-order mark stripped", body: "\ufeff{\"pi\":{\"extensions\":[\"./a.ts\"]}}", want: &PiManifest{Extensions: []string{"./a.ts"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "package.json")
			writeTestFile(t, path, tc.body)
			if got := ReadPiManifest(path); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ReadPiManifest(%s) = %#v, want %#v", tc.body, got, tc.want)
			}
		})
	}
	if got := ReadPiManifest(filepath.Join(t.TempDir(), "missing.json")); got != nil {
		t.Fatalf("missing file = %#v, want nil", got)
	}
}

// Discover reads the package manifest the same way: a BOM or a non-string
// name must not discard the declared resources.
func TestDiscoverReadsPiManifestWithBOMAndNonStringName(t *testing.T) {
	for _, body := range []string{
		"\ufeff{\"name\":\"pkg\",\"pi\":{\"prompts\":[\"./declared\"]}}",
		`{"name":5,"pi":{"prompts":["./declared"]}}`,
	} {
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "package.json"), body)
		writeTestFile(t, filepath.Join(root, "declared", "one.md"), "Declared\n")
		writeTestFile(t, filepath.Join(root, "prompts", "conventional.md"), "Conventional\n")
		resources, err := Discover(root)
		if err != nil {
			t.Fatal(err)
		}
		assertContains(t, resources.PromptFiles, filepath.Join(root, "declared", "one.md"))
		assertNotContains(t, resources.PromptFiles, filepath.Join(root, "prompts", "conventional.md"))
	}
}
