package source

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestResolveConventionalForms(t *testing.T) {
	tests := []struct {
		name     string
		prepare  func(*testing.T, string) string
		language string
		form     Form
		packable bool
	}{
		{
			name: "go factory", language: "go", form: Factory, packable: true,
			prepare: func(t *testing.T, root string) string {
				writeSourceTestFile(t, filepath.Join(root, "go.mod"), "module example.com/review\n\ngo 1.26\n")
				writeSourceTestFile(t, filepath.Join(root, "extension.go"), "package review\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\nfunc Extension() *sdk.Extension { return nil }\n")
				return root
			},
		},
		{
			name: "go standalone", language: "go", form: Standalone,
			prepare: func(t *testing.T, root string) string {
				writeSourceTestFile(t, filepath.Join(root, "go.mod"), "module example.com/review\n\ngo 1.26\n")
				writeSourceTestFile(t, filepath.Join(root, "main.go"), "package main\nfunc main() {}\n")
				return root
			},
		},
		{
			name: "rust factory", language: "rust", form: Factory, packable: true,
			prepare: func(t *testing.T, root string) string {
				writeSourceTestFile(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = \"review-ext\"\nversion = \"0.1.0\"\n")
				writeSourceTestFile(t, filepath.Join(root, "src", "lib.rs"), "pub fn new_extension() -> pig_sdk::Extension { todo!() }\n")
				return root
			},
		},
		{
			name: "rust standalone", language: "rust", form: Standalone,
			prepare: func(t *testing.T, root string) string {
				writeSourceTestFile(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = \"review-ext\"\nversion = \"0.1.0\"\n")
				writeSourceTestFile(t, filepath.Join(root, "src", "main.rs"), "fn main() {}\n")
				return root
			},
		},
		{
			name: "python factory", language: "python", form: Factory, packable: true,
			prepare: func(t *testing.T, root string) string {
				writeSourceTestFile(t, filepath.Join(root, "review_ext.py"), "def new_extension() -> Extension:\n    pass\n")
				return root
			},
		},
		{
			name: "python standalone", language: "python", form: Standalone,
			prepare: func(t *testing.T, root string) string {
				path := filepath.Join(root, "main.py")
				writeSourceTestFile(t, path, "#!/usr/bin/env python3\nprint('standalone')\n")
				if err := os.Chmod(path, 0o755); err != nil {
					t.Fatal(err)
				}
				return root
			},
		},
		{
			name: "node factory stays isolated", language: "node", form: Factory,
			prepare: func(t *testing.T, root string) string {
				writeSourceTestFile(t, filepath.Join(root, "index.js"), "export default function extension(pi) {}\n")
				return root
			},
		},
		{
			name: "node executable standalone", language: "node", form: Standalone,
			prepare: func(t *testing.T, root string) string {
				path := filepath.Join(root, "run.mjs")
				writeSourceTestFile(t, path, "#!/usr/bin/env node\n")
				if err := os.Chmod(path, 0o755); err != nil {
					t.Fatal(err)
				}
				return path
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			definition, err := Resolve(tc.prepare(t, t.TempDir()))
			if err != nil {
				t.Fatal(err)
			}
			if definition.Language != tc.language || definition.Form != tc.form || definition.Packable != tc.packable {
				t.Fatalf("definition = %#v", definition)
			}
		})
	}
}

func TestResolveGoMultiFactoryModuleRequiresExactPackageSelection(t *testing.T) {
	root := t.TempDir()
	writeSourceTestFile(t, filepath.Join(root, "go.mod"), "module example.com/multi\n\ngo 1.26\n")
	for _, name := range []string{"alpha", "beta"} {
		writeSourceTestFile(t, filepath.Join(root, name, "extension.go"), "package "+name+"\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\nfunc Extension() *sdk.Extension { return nil }\n")
	}

	if _, err := Resolve(root); err == nil || !strings.Contains(err.Error(), "example.com/multi/alpha") || !strings.Contains(err.Error(), "example.com/multi/beta") {
		t.Fatalf("module-root ambiguity error = %v, want both exact candidates", err)
	}
	for _, name := range []string{"alpha", "beta"} {
		definition, err := Resolve(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("Resolve(%s): %v", name, err)
		}
		if definition.Root != root || definition.ModulePath != "example.com/multi" || definition.Package != "example.com/multi/"+name || definition.Form != Factory {
			t.Fatalf("Resolve(%s) = %#v", name, definition)
		}
	}
}

func TestResolveRejectsAmbiguousAndNonstandardFactories(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, string)
		want    string
	}{
		{
			name: "multiple languages", want: "multiple extension languages",
			prepare: func(t *testing.T, root string) {
				writeSourceTestFile(t, filepath.Join(root, "go.mod"), "module example.com/x\n")
				writeSourceTestFile(t, filepath.Join(root, "Cargo.toml"), "[package]\nname=\"x\"\n")
			},
		},
		{
			name: "go nonstandard", want: "func Extension() *sdk.Extension",
			prepare: func(t *testing.T, root string) {
				writeSourceTestFile(t, filepath.Join(root, "go.mod"), "module example.com/x\n")
				writeSourceTestFile(t, filepath.Join(root, "extension.go"), "package x\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\nfunc NewExtension() *sdk.Extension { return nil }\n")
			},
		},
		{
			name: "rust factory and standalone", want: "both factory and standalone",
			prepare: func(t *testing.T, root string) {
				writeSourceTestFile(t, filepath.Join(root, "Cargo.toml"), "[package]\nname=\"x\"\n")
				writeSourceTestFile(t, filepath.Join(root, "src", "lib.rs"), "pub fn new_extension() -> Extension { todo!() }\n")
				writeSourceTestFile(t, filepath.Join(root, "src", "main.rs"), "fn main() {}\n")
			},
		},
		{
			name: "rust nonstandard factory", want: "pub fn new_extension() -> Extension",
			prepare: func(t *testing.T, root string) {
				writeSourceTestFile(t, filepath.Join(root, "Cargo.toml"), "[package]\nname=\"x\"\n")
				writeSourceTestFile(t, filepath.Join(root, "src", "lib.rs"), "pub fn make_extension() -> Extension { todo!() }\n")
			},
		},
		{
			name: "python nonstandard factory", want: "def new_extension() factory",
			prepare: func(t *testing.T, root string) {
				writeSourceTestFile(t, filepath.Join(root, "custom.py"), "def make_extension():\n    pass\n")
			},
		},
		{
			name: "node multiple package entries", want: "resolves to 2 entrypoints",
			prepare: func(t *testing.T, root string) {
				writeSourceTestFile(t, filepath.Join(root, "package.json"), `{"pi":{"extensions":["one.js","two.js"]}}`)
				writeSourceTestFile(t, filepath.Join(root, "one.js"), "export default function one(pi) {}\n")
				writeSourceTestFile(t, filepath.Join(root, "two.js"), "export default function two(pi) {}\n")
			},
		},
		{
			name: "python multiple factories", want: "multiple Python factory modules",
			prepare: func(t *testing.T, root string) {
				writeSourceTestFile(t, filepath.Join(root, "one.py"), "def new_extension() -> Extension:\n    pass\n")
				writeSourceTestFile(t, filepath.Join(root, "two.py"), "def new_extension() -> Extension:\n    pass\n")
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			tc.prepare(t, root)
			_, err := Resolve(root)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Resolve error = %v, want %q", err, tc.want)
			}
		})
	}
}

func writeSourceTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolvePythonRejectsUnimportableFactoryModuleName(t *testing.T) {
	root := t.TempDir()
	writeSourceTestFile(t, filepath.Join(root, "9-bad.py"), "def new_extension() -> Extension:\n    pass\n")
	_, err := Resolve(root)
	if err == nil || !strings.Contains(err.Error(), "not importable") || !strings.Contains(err.Error(), "do not start with a digit") {
		t.Fatalf("Resolve error = %v", err)
	}
}

// TestResolveGoFactoryRecordsSDKModulePath pins AK-001: a factory importing
// the Pig 0.84 SDK module path is the same conventional factory as one
// importing the current path, differing only in the recorded SDK identity.
func TestResolveGoFactoryRecordsSDKModulePath(t *testing.T) {
	tests := []struct {
		name, imports, sdkModulePath string
	}{
		{name: "current", imports: `import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"`, sdkModulePath: GoSDKModulePath},
		{name: "legacy", imports: `import sdk "github.com/mainstai/pig/extensions/sdk"`, sdkModulePath: LegacyGoSDKModulePath},
		{name: "legacy implicit name", imports: `import "github.com/mainstai/pig/extensions/sdk"`, sdkModulePath: LegacyGoSDKModulePath},
		{name: "legacy alias", imports: "import (\n\t\"os\"\n\tpig \"github.com/mainstai/pig/extensions/sdk\"\n)\nvar _ = os.Getenv", sdkModulePath: LegacyGoSDKModulePath},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeSourceTestFile(t, filepath.Join(root, "go.mod"), "module example.com/ask\n\ngo 1.26\n\nrequire "+test.sdkModulePath+" v0.0.0\n")
			result := "*sdk.Extension"
			if strings.Contains(test.imports, "pig \"") {
				result = "*pig.Extension"
			}
			writeSourceTestFile(t, filepath.Join(root, "main.go"), "package ask\n"+test.imports+"\nfunc Extension() "+result+" { return nil }\n")
			definition, err := Resolve(root)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if definition.Language != "go" || definition.Form != Factory || definition.Factory != "Extension" || definition.Package != "example.com/ask" || !definition.Packable {
				t.Fatalf("definition = %+v", definition)
			}
			if definition.SDKModulePath != test.sdkModulePath {
				t.Fatalf("SDKModulePath = %q, want %q", definition.SDKModulePath, test.sdkModulePath)
			}
		})
	}
}

func TestResolveGoFactoryRejectsUnrelatedExtensionType(t *testing.T) {
	root := t.TempDir()
	writeSourceTestFile(t, filepath.Join(root, "go.mod"), "module example.com/other\n\ngo 1.26\n")
	writeSourceTestFile(t, filepath.Join(root, "main.go"), "package other\nimport sdk \"example.com/not/the/pig/sdk\"\nfunc Extension() *sdk.Extension { return nil }\n")
	if _, err := Resolve(root); err == nil || !strings.Contains(err.Error(), "has no func Extension() *sdk.Extension factory") {
		t.Fatalf("Resolve error = %v", err)
	}
}

// Upstream imports the module and reads its default export; it never scans the
// text. Bundled (`export { x as default }`) and CommonJS entries resolve as
// factories, and the Node runtime reports a module without a default export.
func TestResolveNodeEntriesWithoutLiteralExportDefault(t *testing.T) {
	for _, tc := range []struct {
		name, file, source string
		manifest           string
	}{
		{name: "bundled export list", file: "dist/index.js", manifest: `{"pi":{"extensions":["./dist"]}}`,
			source: "var index_default = function(pi) {};\nexport {\n  index_default as default,\n  helper\n};\n"},
		{name: "commonjs", file: "index.js", source: "module.exports = function (pi) {};\n"},
		{name: "no default export", file: "index.js", source: "export function extension(pi) {}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.manifest != "" {
				writeSourceTestFile(t, filepath.Join(root, "package.json"), tc.manifest)
			}
			writeSourceTestFile(t, filepath.Join(root, tc.file), tc.source)
			def, err := Resolve(root)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			want := tc.file
			if def.Language != "node" || def.Form != Factory || def.Entrypoint != filepath.Join(root, want) {
				t.Fatalf("Resolve = %+v, want node factory at %s", def, want)
			}
		})
	}
}

// A directory named in package.json "pi.extensions" expands as upstream Pi's
// package manager expands it (collectAutoExtensionEntries in
// core/package-manager.ts): the directory's own pi.extensions, else index.ts,
// else index.js, else its .ts and .js files and subdirectory entries. The
// runtime imports a file; Node refuses a directory import.
func TestResolveNodeManifestDirectoryEntries(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  []string
		err   string
	}{
		{name: "index.js", files: map[string]string{"dist/index.js": "", "dist/chunk.js": ""}, want: []string{"dist/index.js"}},
		{name: "index.ts before index.js", files: map[string]string{"dist/index.ts": "", "dist/index.js": ""}, want: []string{"dist/index.ts"}},
		{name: "nested manifest before index", files: map[string]string{
			"dist/package.json": "\ufeff{\"pi\":{\"extensions\":[\"./main.js\",\"./absent.js\"]}}", "dist/main.js": "", "dist/index.js": "",
		}, want: []string{"dist/main.js"}},
		{name: "no index: the directory's single file", files: map[string]string{"dist/tool.js": "", "dist/notes.md": "", "dist/.hidden.js": ""}, want: []string{"dist/tool.js"}},
		{name: "no index: ignored files and node_modules skipped", files: map[string]string{
			"dist/tool.ts": "", "dist/gen.js": "", "dist/.gitignore": "gen.js\n", "dist/node_modules/dep/index.js": "",
		}, want: []string{"dist/tool.ts"}},
		{name: "no index: a subdirectory's index", files: map[string]string{"dist/sub/index.js": "", "dist/empty/readme.md": ""}, want: []string{"dist/sub/index.js"}},
		{name: "no index: several files", files: map[string]string{"dist/a.js": "", "dist/b.ts": ""}, err: "resolves to 2 entrypoints"},
		{name: "no entry at all", files: map[string]string{"dist/readme.md": ""}, err: "no extension entry file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeSourceTestFile(t, filepath.Join(root, "package.json"), `{"pi":{"extensions":["./dist"]}}`)
			writeSourceTestFile(t, filepath.Join(root, "index.js"), "export default function root(pi) {}\n")
			for name, content := range tc.files {
				writeSourceTestFile(t, filepath.Join(root, filepath.FromSlash(name)), content)
			}
			entries, _, declared, err := NodeManifestEntries(root)
			if err != nil || !declared {
				t.Fatalf("NodeManifestEntries: declared=%t err=%v", declared, err)
			}
			def, err := Resolve(root)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("Resolve error = %v (entries %v), want %q", err, entries, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			want := make([]string, 0, len(tc.want))
			for _, name := range tc.want {
				want = append(want, filepath.Join(root, filepath.FromSlash(name)))
			}
			if !slices.Equal(entries, want) || def.Entrypoint != want[0] {
				t.Fatalf("entries = %v, Entrypoint = %s, want %v", entries, def.Entrypoint, want)
			}
		})
	}
}
