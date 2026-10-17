package cli

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/packagemanager"

	"github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

func packageQueryFixture(t *testing.T, reply string) (packageResourceFixture, string) {
	t.Helper()
	f := newPackageResourceFixture(t)
	log := filepath.Join(f.cwd, "commands")
	bin := t.TempDir()
	t.Setenv("PIG_PM_COMMANDS", log)
	// Git Bash's $PWD is an MSYS path (/tmp/...); pwd -W prints the Windows path.
	pwd := `"$PWD"`
	if runtime.GOOS == "windows" {
		pwd = `"$(pwd -W)"`
	}
	for _, name := range []string{"npm", "git", "mise"} {
		writeStubScript(t, filepath.Join(bin, name), fmt.Sprintf("#!/bin/sh\nprintf '%%s|%%s\\n' %s \"$*\" >> \"$PIG_PM_COMMANDS\"\nprintf '%%s\\n' '%s'\n", pwd, reply))
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return f, log
}
func requireNoPackageQuery(t *testing.T, log string) {
	t.Helper()
	if data, err := os.ReadFile(log); !os.IsNotExist(err) {
		t.Fatalf("unexpected command file: %q err=%v", data, err)
	}
}

func TestPackageUpdateLookupPrefixesOriginal(t *testing.T) {
	for _, tc := range []struct{ input, configured string }{
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2487
		{"example", "npm:example"},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2495
		{"github.com/example/repo", "git:github.com/example/repo"},
	} {
		t.Run("should suggest source prefix for "+tc.input, func(t *testing.T) {
			f := newPackageResourceFixture(t)
			if err := f.settings.SetProjectPackages([]codingagent.PackageSource{{Source: tc.configured}}); err != nil {
				t.Fatal(err)
			}
			err := packageManagerFor(f.cwd, f.settings, nil).Update(tc.input)
			want := "No matching package found for " + tc.input + ". Did you mean " + tc.configured + "?"
			if err == nil || err.Error() != want {
				t.Fatalf("error=%v, want %q", err, want)
			}
		})
	}
}

func TestPackageOfflineResolutionOriginal(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2503
	t.Run("should skip installing missing package sources when offline", func(t *testing.T) {
		f, log := packageQueryFixture(t, "")
		t.Setenv("PI_OFFLINE", "1")
		if err := f.settings.SetProjectPackages([]codingagent.PackageSource{{Source: "npm:missing-package"}, {Source: "git:github.com/example/missing-repo"}}); err != nil {
			t.Fatal(err)
		}
		installed, _ := packagemanager.EnsureConfiguredPackagesInstalled(f.cwd, f.settings.AgentDir(), f.settings)
		if len(installed) != 0 {
			t.Fatalf("installed=%v", installed)
		}
		for _, item := range f.items(t) {
			if item.Origin == "package" {
				t.Fatalf("unexpected package resource=%+v", item)
			}
		}
		requireNoPackageQuery(t, log)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2515
	t.Run("should skip refreshing temporary git sources when offline", func(t *testing.T) {
		f, log := packageQueryFixture(t, "")
		t.Setenv("PI_OFFLINE", "1")
		digest := sha256.Sum256([]byte("git-github.com-example/repo"))
		path := filepath.Join(f.agent, "tmp", "extensions", "git-github.com", fmt.Sprintf("%x", digest)[:8], "example", "repo")
		file := filepath.Join(path, "extensions", "index.ts")
		writePackageResource(t, file, "export default function() {};")
		flags, err := resolveCLIResourceFlags(Args{Extensions: []string{"git:github.com/example/repo"}, NoExtensions: true}, f.cwd)
		if err != nil {
			t.Fatal(err)
		}
		configs := collectExtensionConfigs(f.cwd, f.agent, f.settings, flags, nil)
		found := false
		for _, config := range configs {
			if config.Source == file && config.Enabled {
				found = true
			}
		}
		if !found {
			t.Fatalf("extension absent: %+v", configs)
		}
		requireNoPackageQuery(t, log)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2531
	t.Run("should not run npm view during resolve for installed unpinned packages", func(t *testing.T) {
		f, log := packageQueryFixture(t, "")
		t.Setenv("PI_OFFLINE", "1")
		path := filepath.Join(f.cwd, codingagent.CONFIG_DIR_NAME, "npm", "node_modules", "example")
		writePackageResource(t, filepath.Join(path, "package.json"), `{"name":"example","version":"1.0.0"}`)
		file := filepath.Join(path, "extensions", "index.ts")
		writePackageResource(t, file, "export default function() {};")
		if err := f.settings.SetProjectPackages([]codingagent.PackageSource{{Source: "npm:example@^1.0.0"}}); err != nil {
			t.Fatal(err)
		}
		requirePackageResource(t, f.items(t), file, tui.ResourceExtensions, true)
		requireNoPackageQuery(t, log)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2546
	t.Run("should reinstall pinned npm packages when installed version does not match", func(t *testing.T) {
		f, log := packageQueryFixture(t, "")
		path := filepath.Join(f.cwd, codingagent.CONFIG_DIR_NAME, "npm", "node_modules", "example")
		writePackageResource(t, filepath.Join(path, "package.json"), `{"name":"example","version":"1.0.0"}`)
		if err := f.settings.SetProjectPackages([]codingagent.PackageSource{{Source: "npm:example@2.0.0"}}); err != nil {
			t.Fatal(err)
		}
		_, missing := packagemanager.EnsureConfiguredPackagesInstalled(f.cwd, f.settings.AgentDir(), f.settings)
		if len(missing) != 0 {
			t.Fatalf("missing=%v", missing)
		}
		data, err := os.ReadFile(log)
		if err != nil {
			t.Fatal("configured version mismatch did not reinstall")
		}
		installs := 0
		for line := range strings.SplitSeq(string(data), "\n") {
			if strings.Contains(line, "|install ") {
				installs++
			}
		}
		if installs != 1 {
			t.Fatalf("install calls=%d, commands=%s", installs, data)
		}
		fmt.Printf("RESOLVE_PINNED %d\n", installs)
	})
}

func TestPackageAvailableUpdatesOriginal(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2560
	t.Run("should not check package updates when offline", func(t *testing.T) {
		f, log := packageQueryFixture(t, "")
		t.Setenv("PI_OFFLINE", "1")
		if got := packagemanager.NewPackageManager(packagemanager.PackageManagerOptions{CWD: f.cwd, SettingsManager: f.settings}).CheckForAvailableUpdates(); !reflect.DeepEqual(got, []packagemanager.PackageUpdate{}) {
			t.Fatalf("updates=%v, want []", got)
		}
		requireNoPackageQuery(t, log)
	})
	for _, tc := range []struct {
		name, installed, latest string
		want                    []packagemanager.PackageUpdate
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2569
		{"should report updates for installed unpinned npm packages", "1.0.0", `"1.2.3"`, []packagemanager.PackageUpdate{{Source: "npm:example", DisplayName: "example", Type: "npm", Scope: "project"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2588
		{"should not report npm updates when installed version is newer than registry version", "2.0.0", `"1.9.0"`, []packagemanager.PackageUpdate{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := packageQueryFixture(t, tc.latest)
			writePackageResource(t, filepath.Join(f.cwd, codingagent.CONFIG_DIR_NAME, "npm", "node_modules", "example", "package.json"), `{"name":"example","version":"`+tc.installed+`"}`)
			if err := f.settings.SetProjectPackages([]codingagent.PackageSource{{Source: "npm:example"}}); err != nil {
				t.Fatal(err)
			}
			got := packagemanager.NewPackageManager(packagemanager.PackageManagerOptions{CWD: f.cwd, SettingsManager: f.settings}).CheckForAvailableUpdates()
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("updates=%+v, want %+v", got, tc.want)
			}
			rows := make([][]string, 0, len(got))
			for _, update := range got {
				rows = append(rows, []string{update.Source, update.DisplayName, update.Type, update.Scope})
			}
			raw, err := json.Marshal(rows)
			if err != nil {
				t.Fatal(err)
			}
			fmt.Printf("AVAILABLE_UPDATES %s\n", raw)
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2600
	t.Run("should skip pinned packages when checking for updates", func(t *testing.T) {
		f, log := packageQueryFixture(t, "")
		writePackageResource(t, filepath.Join(f.cwd, codingagent.CONFIG_DIR_NAME, "npm", "node_modules", "example", "package.json"), `{"name":"example","version":"1.0.0"}`)
		if err := os.MkdirAll(filepath.Join(f.cwd, codingagent.CONFIG_DIR_NAME, "git", "github.com", "example", "repo"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := f.settings.SetProjectPackages([]codingagent.PackageSource{{Source: "npm:example@1.0.0"}, {Source: "git:github.com/example/repo@v1"}}); err != nil {
			t.Fatal(err)
		}
		if got := packagemanager.NewPackageManager(packagemanager.PackageManagerOptions{CWD: f.cwd, SettingsManager: f.settings}).CheckForAvailableUpdates(); !reflect.DeepEqual(got, []packagemanager.PackageUpdate{}) {
			t.Fatalf("updates=%v, want []", got)
		}
		requireNoPackageQuery(t, log)
	})
}

func TestPackageLatestVersionCommandsOriginal(t *testing.T) {
	for _, tc := range []struct {
		name, spec    string
		command, argv []string
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2619
		{"should use npm view to fetch latest version", "example", nil, []string{"view", "example", "version", "--json"}},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2632
		{"should use npmCommand argv for npm update checks", "@scope/pkg", []string{"mise", "exec", "node@20", "--", "npm"}, []string{"exec", "node@20", "--", "npm", "view", "@scope/pkg", "version", "--json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, log := packageQueryFixture(t, `"1.2.3"`)
			if tc.command != nil {
				if err := f.settings.SetNpmCommand(tc.command); err != nil {
					t.Fatal(err)
				}
			}
			ref, err := source.Parse("npm:"+tc.spec, source.Options{})
			if err != nil {
				t.Fatal(err)
			}
			latest, err := packagemanager.GetLatestNpmVersion(f.cwd, f.settings, ref, false)
			if err != nil || latest != "1.2.3" {
				t.Fatalf("latest=%s err=%v", latest, err)
			}
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			// D79 keeps metadata commands in the owning managed npm root so project-local configuration cannot alter registry queries.
			// The stub logs the shell's $PWD, which is the physical directory (macOS /var is /private/var).
			dir, argv, _ := strings.Cut(string(data), "|")
			if want := strings.Join(tc.argv, " ") + "\n"; canonicalTestPath(t, dir) != canonicalTestPath(t, filepath.Join(f.agent, "npm")) || argv != want {
				t.Fatalf("command=%q, want %q", data, filepath.Join(f.agent, "npm")+"|"+want)
			}
		})
	}
}
