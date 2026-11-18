package main

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/internal/sizereport"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// viewWidgetSymbol matches the code of the view kit's widgets in the extension host: the builder of each widget kind, the
// types a builder makes (their methods, type descriptors and method tables) and the helpers only a builder calls.
var viewWidgetSymbol = regexp.MustCompile(`/coding/extension/host/subprocess\.\(?\*?(` + strings.Join([]string{
	"buildGrid", "viewGrid", "viewGridLayout", "chipsOf", "chipLines", "gridClean", "gridPad", "gridContrast",
	"buildInput", "viewInput", "viewInputState", "viewInputComponent",
	"buildScrollView", "viewScroll", "viewScrollComponent", "viewChildProxy",
	"buildMouseRegion", "viewMouseRegion",
	"buildBorderedLoader", "viewBordered",
	"buildCancellableLoader", "viewCancellable", "loaderSetup",
	"buildBranchSummary", "buildCompactionSummary", "buildCustomMessage", "buildSkillInvocation", "viewExpandable",
	"buildEditor", "viewEditorComponent", "viewEditorState", "editorSlashCommands",
}, "|") + `)(\W|$)`)

// A program that embeds the extension host and enables no view widget links none: the builders of the grid, the input, the
// scroll view, the mouse region, the loaders, the message components and the editor reach a program only through
// subprocess.EnableViewWidgets and subprocess.EnableEditorViews, and the example calls neither. The size gate only notices
// growth past a ceiling; this reads the link: the symbol table of an unstripped build, plain and with the size gate's
// stripped tag set (test/perfgate headlessVariants).
func TestHeadlessEmbedLinksNoViewWidget(t *testing.T) {
	root := testenv.ModuleRoot(t)
	stripped := slices.DeleteFunc(piglet.StripBuildTags(), func(tag string) bool { return tag == "pig_strip_mcp" })
	for _, variant := range []struct{ name, tags string }{{"default", ""}, {"stripped", strings.Join(stripped, ",")}} {
		t.Run(variant.name, func(t *testing.T) {
			build := sizereport.Build{Root: root, Package: "./examples/headless", GOOS: "linux", GOARCH: "amd64", Tags: variant.tags}
			symbols, err := build.Symbols(t.Context(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			host := false
			for _, symbol := range symbols {
				if strings.HasSuffix(symbol.Name, "/coding/extension/host/subprocess.NewUIBridge") {
					host = true
				}
				if viewWidgetSymbol.MatchString(symbol.Name) {
					t.Errorf("the headless embed links %s (%d bytes)", symbol.Name, symbol.Size)
				}
			}
			if !host {
				t.Fatalf("the symbol table (%d symbols) does not hold subprocess.NewUIBridge: nothing was read that can be judged", len(symbols))
			}
		})
	}
}

// An embedder that mounts widgets calls EnableViewWidgets and still links no editor (D107): the widget builders reach the
// program, and the builder of the editor, the terminal editor behind it and its word dictionaries do not, since
// EnableEditorViews is its own call. The program is testdata/widgetembed, the example's host with that one call.
func TestHeadlessEmbedWithViewWidgetsLinksNoEditor(t *testing.T) {
	root := testenv.ModuleRoot(t)
	build := sizereport.Build{Root: root, Package: "./examples/headless/testdata/widgetembed", GOOS: "linux", GOARCH: "amd64"}
	symbols, err := build.Symbols(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	editor := regexp.MustCompile(`/coding/extension/host/subprocess\.\(?\*?(buildEditor|viewEditorComponent|viewEditorState|editorSlashCommands)(\W|$)`)
	widgets := false
	for _, symbol := range symbols {
		if strings.HasSuffix(symbol.Name, "/coding/extension/host/subprocess.buildGrid") {
			widgets = true
		}
		if editor.MatchString(symbol.Name) {
			t.Errorf("a program that enabled only the view widgets links %s (%d bytes)", symbol.Name, symbol.Size)
		}
	}
	if !widgets {
		t.Fatalf("the symbol table (%d symbols) holds no grid builder: EnableViewWidgets linked no widget", len(symbols))
	}
}
