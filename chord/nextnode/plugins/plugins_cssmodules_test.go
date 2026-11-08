package plugins

// pi: packages/chord/src/node/plugins/css-modules.ts

import (
	"context"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/build"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

// Ports packages/chord/test/node/plugins/build.test.ts's CSS cases (slice 11 §4b, fix round 1 Opus L1): a plugin's
// `*.module.css` has its class names scoped to the plugin and the file, the import giving the mapping.

// Ports build.test.ts "scopes a CSS module's class names to its file, the import giving the mapping (slice 11 §4b)".
func TestEsbuildBuildScopesACSSModulesClassNamesToItsFileTheImportGivingTheMapping(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "web", "@test/web", map[string]string{
		"web.ts":           "import styles from \"./notes.module.css\";\nexport default { setup: () => styles.card };",
		"notes.module.css": ".card { color: var(--accent); }",
	}, nil)
	_, plugin := esbuildDiscovered(t, "@test/web", root)
	built := esbuildScript(t, esbuildBuild(t, plugin, "web", esbuildBrowser))
	if built.CSS == nil {
		t.Fatal("no stylesheet")
	}
	scoped := regexp.MustCompile(`\.(chord_[0-9a-f]{8}_notes_card)\{color:var\(--accent\)\}`).FindStringSubmatch(built.CSS.Text)
	if scoped == nil {
		t.Fatalf("css = %q", built.CSS.Text)
	}
	if want := `card:"` + scoped[1] + `"`; !strings.Contains(built.Source, want) {
		t.Errorf("source lacks %s", want)
	}
	// Another plugin's `notes.module.css` gets other names.
	writePlugin(t, root, "other", "@test/other", map[string]string{
		"web.ts":           "import styles from \"./notes.module.css\";\nexport default { setup: () => styles.card };",
		"notes.module.css": ".card { color: red; }",
	}, nil)
	_, otherPlugin := esbuildDiscovered(t, "@test/other", root)
	other := esbuildScript(t, esbuildBuild(t, otherPlugin, "web", esbuildBrowser))
	if other.CSS == nil || !regexp.MustCompile(`\.chord_[0-9a-f]{8}_notes_card\{`).MatchString(other.CSS.Text) {
		t.Fatalf("other css = %+v", other.CSS)
	}
	if strings.Contains(other.CSS.Text, scoped[1]) {
		t.Errorf("both plugins' classes are named %s", scoped[1])
	}
}

// Ports build.test.ts "gives a CSS module's url() assets, its @import, composes and escaped class names (Opus L1)".
func TestEsbuildBuildGivesACSSModulesURLAssetsItsImportComposesAndEscapedClassNames(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "web", "@test/web", map[string]string{
		"web.ts": "import styles from \"./notes.module.css\";\nexport default { setup: () => styles };",
		"notes.module.css": strings.Join([]string{
			`@import "./plain.css";`,
			".base { color: red; }",
			".card { composes: base; background: url(./bg.png); }",
			".café { color: blue; }",
		}, "\n"),
		"plain.css": ".plain { color: green; }",
		"bg.png":    "PNG",
	}, nil)
	_, plugin := esbuildDiscovered(t, "@test/web", root)
	built := esbuildScript(t, esbuildBuild(t, plugin, "web", esbuildBrowser))
	if built.CSS == nil {
		t.Fatal("no stylesheet")
	}
	css := built.CSS.Text
	card := regexp.MustCompile(`\.(chord_[0-9a-f]{8}_notes_card)\{`).FindStringSubmatch(css)
	base := regexp.MustCompile(`\.(chord_[0-9a-f]{8}_notes_base)\{`).FindStringSubmatch(css)
	if card == nil || base == nil {
		t.Fatalf("css = %q", css)
	}
	// composes: the class names both; the escaped name maps from the real one.
	if want := `card:"` + base[1] + " " + card[1] + `"`; !strings.Contains(built.Source, want) {
		t.Errorf("source lacks %s", want)
	}
	if !regexp.MustCompile(`caf\\u00E9:"chord_[0-9a-f]{8}_notes_caf\\xE9"`).MatchString(built.Source) {
		t.Errorf("source lacks the escaped class name")
	}
	if !strings.Contains(css, ".plain{color:green}") {
		t.Errorf("css = %q", css)
	}
	if len(built.Assets) != 1 || !regexp.MustCompile(`^bg-[A-Z0-9]+\.png$`).MatchString(built.Assets[0].Name) {
		t.Fatalf("assets = %+v", built.Assets)
	}
	if want := `url("/assets/` + built.Assets[0].Name + `")`; !strings.Contains(css, want) {
		t.Errorf("css lacks %s: %q", want, css)
	}
}

// Ports build.test.ts "gives a plain stylesheet's url() an asset".
func TestEsbuildBuildGivesAPlainStylesheetsURLAnAsset(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "web", "@test/web", map[string]string{
		"web.ts": "import \"./a.css\";\nexport default { setup: () => {} };",
		"a.css":  ".x { background: url(./bg.png); }",
		"bg.png": "PNG",
	}, nil)
	_, plugin := esbuildDiscovered(t, "@test/web", root)
	built := esbuildScript(t, esbuildBuild(t, plugin, "web", esbuildBrowser))
	if built.CSS == nil || len(built.Assets) != 1 {
		t.Fatalf("css = %+v, assets = %+v", built.CSS, built.Assets)
	}
	if want := `url("/assets/` + built.Assets[0].Name + `")`; !strings.Contains(built.CSS.Text, want) {
		t.Errorf("css lacks %s: %q", want, built.CSS.Text)
	}
}

// Ports css-modules.ts's reading of a module as text: build.ts hands `scopeCssModule` `new TextDecoder().decode(contents)`, so
// a byte that is not UTF-8 is U+FFFD before the class names are scoped (the review of this slice: `.a<0xFF>b{}` is the class
// `a\ufffdb`, esbuild writes it `a\fffd b` with the space that ends the escape, and the mapping names it).
func TestEsbuildBuildReadsACSSModuleAsUTF8LikeTextDecoderInvalidBytesAreU_FFFDBeforeScoping(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "web", "@test/web", map[string]string{
		"web.ts":           "import styles from \"./notes.module.css\";\nexport default { setup: () => styles };",
		"notes.module.css": ".a\xffb{color:red}\n.c{content:\"\xff\"}\n/* \xc3 */",
	}, nil)
	_, plugin := esbuildDiscovered(t, "@test/web", root)
	built := esbuildScript(t, esbuildBuild(t, plugin, "web", esbuildBrowser))
	if built.CSS == nil {
		t.Fatal("no stylesheet")
	}
	for _, want := range []string{`_a\fffd b{color:red}`, `_c{content:"\fffd"}`} {
		if !strings.Contains(built.CSS.Text, want) {
			t.Errorf("css = %q, want %q in it", built.CSS.Text, want)
		}
	}
	if strings.Contains(built.CSS.Text, `\fffdb`) {
		t.Errorf("css = %q: the escape of U+FFFD runs into the `b` after it", built.CSS.Text)
	}
}

// The evaluator a build is given starts one Node subprocess for the mappings of its CSS modules, started with NodeOptions.Command,
// and ends it when it is closed: a build with no CSS module starts none, and no process is left.
func TestEsbuildBuildEndsTheNodeSubprocessThatReadsCSSModuleMappings(t *testing.T) {
	var mu sync.Mutex
	var started []*exec.Cmd
	options := esbuildBrowser
	node := NodeOptions{}
	node.Command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, name, args...)
		mu.Lock()
		started = append(started, cmd)
		mu.Unlock()
		return cmd
	}
	evaluator := newMappingEvaluator(node)
	options.Mappings = evaluator
	closed := false
	evaluators := func() (count int) {
		mu.Lock()
		defer mu.Unlock()
		for _, cmd := range started {
			if slices.Contains(cmd.Args, "-e") {
				count++
			}
			if cmd.ProcessState == nil && closed {
				t.Errorf("%v is still running after the evaluator was closed", cmd.Args[1:])
			}
		}
		return count
	}
	root := tempRoot(t)
	writePlugin(t, root, "plain", "@test/plain", map[string]string{"web.ts": "export default { setup: () => 1 };"}, nil)
	writePlugin(t, root, "web", "@test/web", map[string]string{
		"web.ts":       "import a from \"./a.module.css\";\nimport b from \"./b.module.css\";\nexport default { setup: () => [a, b] };",
		"a.module.css": ".x{color:red}",
		"b.module.css": ".y{color:blue}",
	}, nil)
	_, plain := esbuildDiscovered(t, "@test/plain", root)
	esbuildScript(t, esbuildBuild(t, plain, "web", options))
	if got := evaluators(); got != 0 {
		t.Fatalf("%d evaluator processes for a build with no CSS module", got)
	}
	_, plugin := esbuildDiscovered(t, "@test/web", root)
	esbuildScript(t, esbuildBuild(t, plugin, "web", options))
	if got := evaluators(); got != 1 {
		t.Fatalf("%d evaluator processes for a build with two CSS modules, want 1", got)
	}
	evaluator.close()
	closed = true
	evaluators()
}

// Builds that share an evaluator (the component's) start one Node subprocess between them, however many facets with CSS modules
// they build at once, and it runs until the evaluator is closed.
func TestEsbuildBuildsThatShareAnEvaluatorStartOneNodeSubprocessBetweenThem(t *testing.T) {
	var mu sync.Mutex
	var started []*exec.Cmd
	options := esbuildBrowser
	node := NodeOptions{}
	node.Command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, name, args...)
		if slices.Contains(args, "-e") {
			mu.Lock()
			started = append(started, cmd)
			mu.Unlock()
		}
		return cmd
	}
	shared := newMappingEvaluator(node)
	options.Mappings = shared
	root := tempRoot(t)
	const plugins = 6
	for i := range plugins {
		name := "p" + strconv.Itoa(i)
		writePlugin(t, root, name, "@test/"+name, map[string]string{
			"web.ts":       "import a from \"./a.module.css\";\nexport default { setup: () => a };",
			"a.module.css": ".x{color:red}",
		}, nil)
	}
	var group sync.WaitGroup
	for i := range plugins {
		_, plugin := esbuildDiscovered(t, "@test/p"+strconv.Itoa(i), root)
		group.Go(func() { esbuildScript(t, esbuildBuild(t, plugin, "web", options)) })
	}
	group.Wait()
	mu.Lock()
	count, running := len(started), started[0].ProcessState == nil
	mu.Unlock()
	if count != 1 || !running {
		t.Fatalf("%d evaluator processes, running = %v; want one, running", count, running)
	}
	shared.close()
	if started[0].ProcessState == nil {
		t.Fatal("the shared process is still running after close")
	}
}

// The component's builds share one Node subprocess for the mappings of their CSS modules (buildOne passes the component's
// evaluator), and Close ends it.
func TestPluginsBuildsShareOneEvaluatorProcessAndCloseEndsIt(t *testing.T) {
	var mu sync.Mutex
	var started []*exec.Cmd
	node := NodeOptions{CacheDir: t.TempDir(), Command: func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, name, args...)
		if slices.Contains(args, mappingScript) {
			mu.Lock()
			started = append(started, cmd)
			mu.Unlock()
		}
		return cmd
	}}
	root := tempRoot(t)
	var names []string
	for i := range 3 {
		name := "p" + strconv.Itoa(i)
		writePlugin(t, root, name, "@test/"+name, map[string]string{
			"web.ts":       "import a from \"./a.module.css\";\nexport default { setup: () => a };",
			"a.module.css": ".x{color:red}",
		}, nil)
		names = append(names, "@test/"+name)
	}
	component := CreatePlugins(PluginsOptions{Build: build.BuildFacet, Roots: []model.PluginRoot{{Path: root}}, Kinds: pcKinds(nil), Node: node}, background)
	closed := false
	defer func() {
		if !closed {
			component.Close()
		}
	}()
	built, err := component.Build(names, background)
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Facets) != len(names) {
		t.Fatalf("%d facets built, want %d: %+v", len(built.Facets), len(names), built)
	}
	for _, facet := range built.Facets {
		if facet.Kind != model.BuiltFacetScript {
			t.Fatalf("facet did not build: %+v", facet)
		}
	}
	mu.Lock()
	count, running := len(started), len(started) > 0 && started[0].ProcessState == nil
	mu.Unlock()
	if count != 1 || !running {
		t.Fatalf("%d evaluator processes after the component's builds, running = %v; want one, running", count, running)
	}
	component.Close()
	closed = true
	if started[0].ProcessState == nil {
		t.Fatal("the shared process is still running after Plugins.Close")
	}
}
