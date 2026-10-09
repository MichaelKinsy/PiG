package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// ledgerOutputs is everything a derivation writes: the full gap list, the rows that are not gaps, the derived mapping and the frontier.
type ledgerOutputs struct{ Gaps, NotGap, Mapping, Frontier string }

// renderOutputs applies the reviewed gaps and holds to the derivation and renders it as run does, without writing into the tree.
func renderOutputs(t *testing.T, root, version string, l *ledger, dv *derivation) ledgerOutputs {
	t.Helper()
	// The decisions are shared with the caller's derivation; copy them, because the reviewed inputs rewrite decisions in place.
	ds := make([]*decision, len(dv.Decisions))
	for i, d := range dv.Decisions {
		c := *d
		ds[i] = &c
	}
	if _, _, err := applyReviewInputs(root, ds, false, false); err != nil {
		t.Fatal(err)
	}
	mapping, _, _, err := derivedMapping(l, filepath.Join(root, "test/parity/interfaces", "mapping-v"+version+".json"), filepath.Join(root, reviewedDecisionsFile), ds, false)
	if err != nil {
		t.Fatal(err)
	}
	var ok strings.Builder
	for _, d := range ds {
		if !d.Gap {
			fmt.Fprintf(&ok, "%s\t%s\t%s\t%s\n", d.ID, d.Pkg, d.Candidate, d.Evidence)
		}
	}
	return ledgerOutputs{Gaps: gapTSV(ds, true), NotGap: ok.String(), Mapping: string(mapping), Frontier: frontierTSV(frontier(l, ds), 0)}
}

func diffOutputs(t *testing.T, what string, want, got ledgerOutputs) {
	t.Helper()
	for _, c := range []struct{ name, a, b string }{
		{"gaps", want.Gaps, got.Gaps}, {"not-a-gap", want.NotGap, got.NotGap}, {"mapping", want.Mapping, got.Mapping}, {"frontier", want.Frontier, got.Frontier},
	} {
		if c.a != c.b {
			t.Errorf("%s: %s differs from the derivation from scratch (%d vs %d bytes); first difference: %s", what, c.name, len(c.b), len(c.a), firstDifference(c.a, c.b))
		}
	}
}

// firstDifference names the first line at which two outputs differ.
func firstDifference(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < max(len(w), len(g)); i++ {
		switch {
		case i >= len(w):
			return fmt.Sprintf("line %d: extra %q", i+1, g[i])
		case i >= len(g):
			return fmt.Sprintf("line %d: missing %q", i+1, w[i])
		case w[i] != g[i]:
			return fmt.Sprintf("line %d: scratch %q, cached %q", i+1, w[i], g[i])
		}
	}
	return "none"
}

// TestCachedDerivationEqualsFull proves on the fixture module that a cache miss, a cache hit and a derivation from scratch write the same
// bytes, and that a stored derivation survives a JSON round trip with every field the sync reads.
func TestCachedDerivationEqualsFull(t *testing.T) {
	l, _ := fixtureLedger(t, nil, nil, nil, nil)
	root := fixtureLast.root
	o := options{root: root, version: "0.0.0"}
	reachOf := func() (map[string]bool, error) { return fxReach, nil }
	_, full, err := derive(root, o, l, reachOf)
	if err != nil {
		t.Fatal(err)
	}
	want := renderOutputs(t, root, "0.0.0", l, full)
	if want.Mapping == "" || want.Gaps == "" && want.NotGap == "" {
		t.Fatal("the fixture rendered nothing")
	}
	cache, err := openCache(t.TempDir(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	miss, err := cachedDerive(cache, root, o, l, reachOf)
	if err != nil {
		t.Fatal(err)
	}
	diffOutputs(t, "cache miss", want, renderOutputs(t, root, "0.0.0", l, miss))
	calls := 0
	hit, err := cachedDerive(cache, root, o, l, func() (map[string]bool, error) { calls++; return fxReach, nil })
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("a cache hit derived again (%d reach loads)", calls)
	}
	diffOutputs(t, "cache hit", want, renderOutputs(t, root, "0.0.0", l, hit))
}

// TestInputKeyTracksEveryInput proves the derivation key changes with each file the derivation reads and not with a file it does not read.
func TestInputKeyTracksEveryInput(t *testing.T) {
	fixtureLedger(t, nil, nil, nil, nil)
	root := fixtureLast.root
	key := func() string {
		k, err := inputKey(root, "0.0.0")
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	base := key()
	if key() != base {
		t.Fatal("the key is not stable")
	}
	for _, rel := range []string{"README.md", "docs/notes.md", "test/parity/interface-closure/gaps/interface-gaps-v0.0.0.tsv", "changelog.d/x.md"} {
		write(rel, "not an input")
		if key() != base {
			t.Errorf("%s is not an input of the derivation but changed the key", rel)
		}
	}
	for _, rel := range []string{
		"lib/lib_test.go", "lib/extra.go", "go.mod", "docs/parity/PORT_MAP.md", "test/parity/behavior-contracts.toml",
		"test/parity/interfaces/upstream-v0.0.0.json", "test/parity/interface-closure/autobind/renames.json",
		"test/parity/interface-closure/autobind/holds.json", ".upstream/current/packages/fx/src/types.ts",
	} {
		before := key()
		write(rel, fmt.Sprintf("changed %s", rel))
		if key() == before {
			t.Errorf("%s is an input of the derivation but did not change the key", rel)
		}
	}
}

// TestInputKeyFollowsTheMirrorContent proves that a mirror repaired in place under the same version name changes the key: the key hashes the
// content behind .upstream/current, not its link target.
func TestInputKeyFollowsTheMirrorContent(t *testing.T) {
	root := t.TempDir()
	mirror := filepath.Join(t.TempDir(), "v9.9.9")
	file := filepath.Join(mirror, "packages", "fx", "src", "types.ts")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".upstream"), 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.RequireDirectoryLink(t, mirror, filepath.Join(root, ".upstream", "current"))
	key := func() string {
		k, err := inputKey(root, "9.9.9")
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("export type A = string;\n")
	before := key()
	write("export type A = number;\n")
	if key() == before {
		t.Error("a mirror repaired in place did not change the key")
	}
	write("export type A = string;\n")
	if key() != before {
		t.Error("the key is not a function of the mirror content")
	}
}

// TestReachKeyIgnoresTests proves the reach key follows the source the binary links: a test file or an unlinked package does not change
// it, a linked file does.
func TestReachKeyIgnoresTests(t *testing.T) {
	fixtureLedger(t, nil, nil, nil, nil)
	root := fixtureLast.root
	key := func() string {
		k, err := reachKey(root, nil, []string{"./lib"})
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	base := key()
	write("lib/lib_test.go", "package lib\n")
	write("other/other.go", "package other\n")
	write("docs/x.md", "x")
	if key() != base {
		t.Error("a test file, an unlinked package or a document changed the reach key")
	}
	write("lib/use.go", fxUse+"\nfunc Added() {}\n")
	if key() == base {
		t.Error("a linked source file did not change the reach key")
	}
	changed := key()
	write("go.mod", "module fixture\n\ngo 1.26\n\n// edited\n")
	if key() == changed {
		t.Error("go.mod did not change the reach key")
	}
}

func TestCacheEntriesArePublishedWhole(t *testing.T) {
	cache, err := openCache(t.TempDir(), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.loadReach("deadbeef"); ok {
		t.Fatal("a missing entry loaded")
	}
	if err := cache.storeReach("deadbeef", []byte(`["a.go#A"]`)); err != nil {
		t.Fatal(err)
	}
	if err := cache.storeReach("deadbeef", []byte(`["a.go#A"]`)); err != nil {
		t.Fatalf("publishing an existing entry must succeed: %v", err)
	}
	if list, ok := cache.loadReach("deadbeef"); !ok || len(list) != 1 || list[0] != "a.go#A" {
		t.Fatalf("round trip: %v %v", list, ok)
	}
	entries, _ := os.ReadDir(cache.dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Errorf("a scratch directory %s was left behind", e.Name())
		}
	}
}

// TestFrontierRanksRootGaps proves the ranking on the fixture: a gap under a closed-looking parent is ranked by the child-gap rows it holds
// open, and an owner gap by the members it hides.
func TestFrontierRanksRootGaps(t *testing.T) {
	cfg := "pkg:fx/.#Config"
	l := &ledger{byID: map[string]*upstreamEntry{}, children: map[string][]*upstreamEntry{}}
	add := func(id, parent string) {
		e := &upstreamEntry{ID: id, ParentID: parent, SourcePath: "node_modules/p/dist/x.d.ts", SourceLine: 7}
		l.byID[id] = e
		l.children[parent] = append(l.children[parent], e)
	}
	add(cfg, "")
	add(cfg+"::property:a", cfg)
	add(cfg+"::property:b", cfg)
	add(cfg+"::property:a::call:0", cfg+"::property:a")
	ds := []*decision{
		{ID: cfg, Pkg: "fx", Gap: true, Reason: reasonChild},
		{ID: cfg + "::property:a", Pkg: "fx", Gap: true, Reason: reasonChild},
		{ID: cfg + "::property:a::call:0", Pkg: "fx", Gap: true, Reason: "signature-mismatch", Candidate: "lib.go#Config.Run"},
		{ID: cfg + "::property:b", Pkg: "fx", Gap: true, Reason: "member-missing"},
	}
	rows := frontier(l, ds)
	if len(rows) != 2 {
		t.Fatalf("rows: %+v", rows)
	}
	// The call row blocks its property (alone) and the declaration (shared with b). b blocks only the declaration.
	if r := rows[0]; r.ID != cfg+"::property:a::call:0" || r.Alone != 1 || r.Total != 2 || r.Kind != "signature-mismatch" || r.Source != "node_modules/p/dist/x.d.ts:7" {
		t.Errorf("first row: %+v", r)
	}
	if r := rows[1]; r.ID != cfg+"::property:b" || r.Alone != 0 || r.Total != 1 {
		t.Errorf("second row: %+v", r)
	}
}

// TestCacheEquivalenceOnLaneBranches checks, on real lane branches, that the cached derivation equals the derivation from scratch. It is
// opt-in because each branch needs two full derivations: set LEDGER_EQUIV_BRANCHES to a space-separated list of refs (or run
// `make ledger-equivalence BRANCHES="a b c"`). For each branch it derives the base ref (LEDGER_EQUIV_BASE, default HEAD)
// into an empty cache, derives the branch from the cache (a miss that reuses whatever reach entry matches), derives it again (a hit), and
// compares both with a derivation from scratch of the branch.
func TestCacheEquivalenceOnLaneBranches(t *testing.T) {
	refs := strings.Fields(os.Getenv("LEDGER_EQUIV_BRANCHES"))
	if len(refs) == 0 {
		t.Skip("set LEDGER_EQUIV_BRANCHES to lane branch refs (make ledger-equivalence BRANCHES=...)")
	}
	repo, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	base := os.Getenv("LEDGER_EQUIV_BASE")
	if base == "" {
		base = "HEAD"
	}
	// Remote-tracking refs move while the test runs (any fetch in the shared clone advances a lane branch), and each derivation checks the
	// ref out again, so resolve every ref to its commit once.
	resolve := func(ref string) string {
		out, err := exec.Command("git", "-C", repo, "rev-parse", "--verify", ref+"^{commit}").Output()
		if err != nil {
			t.Fatalf("resolve %s: %v", ref, err)
		}
		return strings.TrimSpace(string(out))
	}
	base = resolve(base)
	names := slices.Clone(refs)
	for i := range refs {
		refs[i] = resolve(refs[i])
	}
	cache, err := openCache(t.TempDir(), repo, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	derive1 := func(t *testing.T, ref string, useCache bool, pass string) ledgerOutputs {
		tree := checkoutTree(t, repo, ref)
		t.Chdir(tree) // the PORT_MAP placement rule reads the table of the working directory's module
		o := options{root: tree, version: pigversion.UpstreamVersion}
		l, err := loadLedger(tree, o.version)
		if err != nil {
			t.Fatal(err)
		}
		var dv *derivation
		if useCache {
			dv, err = cachedDerive(cache, tree, o, l, func() (map[string]bool, error) { return cachedReach(cache, tree, o) })
		} else {
			_, dv, err = derive(tree, o, l, func() (map[string]bool, error) { return loadReach(tree, "") })
		}
		if err != nil {
			t.Fatalf("%s (%s): %v", ref, pass, err)
		}
		return renderOutputs(t, tree, o.version, l, dv)
	}
	derive1(t, base, true, "base")
	for i, ref := range refs {
		t.Run(names[i], func(t *testing.T) {
			full := derive1(t, ref, false, "scratch")
			if len(full.Mapping) == 0 || len(full.Gaps) == 0 {
				t.Fatal("empty derivation")
			}
			diffOutputs(t, "second derivation from scratch", full, derive1(t, ref, false, "scratch again"))
			diffOutputs(t, "cache miss", full, derive1(t, ref, true, "miss"))
			diffOutputs(t, "cache hit", full, derive1(t, ref, true, "hit"))
		})
	}
}

// checkoutTree adds a detached worktree of ref to a scratch directory and links the ignored upstream mirror into it.
func checkoutTree(t *testing.T, repo, ref string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "tree")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("worktree", "add", "--detach", dir, ref)
	t.Cleanup(func() { run("worktree", "remove", "--force", dir) })
	testenv.RequireDirectoryLink(t, filepath.Join(repo, ".upstream"), filepath.Join(dir, ".upstream"))
	return dir
}

// TestFrontierLocatesPiSource proves the .d.ts declaration maps to the pinned source: the member line after its owner's declaration, the
// owner's line for a member the source does not name, and blank for a file or declaration the source lacks.
func TestFrontierLocatesPiSource(t *testing.T) {
	root := t.TempDir()
	src := "import x from \"y\";\n\nexport interface Config {\n\tname: string;\n\tretries: number;\n}\n\nexport class Models {\n\tgetAuth(): void {}\n}\n\nexport class Sub extends Models {\n}\n\nclass Impl {\n\tgetAuth(): void {}\n}\n"
	path := filepath.Join(root, ".upstream", "current", "packages", "fx", "src", "api", "models.ts")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s := piSources{root: root, files: map[string][]string{}}
	entry := func(id string) *upstreamEntry {
		return &upstreamEntry{ID: id, SourcePath: "node_modules/@x/pi-fx/dist/api/models.d.ts", SourceLine: 3}
	}
	for _, c := range []struct{ id, want string }{
		{"pkg:fx/.#Config::property:retries", "packages/fx/src/api/models.ts:5"},
		{"pkg:fx/.#Models::property:getAuth::call:1", "packages/fx/src/api/models.ts:9"},
		{"pkg:fx/.#Config::property:absent", "packages/fx/src/api/models.ts:3"},
		{"pkg:fx/.#Models", "packages/fx/src/api/models.ts:8"},
		// An inherited member is not named in the owner's block: the owner's declaration, not a later class that names it.
		{"pkg:fx/.#Sub::property:getAuth", "packages/fx/src/api/models.ts:12"},
		{"pkg:fx/.#Missing", ""},
	} {
		if got := s.locate(entry(c.id), "fx"); got != c.want {
			t.Errorf("%s: %q, want %q", c.id, got, c.want)
		}
	}
	// A source path the inventory records directly is returned with its recorded line.
	if got := s.locate(&upstreamEntry{ID: "pkg:fx/.#Config", SourcePath: "packages/fx/src/api/models.ts", SourceLine: 12}, "fx"); got != "packages/fx/src/api/models.ts:12" {
		t.Errorf("recorded source path: %q", got)
	}
	if got := s.locate(&upstreamEntry{ID: "pkg:fx/.#Config", SourcePath: "node_modules/@x/pi-fx/dist/gone.d.ts"}, "fx"); got != "" {
		t.Errorf("missing source file: %q", got)
	}
}

// TestSuppliedReachDoesNotShareTheTreeKey proves that a derivation run with -reach <file> neither reads nor publishes the entry of the
// tree's own reach graph: the decisions depend on the reach graph, so a stale or hand-edited reach file stored under the tree key would
// hand every later lane with the same tree a derivation over the wrong graph.
func TestSuppliedReachDoesNotShareTheTreeKey(t *testing.T) {
	l, _ := fixtureLedger(t, nil, nil, nil, nil)
	root := fixtureLast.root
	cache, err := openCache(t.TempDir(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	plain := options{root: root, version: "0.0.0"}
	if _, err := cachedDerive(cache, root, plain, l, func() (map[string]bool, error) { return fxReach, nil }); err != nil {
		t.Fatal(err)
	}
	reachFile := filepath.Join(t.TempDir(), "reach.json")
	if err := os.WriteFile(reachFile, []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	supplied := options{root: root, version: "0.0.0", reach: reachFile}
	calls := 0
	if _, err := cachedDerive(cache, root, supplied, l, func() (map[string]bool, error) { calls++; return map[string]bool{}, nil }); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("a derivation over a supplied reach file read the tree's cached entry (%d reach loads, want 1)", calls)
	}
	treeKey, err := inputKey(root, "0.0.0")
	if err != nil {
		t.Fatal(err)
	}
	suppliedKey, err := derivationKey(root, supplied)
	if err != nil {
		t.Fatal(err)
	}
	if suppliedKey == treeKey {
		t.Fatal("a supplied reach file derives under the tree's key")
	}
	if err := os.WriteFile(reachFile, []byte(`["x"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if changed, err := derivationKey(root, supplied); err != nil || changed == suppliedKey {
		t.Fatalf("the key does not follow the reach file's content (%v)", err)
	}
}

// TestInputKeyTracksGitIgnoredGoSources proves that a Go source the go command loads under a git-ignored directory (a scratch tool under
// build/ or tmp/) and a workspace module's go.mod are inputs of the derivation key: `./...` does not read .gitignore, so a tree with such a
// file must not hit the entry of a tree without it. A file under a directory the go command skips (".x", "_x", testdata) is not an input.
func TestInputKeyTracksGitIgnoredGoSources(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	fixtureLedger(t, nil, nil, nil, nil)
	root := fixtureLast.root
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	write(".gitignore", "/build/\ntmp/\nbin/\n")
	key := func() string {
		t.Helper()
		k, err := inputKey(root, "0.0.0")
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	base := key()
	for _, rel := range []string{"build/notes.txt", "tmp/.scratch/x.go", "tmp/_old/x.go", "tmp/testdata/x.go"} {
		write(rel, "package x\n")
		if key() != base {
			t.Errorf("%s is not loaded by the go command but changed the key", rel)
		}
	}
	for _, rel := range []string{"build/revtool/main.go", "tmp/overlay/lib_test.go", "lib/bin/tool.go", "sub/go.mod"} {
		before := key()
		write(rel, "package x\n// "+rel+"\n")
		if key() == before {
			t.Errorf("%s is loaded by the go command (or selects its module versions) but did not change the key", rel)
		}
	}
}
