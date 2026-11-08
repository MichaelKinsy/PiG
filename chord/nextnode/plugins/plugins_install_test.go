package plugins

// pi: packages/chord/src/next-node/plugins/install.ts

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Ports packages/chord/test/next-node/plugins/install.test.ts. Tests of installing (slice 7c §5, slice 12 §2, §5): the npm
// arguments, run in the plugin's real directory, the command overridable; a failure and an abort reject; git dependencies
// with a ref, a plugin inside a repository of several reached by path; plugins installed from git and npm (a package's
// chord.plugins manifest, the refusals, claims and cleanup). Git repositories and tarballs are local: no network.

// fakeNpm is a fake npm (a Node script, as upstream's is) that records its directory and arguments, one per line, in
// dir/record.txt, and then exits with code, or, with hang, waits to be stopped.
func fakeNpm(t *testing.T, dir string, code int, hang bool) []string {
	t.Helper()
	script := filepath.Join(dir, "fake-npm.mjs")
	lines := []string{
		`import { writeFileSync } from "node:fs";`,
		`import { join } from "node:path";`,
		`writeFileSync(join(import.meta.dirname, "record.txt"), [process.cwd(), ...process.argv.slice(2)].join("\n") + "\n");`,
	}
	if hang {
		lines = append(lines, "setTimeout(() => {}, 30000);")
	} else {
		if code != 0 {
			lines = append(lines, `console.error("npm said no");`)
		}
		lines = append(lines, fmt.Sprintf("process.exit(%d);", code))
	}
	writeFiles(t, dir, map[string]string{"fake-npm.mjs": strings.Join(lines, "\n") + "\n"})
	return []string{pcNode(t), script}
}

func TestInstallDependenciesRunsNpmInstallWithoutDevDependenciesPeersScriptsAuditOrFundingInThePluginsRealDirectory(t *testing.T) {
	tools := tempRoot(t)
	elsewhere := tempRoot(t)
	root := tempRoot(t)
	dir := writePlugin(t, elsewhere, "deps", "@test/deps", map[string]string{"worker.ts": ""}, map[string]any{
		"dependencies": map[string]any{"a": "1.0.0"},
	})
	testenv.RequireDirectoryLink(t, dir, filepath.Join(root, "deps"))
	discovery, err := DiscoverPlugins(plainRoots(root), DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		t.Fatal(err)
	}
	if err := InstallDependencies(discovery.Plugins[0], background, InstallOptions{NpmCommand: fakeNpm(t, tools, 0, false)}); err != nil {
		t.Fatal(err)
	}
	record, err := os.ReadFile(filepath.Join(tools, "record.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{realOf(t, dir), "install", "--omit=dev", "--legacy-peer-deps", "--ignore-scripts", "--no-audit", "--no-fund"}
	if got := strings.Split(strings.TrimSuffix(string(record), "\n"), "\n"); !reflect.DeepEqual(got, want) {
		t.Fatalf("record = %q, want %q", got, want)
	}
}

func TestInstallDependenciesRejectsWithTheCommandsOutputWhenItFailsAndWhenTheContextAborts(t *testing.T) {
	tools := tempRoot(t)
	root := tempRoot(t)
	writePlugin(t, root, "deps", "@test/deps", map[string]string{}, nil)
	discovery, err := DiscoverPlugins(plainRoots(root), DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		t.Fatal(err)
	}
	plugin := discovery.Plugins[0]
	err = InstallDependencies(plugin, background, InstallOptions{NpmCommand: fakeNpm(t, tools, 1, false)})
	if err == nil || !regexp.MustCompile(`^plugin "@test/deps": .* install failed \(exit 1\): npm said no$`).MatchString(err.Error()) {
		t.Fatalf("error = %v", err)
	}
	aborted, cancel := chordctx.WithCancel(background)
	stop := errors.New("stop")
	time.AfterFunc(50*time.Millisecond, func() { cancel(stop) })
	err = InstallDependencies(plugin, aborted, InstallOptions{NpmCommand: fakeNpm(t, tools, 0, true)})
	if !errors.Is(err, stop) {
		t.Fatalf("error = %v, want the abort's reason %v", err, stop)
	}
}

func TestInstallDependenciesRefusesAnEmptyCommandAndAnAbortedContextBeforeRunningAnything(t *testing.T) {
	root := tempRoot(t)
	writePlugin(t, root, "deps", "@test/deps", map[string]string{}, nil)
	discovery, err := DiscoverPlugins(plainRoots(root), DiscoveryOptions{Kinds: kinds}, background)
	if err != nil {
		t.Fatal(err)
	}
	plugin := discovery.Plugins[0]
	err = InstallDependencies(plugin, background, InstallOptions{NpmCommand: []string{}})
	if err == nil || err.Error() != "npmCommand is empty" {
		t.Fatalf("error = %v", err)
	}
	aborted, cancel := chordctx.WithCancel(background)
	stop := errors.New("stop")
	cancel(stop)
	tools := tempRoot(t)
	err = InstallDependencies(plugin, aborted, InstallOptions{NpmCommand: fakeNpm(t, tools, 0, false)})
	if !errors.Is(err, stop) {
		t.Fatalf("error = %v, want the abort's reason", err)
	}
	if _, statErr := os.Stat(filepath.Join(tools, "record.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("the command ran: %v", statErr)
	}
}

// pig divergence (D267), not Pi's: Pi's install waits for the `close` of the command, the end of its output, so a process the command started that
// inherited the pipes holds the install for as long as it lives. Here the fake npm starts a sleeping Node that inherits them
// and exits with code 0 (then with code 1): the install ends with the command, within the stop grace of its exit, with the
// result of its exit code and the output it wrote.
func TestInstallDependenciesEndsWithTheCommandThoughAProcessItStartedHoldsItsPipes(t *testing.T) {
	for _, code := range []int{0, 1} {
		t.Run(fmt.Sprintf("exit%d", code), func(t *testing.T) {
			tools := tempRoot(t)
			root := tempRoot(t)
			writePlugin(t, root, "deps", "@test/deps", map[string]string{}, nil)
			discovery, err := DiscoverPlugins(plainRoots(root), DiscoveryOptions{Kinds: kinds}, background)
			if err != nil {
				t.Fatal(err)
			}
			script := filepath.Join(tools, "fake-npm.mjs")
			writeFiles(t, tools, map[string]string{"fake-npm.mjs": strings.Join([]string{
				`import { spawn } from "node:child_process";`,
				`import { writeFileSync } from "node:fs";`,
				`import { join } from "node:path";`,
				`const child = spawn(process.execPath, ["-e", "setTimeout(() => {}, 60000)"], { stdio: ["ignore", "inherit", "inherit"], detached: true });`,
				`child.unref();`,
				`writeFileSync(join(import.meta.dirname, "held.pid"), String(child.pid));`,
				`console.error("npm output");`,
				fmt.Sprintf("process.exit(%d);", code),
			}, "\n") + "\n"})
			t.Cleanup(func() {
				if raw, err := os.ReadFile(filepath.Join(tools, "held.pid")); err == nil {
					var pid int
					if _, err := fmt.Sscan(string(raw), &pid); err == nil {
						if process, err := os.FindProcess(pid); err == nil {
							_ = process.Kill()
						}
					}
				}
			})
			done := make(chan error, 1)
			go func() {
				done <- InstallDependencies(discovery.Plugins[0], background, InstallOptions{NpmCommand: []string{pcNode(t), script}})
			}()
			select {
			case err := <-done:
				if code == 0 && err != nil {
					t.Fatalf("install = %v, want nil: the command exited with code 0", err)
				}
				if code != 0 && (err == nil || !strings.Contains(err.Error(), "(exit 1): npm output")) {
					t.Fatalf("install = %v, want the failure of exit 1 with the command's output", err)
				}
			case <-time.After(25 * time.Second):
				t.Fatal("the install waited for what holds the command's pipes")
			}
		})
	}
}

// allowGitDependencies lets the npm of the machine install git dependencies: npm 12 refuses them unless allowed (the
// tests' dependencies are local repositories).
func allowGitDependencies(t *testing.T) {
	t.Helper()
	t.Setenv("npm_config_allow_git", "all")
}

// workerOf is the worker facet of plugin name under root, built for Node with Chord as the host package.
func workerOf(t *testing.T, root, name string) string {
	t.Helper()
	discovery := discovered(t, plainRoots(root))
	for _, plugin := range discovery.Plugins {
		if plugin.Name != name {
			continue
		}
		built, err := BuildFacet(plugin, "worker", BuildOptions{HostPackages: []string{"@earendil-works/chord"}, Platform: PlatformNode, Plugins: discovery.Plugins}, background)
		if err != nil {
			t.Fatal(err)
		}
		if built.Kind == BuiltFacetFailed {
			t.Fatal(strings.Join(built.Errors, "\n"))
		}
		return built.Source
	}
	t.Fatalf("no plugin %q", name)
	return ""
}

// notesPackage is a contracts package `@bob/notes` whose contract says label.
func notesPackage(t *testing.T, label string) map[string]string {
	t.Helper()
	quoted, err := json.Marshal(label)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"package.json": manifestText(t, map[string]any{"name": "@bob/notes", "version": "1.0.0", "exports": map[string]any{".": "./contract.ts"}}),
		"contract.ts":  "export const label = " + string(quoted) + ";",
	}
}

// listing is the names directly under dir, sorted.
func listing(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// recordedDir is the directory the fake npm of tools ran in (the first line of its record).
func recordedDir(t *testing.T, tools string) string {
	t.Helper()
	record, err := os.ReadFile(filepath.Join(tools, "record.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.SplitN(string(record), "\n", 2)[0]
}

// Not upstream's: a single-file plugin has no package.json (installDependencies's first line).
func TestInstallDependenciesRefusesASingleFilePlugin(t *testing.T) {
	err := InstallDependencies(Plugin{Name: "hello", Layout: LayoutFile}, background, InstallOptions{})
	if err == nil || err.Error() != `plugin "hello": a single-file plugin has no package.json` {
		t.Fatalf("error = %v", err)
	}
}

func TestInstallDependenciesGitDependenciesInstallsAGitDependencyPinnedToACommitWithNpmTheBuildInlinesThatCommitsFile(t *testing.T) {
	allowGitDependencies(t)
	first := gitRepository(t, "notes", notesPackage(t, "notes at v1"), "v1", nil)
	gitRepository(t, "notes", notesPackage(t, "notes at v2"), "v2", &first)
	root := tempRoot(t)
	writePlugin(t, root, "stats", "@test/stats",
		map[string]string{"worker.ts": "import { label } from \"@bob/notes\";\nexport default { setup: () => label };"},
		map[string]any{"dependencies": map[string]any{"@bob/notes": "git+" + first.url + "#" + first.commit}})
	discovery := discovered(t, plainRoots(root))
	if err := InstallDependencies(discovery.Plugins[0], background, InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	source := workerOf(t, root, "@test/stats")
	if !strings.Contains(source, "notes at v1") || strings.Contains(source, "notes at v2") {
		t.Fatalf("source = %s", source)
	}
}

func TestInstallDependenciesGitDependenciesReachesAPluginInsideAMultiPluginRepositoryThroughADependencyOnTheWholeRepositoryByPath(t *testing.T) {
	allowGitDependencies(t)
	repository := gitRepository(t, "plugins", map[string]string{
		"package.json":       manifestText(t, map[string]any{"name": "@bob/plugins", "version": "1.0.0", "chord": map[string]any{"plugins": []any{"notes"}}}),
		"notes/package.json": manifestText(t, map[string]any{"name": "@bob/notes"}),
		"notes/contract.ts":  `export const label = "notes inside a repository of several";`,
	}, "v1", nil)
	root := tempRoot(t)
	writePlugin(t, root, "stats", "@test/stats",
		map[string]string{"worker.ts": "import { label } from \"@bob/plugins/notes/contract.ts\";\nexport default { setup: () => label };"},
		map[string]any{"dependencies": map[string]any{"@bob/plugins": "git+" + repository.url + "#" + repository.commit}})
	discovery := discovered(t, plainRoots(root))
	if err := InstallDependencies(discovery.Plugins[0], background, InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if source := workerOf(t, root, "@test/stats"); !strings.Contains(source, "notes inside a repository of several") {
		t.Fatalf("source = %s", source)
	}
}

func TestInstallPluginClonesARepositoryWhoseTopIsAPluginIntoRootRepositoryAtATagAndInstallsItsDependencies(t *testing.T) {
	repository := gitRepository(t, "hello", map[string]string{
		"package.json": manifestText(t, map[string]any{"name": "@bob/hello"}),
		"worker.ts":    "export default { setup: () => {} };",
	}, "v1", nil)
	root := tempRoot(t)
	tools := tempRoot(t)
	options := InstallPluginOptions{InstallOptions: InstallOptions{NpmCommand: fakeNpm(t, tools, 0, false)}}
	names, err := InstallPlugin(PluginSource{Git: repository.url, Ref: "v1"}, root, background, options)
	if err != nil || !reflect.DeepEqual(names, []string{"@bob/hello"}) {
		t.Fatalf("names = %v, error = %v", names, err)
	}
	if got, want := recordedDir(t, tools), realOf(t, filepath.Join(root, "hello")); got != want {
		t.Fatalf("npm ran in %q, want %q", got, want)
	}
	// No updating in place.
	_, err = InstallPlugin(PluginSource{Git: repository.url, Ref: "v1"}, root, background, options)
	if err == nil || err.Error() != "installPlugin: "+filepath.Join(root, "hello")+" exists already" {
		t.Fatalf("error = %v", err)
	}
}

func TestInstallPluginClonesARepositoryOfSeveralPluginsItsSubdirectoriesPinnedToACommit(t *testing.T) {
	first := gitRepository(t, "bob-plugins", map[string]string{
		"notes/package.json":  manifestText(t, map[string]any{"name": "@bob/notes"}),
		"search/package.json": manifestText(t, map[string]any{"name": "@bob/search"}),
	}, "v1", nil)
	gitRepository(t, "bob-plugins", map[string]string{"later/package.json": manifestText(t, map[string]any{"name": "@bob/later"})}, "v2", &first)
	root := tempRoot(t)
	tools := tempRoot(t)
	names, err := InstallPlugin(PluginSource{Git: first.url, Ref: first.commit}, root, background, InstallPluginOptions{InstallOptions: InstallOptions{NpmCommand: fakeNpm(t, tools, 0, false)}})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(names)
	if want := []string{"@bob/notes", "@bob/search"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	if got := pluginNames(discovered(t, plainRoots(root)).Plugins); !reflect.DeepEqual(got, []string{"@bob/notes", "@bob/search"}) {
		t.Fatalf("plugins = %v", got)
	}
}

func TestInstallPluginInstallsAnNpmPackageHereALocalTarballIntoRootName(t *testing.T) {
	source := tempRoot(t)
	writeFiles(t, source, map[string]string{
		"package.json": manifestText(t, map[string]any{"name": "@bob/clock", "version": "1.2.0"}),
		"worker.ts":    "export default { setup: () => {} };",
	})
	pack := exec.Command("npm", "pack", "--silent", "--pack-destination", source)
	pack.Dir = source
	out, err := pack.Output()
	if err != nil {
		t.Fatal(err)
	}
	packed := strings.TrimSpace(string(out))
	root := tempRoot(t)
	names, err := InstallPlugin(PluginSource{Npm: "@bob/clock", Version: "file:" + filepath.Join(source, packed)}, root, background, InstallPluginOptions{})
	if err != nil || !reflect.DeepEqual(names, []string{"@bob/clock"}) {
		t.Fatalf("names = %v, error = %v", names, err)
	}
	text, err := os.ReadFile(filepath.Join(root, "@bob", "clock", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var installed struct{ Version string }
	if err := json.Unmarshal(text, &installed); err != nil || installed.Version != "1.2.0" {
		t.Fatalf("package.json = %s (%v)", text, err)
	}
}

func TestInstallPluginInstallsExactlyTheFoldersAPackagesChordPluginsLists(t *testing.T) {
	repository := gitRepository(t, "bob-plugins", map[string]string{
		"package.json": manifestText(t, map[string]any{
			"name":  "@bob/plugins",
			"chord": map[string]any{"plugins": []any{"plugins/*", "!plugins/draft"}},
		}),
		"plugins/notes/package.json": manifestText(t, map[string]any{"name": "@bob/notes"}),
		"plugins/draft/package.json": manifestText(t, map[string]any{"name": "@bob/draft"}),
		"tools/package.json":         manifestText(t, map[string]any{"name": "@bob/tools"}),
	}, "v1", nil)
	root := tempRoot(t)
	tools := tempRoot(t)
	names, err := InstallPlugin(PluginSource{Git: repository.url, Ref: "v1"}, root, background, InstallPluginOptions{InstallOptions: InstallOptions{NpmCommand: fakeNpm(t, tools, 0, false)}})
	if err != nil || !reflect.DeepEqual(names, []string{"@bob/notes"}) {
		t.Fatalf("names = %v, error = %v", names, err)
	}
}

func TestInstallPluginRefusesARefARepositoryAndAPackageNameAnOptionCouldHideInSolP1(t *testing.T) {
	repository := gitRepository(t, "hello", map[string]string{"package.json": manifestText(t, map[string]any{"name": "@bob/hello"})}, "v1", nil)
	root := tempRoot(t)
	marker := filepath.Join(filepath.Dir(root), "upload-pack-ran")
	pattern := regexp.MustCompile(`^installPlugin: .* (is refused|is not an npm package name)$`)
	for _, source := range []PluginSource{
		{Git: repository.url, Ref: "--upload-pack=touch " + marker},
		{Git: "--upload-pack=x", Ref: "v1"},
		{Npm: "--registry=http://evil", Version: "1.0.0"},
		{Npm: "x", Version: "--foreground-scripts"},
		{Npm: "../escaped", Version: "1.0.0"},
	} {
		_, err := InstallPlugin(source, root, background, InstallPluginOptions{})
		if err == nil || !pattern.MatchString(err.Error()) {
			t.Errorf("%+v: error = %v", source, err)
		}
	}
	if names := listing(t, root); len(names) != 0 {
		t.Fatalf("root = %v", names)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the upload pack ran: %v", err)
	}
}

func TestInstallPluginRefusesAPluginNameAnotherRootHasAndLeavesNothingBehindOpusM4(t *testing.T) {
	first := tempRoot(t)
	root := tempRoot(t)
	writePlugin(t, first, "zeta", "@test/same", map[string]string{}, nil)
	repository := gitRepository(t, "alpha", map[string]string{"package.json": manifestText(t, map[string]any{"name": "@test/same"})}, "v1", nil)
	_, err := InstallPlugin(PluginSource{Git: repository.url, Ref: "v1"}, root, background, InstallPluginOptions{Roots: plainRoots(first, root)})
	if want := `installPlugin: plugin "@test/same" is in ` + filepath.Join(realOf(t, first), "zeta") + " already"; err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %s", err, want)
	}
	if names := listing(t, root); len(names) != 0 {
		t.Fatalf("root = %v", names)
	}
}

func TestInstallPluginClaimsItsDirectoryFirstOfTwoConcurrentInstallsOneWinsTheOtherRemovesNothingOfItSol(t *testing.T) {
	repository := gitRepository(t, "hello", map[string]string{"package.json": manifestText(t, map[string]any{"name": "@bob/hello"})}, "v1", nil)
	root := tempRoot(t)
	tools := tempRoot(t)
	options := InstallPluginOptions{InstallOptions: InstallOptions{NpmCommand: fakeNpm(t, tools, 0, false)}}
	var group sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		group.Go(func() {
			_, errs[i] = InstallPlugin(PluginSource{Git: repository.url, Ref: "v1"}, root, background, options)
		})
	}
	group.Wait()
	failed := []error{}
	for _, err := range errs {
		if err != nil {
			failed = append(failed, err)
		}
	}
	if len(failed) != 1 || failed[0].Error() != "installPlugin: "+filepath.Join(root, "hello")+" exists already" {
		t.Fatalf("errors = %v", errs)
	}
	if !slices.Contains(listing(t, filepath.Join(root, "hello")), "package.json") {
		t.Fatalf("hello = %v", listing(t, filepath.Join(root, "hello")))
	}
}

func TestInstallPluginRemovesTheScopeDirectoryItMadeWhenAScopedNpmInstallFailsOpusL2(t *testing.T) {
	root := tempRoot(t)
	tools := tempRoot(t)
	_, err := InstallPlugin(PluginSource{Npm: "@bob/notes", Version: "1.0.0"}, root, background, InstallPluginOptions{InstallOptions: InstallOptions{NpmCommand: fakeNpm(t, tools, 1, false)}})
	if err == nil || !strings.Contains(err.Error(), "pack @bob/notes@1.0.0 failed") {
		t.Fatalf("error = %v", err)
	}
	if names := listing(t, root); len(names) != 0 {
		t.Fatalf("root = %v", names)
	}
}

func TestInstallPluginRemovesStagingDirectoriesAnInstallCrashedOutOfOnceAnHourOldOpusN6(t *testing.T) {
	repository := gitRepository(t, "hello", map[string]string{"package.json": manifestText(t, map[string]any{"name": "@bob/hello"})}, "v1", nil)
	root := tempRoot(t)
	for _, name := range []string{".chord-install-old", ".chord-install-recent"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	hourAgo := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(filepath.Join(root, ".chord-install-old"), hourAgo, hourAgo); err != nil {
		t.Fatal(err)
	}
	tools := tempRoot(t)
	if _, err := InstallPlugin(PluginSource{Git: repository.url, Ref: "v1"}, root, background, InstallPluginOptions{InstallOptions: InstallOptions{NpmCommand: fakeNpm(t, tools, 0, false)}}); err != nil {
		t.Fatal(err)
	}
	if got, want := listing(t, root), []string{".chord-install-recent", "hello"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("root = %v, want %v", got, want)
	}
}

func TestInstallPluginRemovesWhatArrivedWhenItHoldsNoPlugin(t *testing.T) {
	repository := gitRepository(t, "empty", map[string]string{"readme.md": ""}, "v1", nil)
	root := tempRoot(t)
	_, err := InstallPlugin(PluginSource{Git: repository.url, Ref: "v1"}, root, background, InstallPluginOptions{})
	if err == nil || !regexp.MustCompile(`^installPlugin: .*empty: no package\.json$`).MatchString(err.Error()) {
		t.Fatalf("error = %v", err)
	}
	if names := listing(t, root); len(names) != 0 {
		t.Fatalf("root = %v", names)
	}
}

// Not upstream's: an abort of ctx during a clone kills git and returns the abort's reason, and what the install created is
// removed (`run`'s `signal: context.abortSignal` and the catch that removes the claim).
func TestInstallPluginReturnsTheAbortsReasonAndRemovesWhatItCreated(t *testing.T) {
	root := tempRoot(t)
	aborted, cancel := chordctx.WithCancel(background)
	stop := errors.New("stop")
	cancel(stop)
	_, err := InstallPlugin(PluginSource{Git: "file:///nonexistent/hello.git", Ref: "v1"}, root, aborted, InstallPluginOptions{})
	if !errors.Is(err, stop) {
		t.Fatalf("error = %v, want the abort's reason", err)
	}
	if names := listing(t, root); len(names) != 0 {
		t.Fatalf("root = %v", names)
	}
}

// D501: Pi reads `const [tarball] = JSON.parse(output)`, the list npm 10 and 11 print; npm 12 prints an object keyed by package name,
// which PiG reads by its first member. Everything else is Pi's: its errors for an empty list, a null or primitive member, a name that
// is no string, and for output that is no JSON value of those shapes.
func TestPackedTarballReadsTheListNpm10And11PrintAndTheObjectNpm12Prints(t *testing.T) {
	for name, tc := range map[string]struct{ output, want, wantError string }{
		"list (npm 10, 11)":       {output: `[{"filename":"acme-1.0.0.tgz","size":3}]`, want: "acme-1.0.0.tgz"},
		"first of a list":         {output: `[{"filename":"a.tgz"},{"filename":"b.tgz"}]`, want: "a.tgz"},
		"object by name (npm 12)": {output: `{"acme":{"filename":"acme-1.0.0.tgz","size":3}}`, want: "acme-1.0.0.tgz"},
		"first of an object":      {output: `{"acme":{"filename":"a.tgz"},"other":{"filename":"b.tgz"}}`, want: "a.tgz"},
		"empty list":              {output: `[]`, wantError: "what: packed nothing"},
		"empty object":            {output: `{}`, wantError: "what: packed nothing"},
		"null member":             {output: `[null]`, wantError: "Cannot read properties of null (reading 'filename')"},
		"primitive member":        {output: `{"acme":3}`, wantError: "Cannot read properties of a primitive (reading 'filename')"},
		"no filename":             {output: `[{"size":3}]`, wantError: `The "path" argument must be of type string`},
		"a string":                {output: `"acme.tgz"`, wantError: "npm's output is not a list of packed tarballs"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := packedTarball(tc.output, "what")
			if tc.wantError != "" {
				if err == nil || err.Error() != tc.wantError {
					t.Fatalf("error = %v, want %q", err, tc.wantError)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("packedTarball = %q, %v, want %q", got, err, tc.want)
			}
		})
	}
}
