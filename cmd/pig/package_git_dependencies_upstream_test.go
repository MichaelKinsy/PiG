package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

func TestPackageGitDependenciesOriginal(t *testing.T) {
	for _, tc := range []struct {
		name, mode                                      string
		command                                         []string
		update, project, existing, pinned, dependencies bool
		failure                                         string
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:767
		{name: "should install git package dependencies with --omit=dev"},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:785
		{name: "should remove a newly created checkout when git clone fails", mode: "clone-failure", failure: "simulated git clone failure"},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:801
		{name: "should remove a newly cloned checkout when dependency installation fails", mode: "dependency-failure", failure: "simulated dependency install failure"},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:821
		{name: "should reconcile an existing git checkout to a pinned ref during install", existing: true, pinned: true},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:849
		{name: "should reconcile an existing git checkout to its update target when installing without a ref", existing: true, mode: "origin-head"},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:881
		{name: "should use plain install for git package dependencies when npmCommand is configured", command: []string{"pnpm"}},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:908
		{name: "should update git package dependencies with --omit=dev", update: true, project: true, existing: true},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:935
		{name: "should repair missing git package dependencies when the checkout is already current", update: true, existing: true, dependencies: true, mode: "current"},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:961
		{name: "should repair deleted git package dependencies when cleaning fails", update: true, existing: true, dependencies: true, mode: "clean-failure", failure: "simulated clean failure"},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:992
		{name: "should use plain install through npmCommand argv when updating git package dependencies", update: true, project: true, existing: true, command: []string{"mise", "exec", "node@20", "--", "pnpm"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPackageProcessFixture(t, `
 const mode=process.env.PIG_TEST_PACKAGE_GIT_MODE;
 if(command!=='git'){if(mode==='dependency-failure')throw new Error('simulated dependency install failure');if(args.at(-1)!=='install'&&args.at(-1)!=='--omit=dev')throw new Error('unexpected npm command');process.exit(0);}
 switch(args[0]){
 case 'clone':fs.mkdirSync(args[2],{recursive:true});if(mode==='clone-failure')throw new Error('simulated git clone failure');fs.writeFileSync(path.join(args[2],'package.json'),JSON.stringify({name:'repo',version:'1.0.0'}));break;
 case 'rev-parse':if(args[1]==='--abbrev-ref'){if(mode==='origin-head')process.exit(1);console.log('origin/main');}else if(args[1]==='HEAD')console.log(mode==='current'?'current-head':'old-head');else console.log(mode==='current'?'current-head':'new-head');break;
 case 'symbolic-ref':console.log('refs/remotes/origin/main');break;
 case 'clean':if(mode==='clean-failure')throw new Error('simulated clean failure');break;
 case 'fetch':case 'reset':case 'checkout':case 'remote':break;
 default:throw new Error('unexpected git command '+args.join(' '));}
 `)
			t.Setenv("PIG_TEST_PACKAGE_GIT_MODE", tc.mode)
			if err := f.settings.SetNpmCommand(tc.command); err != nil {
				t.Fatal(err)
			}
			source := "git:github.com/user/repo"
			if tc.pinned {
				source += "@v2"
			}
			root := f.agent
			if tc.project {
				root = codingagent.ProjectConfigDir(f.cwd)
			}
			checkout := filepath.Join(root, "git", "github.com", "user", "repo")
			if tc.existing {
				if err := os.MkdirAll(checkout, 0o755); err != nil {
					t.Fatal(err)
				}
				if tc.mode != "origin-head" {
					manifest := `{"name":"repo","version":"1.0.0"}`
					if tc.dependencies {
						manifest = `{"name":"repo","version":"1.0.0","dependencies":{"dependency":"1.0.0"}}`
					}
					writePackageResource(t, filepath.Join(checkout, "package.json"), manifest)
				}
			}
			var err error
			stderr := captureStderr(t, func() {
				if tc.update {
					sources := []codingagent.PackageSource{{Source: source}}
					if tc.project {
						err = f.settings.SetProjectPackages(sources)
					} else {
						err = f.settings.SetPackages(sources)
					}
					if err != nil {
						t.Fatal(err)
					}
					err = updatePackages(f.cwd, f.settings, source, nil)
				} else {
					err = packagemanager.InstallManagedGit(f.cwd, f.settings.AgentDir(), f.settings, source, tc.project)
				}
			})
			if tc.failure != "" {
				// The upstream rejected command mock is a failing child here: retain its diagnostic on stderr and propagate Pi's real runCommand exit error.
				if err == nil || !strings.Contains(stderr, tc.failure) || !strings.HasSuffix(err.Error(), "failed with code 1") {
					t.Fatalf("error=%v stderr=%q want %s", err, stderr, tc.failure)
				}
				if !tc.existing {
					if _, err := os.Stat(checkout); !os.IsNotExist(err) {
						t.Fatalf("failed checkout retained: %v", err)
					}
					return
				}
			} else if err != nil {
				t.Fatal(err)
			}
			calls := f.calls(t)
			if tc.pinned {
				requirePackageProcessCall(t, calls, "git", []string{"fetch", "origin", "v2"}, checkout)
				requirePackageProcessCall(t, calls, "git", []string{"reset", "--hard", "FETCH_HEAD^{commit}"}, checkout)
				requirePackageProcessCall(t, calls, "git", []string{"clean", "-fdx"}, checkout)
			}
			if tc.mode == "origin-head" {
				requirePackageProcessCall(t, calls, "git", []string{"fetch", "--prune", "--no-tags", "origin", "+refs/heads/main:refs/remotes/origin/main"}, checkout)
				requirePackageProcessCall(t, calls, "git", []string{"reset", "--hard", "origin/HEAD^{commit}"}, checkout)
				requirePackageProcessCall(t, calls, "git", []string{"clean", "-fdx"}, checkout)
				return
			}
			command, args := "npm", []string{"install", "--omit=dev"}
			if len(tc.command) > 0 {
				command = tc.command[0]
				args = append(append([]string{}, tc.command[1:]...), "install")
			}
			requirePackageProcessCall(t, calls, command, args, checkout)
			if tc.mode == "current" {
				for _, call := range calls {
					if call.Command == "git" && call.Args[0] == "clean" {
						t.Fatalf("cleaned current checkout: %+v", calls)
					}
				}
			}
		})
	}
}
