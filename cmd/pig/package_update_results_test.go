package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// These cases retain gate-close-00's TestPackageAvailableUpdatesOriginal inputs and expectations from Pi package-manager.test.ts:2560-2616.
func TestPackageAvailableUpdatesResults(t *testing.T) {
	t.Run("offline", func(t *testing.T) {
		cwd, settings, log := packageUpdateResultFixture(t, "")
		t.Setenv("PI_OFFLINE", "1")
		if got := CheckForAvailableUpdates(cwd, settings); !reflect.DeepEqual(got, []PackageUpdate{}) {
			t.Errorf("updates=%#v, want empty array", got)
		}
		requireNoUpdateQuery(t, log)
	})
	t.Run("no packages", func(t *testing.T) {
		cwd, settings, log := packageUpdateResultFixture(t, "")
		if got := CheckForAvailableUpdates(cwd, settings); !reflect.DeepEqual(got, []PackageUpdate{}) {
			t.Errorf("updates=%#v, want empty array", got)
		}
		requireNoUpdateQuery(t, log)
	})
	for _, tc := range []struct {
		name, installed, latest string
		want                    []PackageUpdate
	}{
		{"newer registry", "1.0.0", `"1.2.3"`, []PackageUpdate{{Source: "npm:example", DisplayName: "example", Type: "npm", Scope: "project"}}},
		{"newer installed", "2.0.0", `"1.9.0"`, []PackageUpdate{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd, settings, _ := packageUpdateResultFixture(t, tc.latest)
			writeStartupFixtureFile(t, filepath.Join(cwd, codingagent.CONFIG_DIR_NAME, "npm", "node_modules", "example", "package.json"), `{"name":"example","version":"`+tc.installed+`"}`)
			if err := settings.SetProjectPackages([]codingagent.PackageSource{{Source: "npm:example"}}); err != nil {
				t.Fatal(err)
			}
			if got := CheckForAvailableUpdates(cwd, settings); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("updates=%#v, want %#v", got, tc.want)
			}
		})
	}
	t.Run("Git display name", func(t *testing.T) {
		// Pi package-manager.ts:1245 retains the parsed host and repository path.
		cwd, settings, _ := packageUpdateResultFixture(t, "")
		source := "git:https://github.com/example/repo.git"
		gitPath, err := packagemanager.GitInstallPath(cwd, settings.AgentDir(), source, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(gitPath, 0o755); err != nil {
			t.Fatal(err)
		}
		bin := t.TempDir()
		writeStubScript(t, filepath.Join(bin, "git"), "#!/bin/sh\ncase \"$*\" in\n 'rev-parse HEAD') printf '%040d\\n' 1 ;;\n 'rev-parse --abbrev-ref @{upstream}') printf 'origin/main\\n' ;;\n 'ls-remote origin refs/heads/main') printf '%040d\\trefs/heads/main\\n' 2 ;;\n *) exit 1 ;;\nesac\n")
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		if err := settings.SetProjectPackages([]codingagent.PackageSource{{Source: source}}); err != nil {
			t.Fatal(err)
		}
		want := []PackageUpdate{{Source: source, DisplayName: "github.com/example/repo", Type: "git", Scope: "project"}}
		if got := CheckForAvailableUpdates(cwd, settings); !reflect.DeepEqual(got, want) {
			t.Fatalf("updates=%#v, want %#v", got, want)
		}
	})
	t.Run("pinned packages", func(t *testing.T) {
		cwd, settings, log := packageUpdateResultFixture(t, "")
		writeStartupFixtureFile(t, filepath.Join(cwd, codingagent.CONFIG_DIR_NAME, "npm", "node_modules", "example", "package.json"), `{"name":"example","version":"1.0.0"}`)
		gitPath, err := packagemanager.GitInstallPath(cwd, settings.AgentDir(), "git:github.com/example/repo@v1", true)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(gitPath, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := settings.SetProjectPackages([]codingagent.PackageSource{{Source: "npm:example@1.0.0"}, {Source: "git:github.com/example/repo@v1"}}); err != nil {
			t.Fatal(err)
		}
		if got := CheckForAvailableUpdates(cwd, settings); !reflect.DeepEqual(got, []PackageUpdate{}) {
			t.Errorf("updates=%#v, want empty array", got)
		}
		requireNoUpdateQuery(t, log)
	})
}

func packageUpdateResultFixture(t *testing.T, reply string) (string, *codingagent.SettingsManager, string) {
	t.Helper()
	cwd, agent := t.TempDir(), t.TempDir()
	t.Setenv("HOME", cwd)
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", agent)
	t.Setenv("PI_OFFLINE", "")
	t.Setenv("PIG_OFFLINE", "")
	log := filepath.Join(cwd, "commands")
	bin := t.TempDir()
	t.Setenv("PIG_PM_COMMANDS", log)
	for _, name := range []string{"npm", "git"} {
		writeStubScript(t, filepath.Join(bin, name), fmt.Sprintf("#!/bin/sh\nprintf '%%s|%%s\\n' \"$PWD\" \"$*\" >> \"$PIG_PM_COMMANDS\"\nprintf '%%s\\n' '%s'\n", reply))
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return cwd, codingagent.NewSettingsManager(cwd, agent), log
}

func requireNoUpdateQuery(t *testing.T, log string) {
	t.Helper()
	if data, err := os.ReadFile(log); !os.IsNotExist(err) {
		t.Fatalf("unexpected command file: %q err=%v", data, err)
	}
}
