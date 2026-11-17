package subprocess_test

// pi: packages/ai/package.json (exports ./api/*)

import (
	"io/fs"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

var (
	relativeNamedImport   = regexp.MustCompile(`(?s)\b(?:import|export)\s*\{([^}]*)\}\s*from\s*"(\.{1,2}/[^"]+)"`)
	relativeDefaultImport = regexp.MustCompile(`\bimport\s+[\w$]+\s*(?:,\s*\{[^}]*\}\s*)?from\s*"(\.{1,2}/[^"]+)"`)
	relativeStarImport    = regexp.MustCompile(`\b(?:import|export)\s*\*\s*(?:as\s+[\w$]+\s*)?from\s*"(\.{1,2}/[^"]+)"`)
	stubExportDeclaration = regexp.MustCompile(`(?m)^export const (?:\{([^}]*)\}|([\w$]+))\s*=`)
	stubReexport          = regexp.MustCompile(`(?m)^export \{([^}]*)\} from "\./[^"]+";$`)
)

// moduleFileSpecifier reports whether a relative specifier names a JavaScript module or data file the runtime loads.
func moduleFileSpecifier(specifier string) bool {
	return strings.HasSuffix(specifier, ".js") || strings.HasSuffix(specifier, ".mjs") || strings.HasSuffix(specifier, ".json")
}

// unresolvedRelativeImports returns one line for every static relative import of the vendored Pi packages (pi-dist) in the vendored runtime tree fsys (shims), of a file that is not there, and of a name that a bridge stub (vendoredBridgeStubs) does not export. A bridge stub exports `stream` and `streamSimple` (`generateImages` for openrouter-images) and the values it re-exports from Pi's own module of the implementation (vendoredAPIReexports): an import of a value Pi's implementation exports beyond those would fail the whole module at link time, so a Pi release that adds one must fail the vendoring gate here.
func unresolvedRelativeImports(t *testing.T, fsys fs.FS) []string {
	t.Helper()
	var problems []string
	err := fs.WalkDir(fsys, "pi-dist", func(file string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || (!strings.HasSuffix(file, ".js") && !strings.HasSuffix(file, ".mjs")) || strings.Contains(file, "/sdk-bundle/") || strings.Contains(file, "/node_modules/") {
			return nil
		}
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return err
		}
		text := string(data)
		check := func(specifier string, imported []string) {
			if !moduleFileSpecifier(specifier) {
				return
			}
			target := path.Join(path.Dir(file), specifier)
			if _, err := fs.Stat(fsys, target); err != nil {
				problems = append(problems, file+" imports "+specifier+", which is not vendored")
				return
			}
			rel := strings.TrimPrefix(target, "pi-dist/")
			if _, stub := vendoredBridgeStubs[rel]; !stub {
				return
			}
			source, err := fs.ReadFile(fsys, target)
			if err != nil {
				t.Fatal(err)
			}
			var exported []string
			for _, declaration := range stubExportDeclaration.FindAllStringSubmatch(string(source), -1) {
				exported = append(exported, strings.FieldsFunc(declaration[1]+","+declaration[2], func(r rune) bool { return r == ',' || r == ' ' || r == '\n' })...)
			}
			for _, declaration := range stubReexport.FindAllStringSubmatch(string(source), -1) {
				exported = append(exported, strings.FieldsFunc(declaration[1], func(r rune) bool { return r == ',' || r == ' ' || r == '\n' })...)
			}
			for _, name := range imported {
				if !slices.Contains(exported, name) {
					problems = append(problems, file+" imports "+name+" from the bridge stub "+rel+", which exports only "+strings.Join(exported, ", "))
				}
			}
		}
		for _, m := range relativeNamedImport.FindAllStringSubmatch(text, -1) {
			var names []string
			for item := range strings.SplitSeq(m[1], ",") {
				if name, _, _ := strings.Cut(strings.TrimSpace(item), " as "); name != "" {
					names = append(names, strings.TrimSpace(name))
				}
			}
			check(m[2], names)
		}
		for _, m := range relativeDefaultImport.FindAllStringSubmatch(text, -1) {
			check(m[1], []string{"default"})
		}
		for _, m := range relativeStarImport.FindAllStringSubmatch(text, -1) {
			check(m[1], nil)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return problems
}

// TestVendoredModulesImportOnlyWhatTheBridgeStubsExport: a vendored module that statically imports a value Pi's builtin API implementation exports but the bridge stub of it does not (today the Codex WebSocket helpers), or a relative file the vendoring left out, fails at link time with a SyntaxError that takes the whole extension with it. Pi's package exports those only through `./api/*`, which no extension can resolve (TestExtensionImportsOfPiAiSubpathsResolveAsUnderPi); this keeps every other path to them closed, and a pin that opens one fails here.
func TestVendoredModulesImportOnlyWhatTheBridgeStubsExport(t *testing.T) {
	shims := os.DirFS("runtime-node/shims")
	if problems := unresolvedRelativeImports(t, shims); len(problems) > 0 {
		t.Errorf("the vendored runtime has link errors:\n%s", strings.Join(problems, "\n"))
	}
}

// The checker itself: it finds a name the stub lacks, an absent file, a default import of a stub, and accepts the names a stub exports, a namespace re-export and an import of a vendored file.
func TestUnresolvedRelativeImportsFindsWhatAStubLacks(t *testing.T) {
	stub := func(api string) *fstest.MapFile {
		return &fstest.MapFile{Data: []byte(vendoredBridgeStubs["pi-ai/api/"+api+".js"])}
	}
	fsys := fstest.MapFS{
		"pi-dist/pi-ai/api/openai-completions.js":      stub("openai-completions"),
		"pi-dist/pi-ai/api/openai-completions.impl.js": {Data: []byte("export function convertMessages() {}\n")},
		"pi-dist/pi-ai/api/pi-messages.js":             stub("pi-messages"),
		"pi-dist/pi-ai/api/pi-messages.impl.js":        {Data: []byte("export class PiMessagesResponseError extends Error {}\n")},
		"pi-dist/pi-ai/api/openai-codex-responses.js":  stub("openai-codex-responses"),
		"pi-dist/pi-ai/api/openrouter-images.js":       stub("openrouter-images"),
		"pi-ai-bridge.mjs":                             {Data: []byte("export const bridgeApi = () => ({});\n")},
		"pi-dist/pi-ai/api/lazy.js":                    {Data: []byte("export const lazyApi = () => 1;\n")},
		"pi-dist/pi-ai/ok.js": {Data: []byte("import { stream, streamSimple as s, convertMessages } from \"./api/openai-completions.js\";\nimport { lazyApi } from \"./api/lazy.js\";\n" +
			"import {\n  generateImages,\n} from \"./api/openrouter-images.js\";\nexport * from \"./api/pi-messages.js\";\nimport { PiMessagesResponseError } from \"./api/pi-messages.js\";\n")},
		"pi-dist/pi-ai/bad.js": {Data: []byte("import { stream, convertMessage } from \"./api/openai-completions.js\";\n" +
			"import { codexNoSuchHelper,\n  streamSimple } from \"./api/openai-codex-responses.js\";\nimport { retryGoogleRequest } from \"./api/absent.js\";\n" +
			"import completions from \"./api/openai-completions.js\";\n")},
		"pi-dist/pi-ai/sdk-bundle/chunk.js": {Data: []byte("import { nothing } from \"./absent.js\";\n")},
	}
	want := []string{
		"pi-dist/pi-ai/bad.js imports codexNoSuchHelper from the bridge stub pi-ai/api/openai-codex-responses.js, which exports only stream, streamSimple, closeOpenAICodexWebSocketSessions, getOpenAICodexWebSocketDebugStats, resetOpenAICodexWebSocketDebugStats",
		"pi-dist/pi-ai/bad.js imports convertMessage from the bridge stub pi-ai/api/openai-completions.js, which exports only stream, streamSimple, convertMessages",
		"pi-dist/pi-ai/bad.js imports default from the bridge stub pi-ai/api/openai-completions.js, which exports only stream, streamSimple, convertMessages",
		"pi-dist/pi-ai/bad.js imports ./api/absent.js, which is not vendored",
	}
	got := unresolvedRelativeImports(t, fsys)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("problems =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
