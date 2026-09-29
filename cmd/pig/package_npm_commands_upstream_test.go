package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
	"github.com/MichaelKinsy/PiG/tui"
)

type packageProcessCall struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	CWD     string   `json:"cwd"`
}

type packageProcessFixture struct {
	packageResourceFixture
	log string
}

func newPackageProcessFixture(t *testing.T, body string) packageProcessFixture {
	t.Helper()
	f := newPackageResourceFixture(t)
	f.settings = codingagent.NewSettingsManager(f.cwd, f.agent)
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := filepath.Join(bin, "process.cjs")
	log := filepath.Join(bin, "calls.jsonl")
	t.Setenv("PIG_TEST_PACKAGE_LOG", log)
	t.Setenv("PIG_TEST_PACKAGE_AGENT", f.agent)
	t.Setenv("PIG_TEST_PACKAGE_CWD", f.cwd)
	prefix := `const fs=require('node:fs'),path=require('node:path');const [command,...args]=process.argv.slice(2);fs.appendFileSync(process.env.PIG_TEST_PACKAGE_LOG,JSON.stringify({command,args,cwd:process.cwd()})+'\n');`
	writePackageResource(t, script, prefix+body)
	for _, name := range []string{"npm", "pnpm", "bun", "mise", "git"} {
		writeStubScript(t, filepath.Join(bin, name), fmt.Sprintf("#!/bin/sh\nexec %q %q %q \"$@\"\n", filepath.ToSlash(node), filepath.ToSlash(script), name))
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return packageProcessFixture{f, log}
}
func (f packageProcessFixture) calls(t *testing.T) []packageProcessCall {
	t.Helper()
	data, err := os.ReadFile(f.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var calls []packageProcessCall
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var c packageProcessCall
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, c)
	}
	return calls
}

// requirePackageProcessCall compares cwd physically: the stub records
// process.cwd(), which getcwd reports without symlinks (macOS /var is
// /private/var).
func requirePackageProcessCall(t *testing.T, calls []packageProcessCall, command string, args []string, cwd string) {
	t.Helper()
	for _, c := range calls {
		if c.Command == command && reflect.DeepEqual(c.Args, args) && (cwd == "" || canonicalTestPath(t, c.CWD) == canonicalTestPath(t, cwd)) {
			return
		}
	}
	t.Fatalf("missing %s %q cwd=%q in %+v", command, args, cwd, calls)
}

func TestPackageNpmCommandOriginal(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command []string
		remove  bool
		want    []string
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:702
		{"should use npmCommand argv for npm installs", []string{"mise", "exec", "node@20", "--", "npm"}, false, []string{"exec", "node@20", "--", "npm", "install", "@scope/pkg", "--prefix", "ROOT", "--legacy-peer-deps"}},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:733
		{"should pass legacy peer deps when uninstalling npm packages", nil, true, []string{"uninstall", "@scope/pkg", "--prefix", "ROOT", "--legacy-peer-deps"}},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:746
		{"should use bun --cwd for npm package installs", []string{"mise", "exec", "bun@1", "--", "bun"}, false, []string{"exec", "bun@1", "--", "bun", "install", "@scope/pkg", "--cwd", "ROOT", "--omit=peer"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPackageProcessFixture(t, "")
			if err := f.settings.SetNpmCommand(tc.command); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(f.agent, "npm")
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			var err error
			if tc.remove {
				err = removePackageArtifacts(f.cwd, f.settings, "npm:@scope/pkg", false)
			} else {
				err = packagemanager.InstallManagedNPM(f.cwd, f.settings.AgentDir(), f.settings, "npm:@scope/pkg", false)
			}
			if err != nil {
				t.Fatal(err)
			}
			want := append([]string(nil), tc.want...)
			for i, v := range want {
				if v == "ROOT" {
					want[i] = root
				}
			}
			command := "npm"
			if len(tc.command) > 0 {
				command = tc.command[0]
			}
			requirePackageProcessCall(t, f.calls(t), command, want, "")
		})
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1030
func TestPackageNpmRootCommandChangesOriginal(t *testing.T) {
	f := newPackageProcessFixture(t, `if(command!=='mise'||args.slice(-2).join(' ')!=='root -g')throw new Error('unexpected command'); console.log(path.join(process.env.PIG_TEST_PACKAGE_CWD,args[1]==='node@20'?'node20':'node22','lib','node_modules'));`)
	root20 := filepath.Join(f.cwd, "node20", "lib", "node_modules")
	want := filepath.Join(root20, "@scope", "pkg")
	if err := os.MkdirAll(want, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := f.settings.SetNpmCommand([]string{"mise", "exec", "node@20", "--", "npm"}); err != nil {
		t.Fatal(err)
	}
	if got := packagemanager.InstalledPathForSource(f.cwd, f.settings.AgentDir(), f.settings, "npm:@scope/pkg", false); got != want {
		t.Fatalf("installed=%q want=%q", got, want)
	}
	if err := f.settings.SetNpmCommand([]string{"mise", "exec", "node@22", "--", "npm"}); err != nil {
		t.Fatal(err)
	}
	if got := packagemanager.InstalledPathForSource(f.cwd, f.settings.AgentDir(), f.settings, "npm:@scope/pkg", false); got != "" {
		t.Fatalf("old root retained: %s", got)
	}
	calls := f.calls(t)
	expected := [][]string{{"exec", "node@20", "--", "npm", "root", "-g"}, {"exec", "node@22", "--", "npm", "root", "-g"}}
	if len(calls) != len(expected) {
		t.Fatalf("calls=%+v", calls)
	}
	for i, want := range expected {
		if calls[i].Command != "mise" || !reflect.DeepEqual(calls[i].Args, want) {
			t.Fatalf("call=%+v want=%q", calls[i], want)
		}
	}
	fmt.Println("NPM_ROOT_CHANGE node20 found; node22 missing")
}

// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1069
func TestPackagePnpmManagedResolutionOriginal(t *testing.T) {
	f := newPackageProcessFixture(t, `if(args[0]!=='install')process.exit(7);const root=args[args.indexOf('--prefix')+1],pkg=path.join(root,'node_modules','pnpm-pkg');fs.mkdirSync(path.join(pkg,'extensions'),{recursive:true});fs.writeFileSync(path.join(pkg,'package.json'),JSON.stringify({name:'pnpm-pkg',version:'1.0.0'}));fs.writeFileSync(path.join(pkg,'extensions/index.ts'),'export default function() {};');`)
	if err := f.settings.SetNpmCommand([]string{"pnpm"}); err != nil {
		t.Fatal(err)
	}
	if err := f.settings.SetPackages([]codingagent.PackageSource{{Source: "npm:pnpm-pkg"}}); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(f.agent, "npm", "node_modules", "pnpm-pkg")
	for range 2 {
		_, missing := packagemanager.EnsureConfiguredPackagesInstalled(f.cwd, f.settings.AgentDir(), f.settings)
		if len(missing) > 0 {
			t.Fatal(missing)
		}
		requirePackageResource(t, f.items(t), filepath.Join(pkg, "extensions/index.ts"), tui.ResourceExtensions, true)
	}
	var installs []packageProcessCall
	for _, c := range f.calls(t) {
		if len(c.Args) > 0 && c.Args[0] == "install" {
			installs = append(installs, c)
		}
	}
	if len(installs) != 1 {
		t.Fatalf("installs=%+v", installs)
	}
	requirePackageProcessCall(t, installs, "pnpm", []string{"install", "pnpm-pkg", "--prefix", filepath.Join(f.agent, "npm"), "--config.auto-install-peers=false", "--config.strict-peer-dependencies=false", "--config.strict-dep-builds=false"}, "")
	if got := packagemanager.InstalledPathForSource(f.cwd, f.settings.AgentDir(), f.settings, "npm:pnpm-pkg", false); got != pkg {
		t.Fatalf("installed=%s want=%s", got, pkg)
	}
}

func TestPackagePnpmLegacyResolutionOriginal(t *testing.T) {
	for _, tc := range []struct {
		name               string
		command            []string
		malformed, resolve bool
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1116
		{"should load legacy pnpm global package paths from pnpm list output", []string{"pnpm"}, false, true},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1159
		{"should resolve wrapped pnpm global package paths from pnpm list output", []string{"mise", "exec", "node@20", "--", "pnpm"}, false, false},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1185
		{"should ignore malformed legacy pnpm global package lists", []string{"pnpm"}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPackageProcessFixture(t, `if(args.slice(-5).join(' ')!=='list -g --depth 0 --json')throw new Error('unexpected command');console.log(process.env.PIG_TEST_PACKAGE_LIST);`)
			root := filepath.Join(f.cwd, "pnpm", "global", "v11")
			pkg := filepath.Join(root, "20-hash", "node_modules", "pnpm-pkg")
			output := "not json"
			if !tc.malformed {
				writePackageResource(t, filepath.Join(pkg, "package.json"), `{"name":"pnpm-pkg","version":"1.0.0"}`)
				writePackageResource(t, filepath.Join(pkg, "extensions/index.ts"), "export default function() {};")
				data, _ := json.Marshal([]any{map[string]any{"path": root, "dependencies": map[string]any{"pnpm-pkg": map[string]string{"version": "1.0.0", "path": pkg}}}})
				output = string(data)
			}
			t.Setenv("PIG_TEST_PACKAGE_LIST", output)
			if err := f.settings.SetNpmCommand(tc.command); err != nil {
				t.Fatal(err)
			}
			if tc.resolve {
				if err := f.settings.SetPackages([]codingagent.PackageSource{{Source: "npm:pnpm-pkg"}}); err != nil {
					t.Fatal(err)
				}
				_, missing := packagemanager.EnsureConfiguredPackagesInstalled(f.cwd, f.settings.AgentDir(), f.settings)
				if len(missing) > 0 {
					t.Fatal(missing)
				}
				requirePackageResource(t, f.items(t), filepath.Join(pkg, "extensions/index.ts"), tui.ResourceExtensions, true)
			}
			want := pkg
			if tc.malformed {
				want = ""
			}
			if got := packagemanager.InstalledPathForSource(f.cwd, f.settings.AgentDir(), f.settings, "npm:pnpm-pkg", false); got != want {
				t.Fatalf("installed=%s want=%s", got, want)
			}
			expected := append(append([]string{}, tc.command[1:]...), "list", "-g", "--depth", "0", "--json")
			for _, call := range f.calls(t) {
				if call.Command != tc.command[0] || !reflect.DeepEqual(call.Args, expected) {
					t.Fatalf("unexpected process: %+v", call)
				}
			}
		})
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2328
func TestPackageLegacyNpmUpdateMigratesOriginal(t *testing.T) {
	f := newPackageProcessFixture(t, `if(args.join(' ')==='root -g'){console.log(path.join(process.env.PIG_TEST_PACKAGE_CWD,'legacy-global','node_modules'));}else if(args[0]==='install'){const pkg=path.join(args[args.indexOf('--prefix')+1],'node_modules','legacy-pkg');fs.mkdirSync(pkg,{recursive:true});fs.writeFileSync(path.join(pkg,'package.json'),JSON.stringify({name:'legacy-pkg',version:'1.0.0'}));}else throw new Error('unexpected command');`)
	legacy := filepath.Join(f.cwd, "legacy-global", "node_modules", "legacy-pkg")
	managed := filepath.Join(f.agent, "npm", "node_modules", "legacy-pkg")
	writePackageResource(t, filepath.Join(legacy, "package.json"), `{"name":"legacy-pkg","version":"1.0.0"}`)
	if err := f.settings.SetPackages([]codingagent.PackageSource{{Source: "npm:legacy-pkg"}}); err != nil {
		t.Fatal(err)
	}
	if got := packagemanager.InstalledPathForSource(f.cwd, f.settings.AgentDir(), f.settings, "npm:legacy-pkg", false); got != legacy {
		t.Fatalf("installed=%s want=%s", got, legacy)
	}
	if err := updatePackages(f.cwd, f.settings, "npm:legacy-pkg", nil); err != nil {
		t.Fatal(err)
	}
	installs := 0
	for _, c := range f.calls(t) {
		if c.Args[0] == "view" {
			t.Fatal("registry lookup during migration")
		}
		if c.Args[0] == "install" {
			installs++
		}
	}
	if installs != 1 {
		t.Fatalf("installs=%d want1", installs)
	}
	requirePackageProcessCall(t, f.calls(t), "npm", []string{"install", "legacy-pkg@latest", "--prefix", filepath.Join(f.agent, "npm"), "--legacy-peer-deps"}, "")
	if got := packagemanager.InstalledPathForSource(f.cwd, f.settings.AgentDir(), f.settings, "npm:legacy-pkg", false); got != managed {
		t.Fatalf("installed=%s want=%s", got, managed)
	}
	fmt.Println("NPM_LEGACY_UPDATE legacy found; managed installed; no registry query")
}
