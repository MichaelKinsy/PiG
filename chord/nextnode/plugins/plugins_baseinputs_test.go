package plugins

// pi: packages/chord/src/node/plugins/base-inputs.ts

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/evanw/esbuild/pkg/api"

	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/build"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

// Ports packages/chord/test/node/plugins/base-inputs.test.ts (p3 step 0a): the hash a record keeps, the one read that
// records a file, and the esbuild plugin loading every file through it with esbuild's own loader for its extension.

// Ports base-inputs.test.ts "hashes bytes with SHA-256, and records a file's bytes as it reads them".
func TestBaseInputsHashBytesWithSHA256AndRecordAFilesBytesAsItReadsThem(t *testing.T) {
	if got := model.ContentHash([]byte("abc")); got != sha256Hex("abc") {
		t.Fatalf("ContentHash = %s", got)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "a.ts")
	if err := os.WriteFile(file, []byte("export const a = 1;"), 0o644); err != nil {
		t.Fatal(err)
	}
	recorder := build.NewRecorder()
	bytes, err := recorder.Read(file)
	if err != nil || string(bytes) != "export const a = 1;" {
		t.Fatalf("Read = %q, %v", bytes, err)
	}
	want := model.BaseInputs{file: model.ContentHash(bytes)}
	if got := recorder.Inputs(); !maps.Equal(got, want) {
		t.Fatalf("inputs = %v, want %v", got, want)
	}
}

// Ports base-inputs.test.ts "loads every file of a build with esbuild's own loader and records each one's bytes".
func TestBaseInputsRecordedSourcesLoadEveryFileWithEsbuildsOwnLoaderAndRecordEachOnesBytes(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"main.ts":      "import data from \"./d.json\";\nimport styles from \"./s.module.css\";\nimport \"./g.css\";\nconsole.log(data.k, styles.x);\n",
		"d.json":       `{ "k": 1 }`,
		"s.module.css": ".x { color: red; }",
		"g.css":        "body { margin: 0; }",
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	built := func(plugins ...api.Plugin) []string {
		result := api.Build(api.BuildOptions{
			EntryPoints: []string{filepath.Join(dir, "main.ts")},
			Bundle:      true, Write: false, Outdir: filepath.Join(dir, "out"), LogLevel: api.LogLevelSilent,
			Plugins: plugins,
		})
		if len(result.Errors) > 0 {
			t.Fatalf("errors: %v", result.Errors)
		}
		var texts []string
		for _, file := range result.OutputFiles {
			texts = append(texts, string(file.Contents))
		}
		return texts
	}
	plain := built()
	recorder := build.NewRecorder()
	if recorded := built(recorder.Plugin()); !slices.Equal(recorded, plain) {
		t.Fatalf("recorded outputs differ from plain ones:\n%q\n%q", recorded, plain)
	}
	// Observe the exact paths esbuild resolves and hands to OnLoad in a separate third build so plain stays
	// plugin-free. On Unix behind a linked temp root esbuild resolves symlinks; on Windows it keeps 8.3 short
	// names (C:\Users\RUNNER~1\...) as given.
	var wantMu sync.Mutex
	wantByName := make(map[string]string, len(files))
	observe := api.Plugin{
		Name: "observe-esbuild-paths",
		Setup: func(pb api.PluginBuild) {
			pb.OnLoad(api.OnLoadOptions{Filter: ".*"}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
				if args.Namespace == "file" {
					wantMu.Lock()
					wantByName[filepath.Base(args.Path)] = args.Path
					wantMu.Unlock()
				}
				return api.OnLoadResult{}, nil
			})
		},
	}
	built(observe)
	inputs := recorder.Inputs()
	var got, want []string
	for name := range files {
		abs, ok := wantByName[name]
		if !ok {
			t.Fatalf("esbuild did not load %s (%v)", name, wantByName)
		}
		want = append(want, abs)
	}
	got = slices.Sorted(maps.Keys(inputs))
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("inputs = %v, want %v", got, want)
	}
	for name, text := range files {
		abs := wantByName[name]
		if inputs[abs] != sha256Hex(text) {
			t.Errorf("%s: hash = %s", name, inputs[abs])
		}
	}
}

// hashFiles gives each file's hash in the order of the files, "" for one that cannot be read, however many there are.
func TestHashFilesGivesEachFilesHashInOrderAndNothingForAnUnreadableOne(t *testing.T) {
	dir := t.TempDir()
	var files, want []string
	for i := range 3*model.MaxComparers + 1 {
		file := filepath.Join(dir, "f"+strconv.Itoa(i)+".ts")
		text := "export const n = " + strconv.Itoa(i) + ";"
		if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
		want = append(want, model.ContentHash([]byte(text)))
	}
	files = append(files, filepath.Join(dir, "gone.ts"), dir)
	want = append(want, "", "")
	got := model.HashFiles(files)
	if len(got) != len(want) {
		t.Fatalf("%d hashes for %d files", len(got), len(files))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("hash of %s = %q, want %q", files[i], got[i], want[i])
		}
	}
	if got := model.HashFiles(nil); len(got) != 0 {
		t.Errorf("hashes of no file = %v", got)
	}
}
