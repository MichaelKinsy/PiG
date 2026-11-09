package plugins

// pi: packages/chord/src/node/plugins/testing.ts

import (
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/test/node/plugins/testing.test.ts: testing a plugin alone (slice 12 §6), the plugin built as production
// builds it against the app's API modules, loaded beside a stub serving what it uses; the registries it contributes to that
// nobody serves collected; the contracts it waits for unserved listed; a principal for the host and for a root scope.
//
// Adapted to the process boundary (docs/plan/chord-plugins-node-evaluator.md, D502, D503): the plugin's host is the Node
// subprocess, so `t.host.get` is NodeHost.Get on TestedPlugin.Host, the stub is a Go facet bridged to it (TestStub), the app's
// API modules are files (a module re-exporting Pi's own Chord, as Pi's test spreads `chord`), a registry is named by its name,
// and `root`'s closure is RootPrincipal (what that test's closure returns).

// tpModules writes the app's API v3 for a test: Chord's surface re-exported, and a worker module with the app's `Panels`
// (registry `acme.panels`, version 2), under a new directory; the Node host's tree is the one extracted under cache.
func tpModules(t *testing.T, cache string) HostPackages {
	t.Helper()
	return tpAppModules(t, cache, `defineRegistry("acme.panels", { version: 2 })`, "Panels")
}

// tpAppModules is tpModules with the worker module's one registry export: its name and its `defineRegistry` expression.
func tpAppModules(t *testing.T, cache, registry, name string) HostPackages {
	t.Helper()
	tree, err := extractNode(cache)
	if err != nil {
		t.Fatal(err)
	}
	src := "file://" + filepath.ToSlash(filepath.Join(tree, "pi", "chord", "src")) + "/index.ts"
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"v3.ts":        `export * from "` + src + `";` + "\n",
		"v3/worker.ts": `import { defineRegistry } from "` + src + `";` + "\nexport const " + name + " = " + registry + ";\n",
	})
	return HostPackages{"@acme/app/v3": filepath.Join(dir, "v3.ts"), "@acme/app/v3/worker": filepath.Join(dir, "v3", "worker.ts")}
}

var tpAPI = &AppApi{Name: "@acme/app", Versions: []string{"v3"}}

// tpNotes is the notes contract the stats plugin uses, as the notes plugin declares it.
var tpNotes = mustContract(contracts.Local("acme.notes", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("project", contracts.Str())},
	Members: []contracts.LocalEntry{contracts.Mem("words", contracts.Int())},
}))

// tpStats is the stats plugin's contract, as a test of it declares it again.
var tpStats = mustContract(contracts.Local("acme.stats", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("project", contracts.Str())},
	Members: []contracts.LocalEntry{contracts.Mem("words", contracts.Int())},
}))

// tpEmpty is the params of a contract without any.
func tpEmpty() *delta.JsonObject { return delta.NewJsonObject(0) }

// tpOpts are the options every case shares: the worker kind, the app's API and modules, a Node host cached under cache.
func tpOpts(t *testing.T, cache string) TestPluginOptions {
	t.Helper()
	return TestPluginOptions{Kind: "worker", Api: tpAPI, Modules: tpModules(t, cache), Node: NodeOptions{CacheDir: cache}}
}

// tpNotesStub is a stub of the notes plugin: every project has 42 words.
var tpNotesStub = TestStub{
	Name:      "@acme/notes",
	Contracts: []*contracts.Contract{tpNotes},
	Facet: host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		return h.Serve(tpNotes, func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"words": self.Derive(nil, func([]any) (delta.JsonValue, error) { return 42.0, nil })},
			}}, nil
		}, host.ServeOptions{})
	}),
}

// tpJSON is v as JSON text, to compare values the way the wire carries them.
func tpJSON(t *testing.T, v any) string {
	t.Helper()
	text, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

// tpPlugin tests the plugin in dir with options, closed when the test ends.
func tpPlugin(t *testing.T, dir string, options TestPluginOptions) *TestedPlugin {
	t.Helper()
	tested, err := TestPlugin(dir, options, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tested.Close(background) })
	return tested
}

// tpValue is the value of the state member of the object `contract` of params on the plugin's host.
func tpValue(t *testing.T, tested *TestedPlugin, contract *contracts.Contract, params *delta.JsonObject, member string) delta.JsonValue {
	t.Helper()
	ref, err := tested.Host.Get(background, contract, params, host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ref.Release)
	value, err := ref.Member(member).Value()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// tpContributed is the contributions of registry name as `[plugin, value]` pairs, in JSON.
func tpContributed(t *testing.T, tested *TestedPlugin, name string) string {
	t.Helper()
	table, err := tested.Contributions(name, background)
	if err != nil {
		t.Fatal(err)
	}
	pairs := make([][]any, len(table))
	for i, entry := range table {
		pairs[i] = []any{entry.Plugin, entry.Value}
	}
	return tpJSON(t, pairs)
}

// tpStatsPlugin is a root with the stats plugin: serves `Stats` over `Notes`, contributes a panel, and a tag cloud over `Tags`.
func tpStatsPlugin(t *testing.T) string {
	t.Helper()
	return writePlugin(t, t.TempDir(), "stats", "@acme/stats", map[string]string{"worker.ts": strings.Join([]string{
		`import { constant, defineFacet, int, local, str } from "@acme/app/v3";`,
		`import { Panels } from "@acme/app/v3/worker";`,
		`const Notes = local("acme.notes", { version: 1, params: { project: str() }, members: { words: int() } });`,
		`const Tags = local("acme.tags", { version: 1, params: {}, members: { list: str() } });`,
		`const Stats = local("acme.stats", { version: 1, params: { project: str() }, members: { words: int() } });`,
		`const Cloud = local("acme.cloud", { version: 1, params: {}, members: { list: str() } });`,
		"export default defineFacet((host, self) => {",
		`	self.contribute(Panels, { title: "Word count" });`,
		"	host.serve(Stats, ({ project }, self) => ({ words: self.get(Notes, { project }).words }));",
		"	host.serve(Cloud, (_params, self) => ({ list: self.get(Tags, {}).list }));",
		"});",
	}, "\n")}, nil)
}

// testing.test.ts › testPlugin › loads the plugin built against the API beside a stub; collects its contributions and lists what
// it waits for.
func TestTestPluginLoadsThePluginBuiltAgainstTheAPIBesideAStubCollectsItsContributionsAndListsWhatItWaitsFor(t *testing.T) {
	cache := t.TempDir()
	options := tpOpts(t, cache)
	options.Stubs = []TestStub{tpNotesStub}
	options.Principal = delta.JsonObjectOf("kind", "user", "id", "u1")
	tested := tpPlugin(t, tpStatsPlugin(t), options)

	if got := tpValue(t, tested, tpStats, delta.JsonObjectOf("project", "p1"), "words"); got != 42.0 {
		t.Fatalf("words = %v, want 42", got)
	}
	if got, want := tpContributed(t, tested, "acme.panels"), `[["@acme/stats",{"title":"Word count"}]]`; got != want {
		t.Fatalf("panels = %s, want %s", got, want)
	}
	unserved, err := tested.Unserved(background)
	if err != nil || !slices.Equal(unserved, []string{"acme.tags"}) {
		t.Fatalf("unserved = %v, %v, want [acme.tags]", unserved, err)
	}
	own, err := tested.RootPrincipal(nil, background)
	if err != nil || tpJSON(t, own) != `{"kind":"user","id":"u1"}` {
		t.Fatalf("root principal = %v, %v", own, err)
	}
	other, err := tested.RootPrincipal(delta.JsonObjectOf("kind", "user", "id", "u2"), background)
	if err != nil || tpJSON(t, other) != `{"kind":"user","id":"u2"}` {
		t.Fatalf("root principal as u2 = %v, %v", other, err)
	}
	if _, err := tested.Contributions("acme.other", background); err == nil || !strings.Contains(err.Error(), "testPlugin: registry acme.other is not collected") {
		t.Fatalf("contributions of acme.other: %v", err)
	}
}

// testing.test.ts › testPlugin › rejects a build production would refuse, and a setup that throws.
func TestTestPluginRejectsABuildProductionWouldRefuseAndASetupThatThrows(t *testing.T) {
	cache := t.TempDir()
	root := t.TempDir()
	old := writePlugin(t, root, "old", "@acme/old", map[string]string{
		"worker.ts": "import { defineFacet } from \"@acme/app/v1\";\nexport default defineFacet(() => {});",
	}, nil)
	_, err := TestPlugin(old, tpOpts(t, cache), background)
	if err == nil || !strings.Contains(err.Error(), `"@acme/app/v1": @acme/app/v1 is not available: this app provides v3`) {
		t.Fatalf("old: %v", err)
	}
	throwing := writePlugin(t, root, "throwing", "@acme/throwing", map[string]string{
		"worker.ts": "import { defineFacet } from \"@acme/app/v3\";\nexport default defineFacet(() => { throw new Error(\"no\"); });",
	}, nil)
	_, err = TestPlugin(throwing, tpOpts(t, cache), background)
	if err == nil || err.Error() != `testPlugin: setup failed: plugin "@acme/throwing": no` {
		t.Fatalf("throwing: %v", err)
	}
}

// testing.test.ts › testPlugin › builds as production by default: the per-kind check active, api required with versioned modules
// (Opus M5).
func TestTestPluginBuildsAsProductionByDefaultThePerKindCheckActiveAPIRequiredWithVersionedModules(t *testing.T) {
	cache := t.TempDir()
	dir := writePlugin(t, t.TempDir(), "peek", "@acme/peek", map[string]string{
		"worker.ts":       "import { defineFacet } from \"@acme/app/v3\";\nimport { panel } from \"./contract.web.ts\";\nexport default defineFacet(() => console.log(panel));",
		"contract.web.ts": `export const panel = "panel";`,
	}, nil)
	options := tpOpts(t, cache)
	if _, err := TestPlugin(dir, options, background); err == nil || !strings.Contains(err.Error(), `"./contract.web.ts": a web file, imported into a worker facet`) {
		t.Fatalf("per-kind check: %v", err)
	}
	options.Api = nil
	want := `testPlugin: modules hold the app's versioned API ("@acme/app/v3"): pass api, as production does`
	if _, err := TestPlugin(dir, options, background); err == nil || err.Error() != want {
		t.Fatalf("without api: %v, want %s", err, want)
	}
}

// tpOne is a local contract of no params with one text member.
func tpOne(name, member string) *contracts.Contract {
	return mustContract(contracts.Local(name, contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem(member, contracts.Str())},
	}))
}

// testing.test.ts › testPlugin › tests a single-file plugin and a plugin in a checkout of several (Opus L4).
func TestTestPluginTestsASingleFilePluginAndAPluginInACheckoutOfSeveralOpusL4(t *testing.T) {
	cache := t.TempDir()
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"hello.worker.ts": strings.Join([]string{
		`import { constant, defineFacet, local, str } from "@acme/app/v3";`,
		`const Hello = local("acme.hello", { version: 1, params: {}, members: { text: str() } });`,
		`export default defineFacet((host) => host.serve(Hello, () => ({ text: constant("hello") })));`,
	}, "\n")})
	single := tpPlugin(t, filepath.Join(root, "hello.worker.ts"), tpOpts(t, cache))
	if got := tpValue(t, single, tpOne("acme.hello", "text"), tpEmpty(), "text"); got != "hello" {
		t.Fatalf("hello = %v", got)
	}
	if err := single.Close(background); err != nil {
		t.Fatal(err)
	}
	// A checkout of several (its own package.json) beside another plugin of the root it copies from.
	writePlugin(t, root, "words", "@acme/words", map[string]string{"contract.ts": `export const WORDS = "words";`}, nil)
	writeFiles(t, filepath.Join(root, "repo"), map[string]string{"package.json": `{"name":"@bob/repo"}`})
	inner := writePlugin(t, filepath.Join(root, "repo"), "counter", "@bob/counter", map[string]string{"worker.ts": strings.Join([]string{
		`import { constant, defineFacet, local, str } from "@acme/app/v3";`,
		`import { WORDS } from "../../words/contract.ts";`,
		`const Count = local("bob.count", { version: 1, params: {}, members: { text: str() } });`,
		"export default defineFacet((host) => host.serve(Count, () => ({ text: constant(WORDS) })));",
	}, "\n")}, nil)
	checkout := tpPlugin(t, inner, tpOpts(t, cache))
	if got := tpValue(t, checkout, tpOne("bob.count", "text"), tpEmpty(), "text"); got != "words" {
		t.Fatalf("count = %v", got)
	}
}

// testing.test.ts › testPlugin › collects until no new registry appears: an object live after the first collection contributes
// too (Sol).
func TestTestPluginCollectsUntilNoNewRegistryAppearsAnObjectLiveAfterTheFirstCollectionContributesTooSol(t *testing.T) {
	dir := writePlugin(t, t.TempDir(), "chain", "@acme/chain", map[string]string{"worker.ts": strings.Join([]string{
		`import { constant, defineFacet, defineRegistry, local, str } from "@acme/app/v3";`,
		`const A = defineRegistry("acme.a", { version: 1 });`,
		`const B = defineRegistry("acme.b", { version: 1 });`,
		`const Reader = local("acme.reader", { version: 1, params: {}, members: { s: str() } });`,
		"export default defineFacet((host, self) => {",
		`	self.contribute(A, "a");`,
		`	host.serve(Reader, (_params, self) => { self.registry(A); self.contribute(B, "b"); return { s: constant("x") }; });`,
		"});",
	}, "\n")}, nil)
	tested := tpPlugin(t, dir, tpOpts(t, t.TempDir()))
	if got, want := tpContributed(t, tested, "acme.b"), `[["@acme/chain","b"]]`; got != want {
		t.Fatalf("acme.b = %s, want %s", got, want)
	}
}

// testing.test.ts › testPlugin › waits for slow activations a collection woke before it looks again (Opus R1).
func TestTestPluginWaitsForSlowActivationsACollectionWokeBeforeItLooksAgainOpusR1(t *testing.T) {
	dir := writePlugin(t, t.TempDir(), "slow", "@acme/slow", map[string]string{"worker.ts": strings.Join([]string{
		`import { constant, defineFacet, defineRegistry, local, str } from "@acme/app/v3";`,
		`const A = defineRegistry("acme.a", { version: 1 });`,
		`const B = defineRegistry("acme.b", { version: 1 });`,
		`const Reader = local("acme.reader", { version: 1, params: {}, members: { s: str() } });`,
		"export default defineFacet((host, self) => {",
		`	self.contribute(A, "a");`,
		"	host.serve(Reader, (_params, self) => {",
		"		self.registry(A);",
		"		self.open(() => new Promise((resolve) => setTimeout(resolve, 50)), () => {});",
		`		self.contribute(B, "b");`,
		`		return { s: constant("x") };`,
		"	});",
		"});",
	}, "\n")}, nil)
	tested := tpPlugin(t, dir, tpOpts(t, t.TempDir()))
	if got, want := tpContributed(t, tested, "acme.b"), `[["@acme/slow","b"]]`; got != want {
		t.Fatalf("acme.b = %s, want %s", got, want)
	}
}

// testing.test.ts › testPlugin › serves a registry contributed at a high version without an adapter per older version (Sol).
// Pi puts a bound of one second on the whole call. What it guards is that a registry at version 2000000 costs no adapter per
// older version: `olderAs` answers a lookup, it does not hold 1999999 of them. Here the call also builds the plugin and
// starts the Node subprocess (Pi's process is the test's own), and under CPU contention those alone take over a second at
// version 1 as well (0.8-1.2 s at both versions under -race on 4 CPUs shared with 6 busy loops; the bound failed 8 runs of 20),
// so the wall clock measured the process, not the version. The property is held where it is made, by what the adapters keep,
// which no scheduler changes: the registry is collected at its high version end to end, and `olderAs` of that version holds
// none of the adapters of the versions below it however many are looked up.
func TestTestPluginServesARegistryContributedAtAHighVersionWithoutAnAdapterPerOlderVersionSol(t *testing.T) {
	cache := t.TempDir()
	options := tpOpts(t, cache)
	dir := writePlugin(t, t.TempDir(), "high", "@acme/high", map[string]string{
		"worker.ts": "import { defineFacet, defineRegistry } from \"@acme/app/v3\";\nconst R = defineRegistry(\"acme.high\", { version: 2000000 });\nexport default defineFacet((_host, self) => self.contribute(R, 1));",
	}, nil)
	tested := tpPlugin(t, dir, options)
	if got, want := tpContributed(t, tested, "acme.high"), `[["@acme/high",1]]`; got != want {
		t.Fatalf("acme.high = %s, want %s", got, want)
	}
	table, err := tested.Contributions("acme.high", background)
	if err != nil {
		t.Fatal(err)
	}
	if len(table) != 1 || table[0].Version != 2000000 {
		t.Fatalf("acme.high = %+v, want its one contribution built against version 2000000", table)
	}

	tree, err := extractNode(cache)
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(node, "--input-type=module", "-e", `import { olderAs } from "./testing.mjs";
const accepts = olderAs({ version: 2000000 });
const answers = [1, 1999999, 2000000, 2000001].map((version) => typeof accepts[version]);
for (let version = 1; version < 1000000; version += 997) void accepts[version];
const adapter = accepts[1999999];
console.log(JSON.stringify({ held: Reflect.ownKeys(accepts).length, answers, adapted: [adapter.contribution("c"), adapter.value("v")] }));`)
	command.Dir = tree
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if got, want := strings.TrimSpace(string(out)), `{"held":0,"answers":["object","object","undefined","undefined"],"adapted":["c","v"]}`; got != want {
		t.Fatalf("olderAs(2000000) = %s, want %s (an adapter looked up and answered as it is, none held for a version below)", got, want)
	}
}

// testing.test.ts › testPlugin › takes no kind from test and declaration files (Opus N6).
func TestTestPluginTakesNoKindFromTestAndDeclarationFilesOpusN6(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"hello.worker.ts": "import { defineFacet } from \"@acme/app/v3\";\nexport default defineFacet(() => {});",
		"hello.test.ts":   "",
		"env.d.ts":        "",
	})
	tested := tpPlugin(t, filepath.Join(root, "hello.worker.ts"), tpOpts(t, t.TempDir()))
	var kinds []string
	for kind := range tested.Plugin.Facets {
		kinds = append(kinds, kind)
	}
	if !slices.Equal(kinds, []string{"worker"}) {
		t.Fatalf("facets = %v, want [worker]", kinds)
	}
}

// testing.ts evaluates the facet before it loads it (`evaluateFacet`): a script that throws while it evaluates rejects testPlugin
// with that error as it is, where a setup that throws is `testPlugin: setup failed: ...` (the case above). D502 had the first
// wrapped too; the evaluation's own error is returned, as a *NodeError of the Node host's throw.
func TestTestPluginRejectsAScriptThatThrowsWhileItEvaluatesWithItsOwnError(t *testing.T) {
	dir := writePlugin(t, t.TempDir(), "boom", "@acme/boom", map[string]string{
		"worker.ts": "import { defineFacet } from \"@acme/app/v3\";\nthrow new Error(\"boom\");\nexport default defineFacet(() => {});",
	}, nil)
	_, err := TestPlugin(dir, tpOpts(t, t.TempDir()), background)
	var thrown *NodeError
	if !errors.As(err, &thrown) || thrown.Message != "boom" || err.Error() != "boom" {
		t.Fatalf("evaluation: %v, want the evaluation's own *NodeError \"boom\"", err)
	}
}
