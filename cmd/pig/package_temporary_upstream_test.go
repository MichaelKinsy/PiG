package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// .upstream/v0.87.1/packages/coding-agent/test/git-update.test.ts:237
func TestTemporaryGitRefreshWhenResolvingUpstream(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	t.Chdir(cwd)
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_OFFLINE", "")
	t.Setenv("PIG_OFFLINE", "")
	const source = "git:github.com/test/extension"
	digest := sha256.Sum256([]byte("git-github.com-test/extension"))
	cached := filepath.Join(agentDir, "tmp", "extensions", "git-github.com", fmt.Sprintf("%x", digest)[:8], "test", "extension")
	extensionFile := filepath.Join(cached, "pi-extensions", "session-breakdown.ts")
	if err := os.MkdirAll(filepath.Dir(extensionFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cached, "package.json"), []byte(`{"pi":{"extensions":["./pi-extensions"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(extensionFile, []byte("// stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "commands")
	t.Setenv("PIG_TEMP_REFRESH_LOG", log)
	t.Setenv("PIG_TEMP_REFRESH_FILE", extensionFile)
	writeStubScript(t, filepath.Join(bin, "git"), `#!/bin/sh
printf 'git %s\n' "$*" >> "$PIG_TEMP_REFRESH_LOG"
if [ "$1" = "rev-parse" ]; then
 case "$2" in
 HEAD) printf 'local-head\n';;
 --abbrev-ref) printf 'origin/main\n';;
 *) printf 'remote-head\n';;
 esac
elif [ "$1" = "reset" ]; then
 printf '// fresh' > "$PIG_TEMP_REFRESH_FILE"
fi
`)
	writeStubScript(t, filepath.Join(bin, "npm"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	flags, err := resolveCLIResourceFlags(CLIFlags{Extensions: []string{source}, NoExtensions: true}, cwd)
	if err != nil {
		t.Fatal(err)
	}
	configs := collectExtensionConfigs(cwd, agentDir, sm, flags, nil)
	if !slices.ContainsFunc(configs, func(config subprocess.ExtConfig) bool {
		return config.Source == extensionFile && config.Enabled && config.ResolveError() == nil
	}) {
		t.Fatalf("refreshed extension was not resolved: %+v", configs)
	}
	commands, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("temporary source was not refreshed: %v", err)
	}
	if !strings.Contains(string(commands), "git fetch --prune --no-tags origin +refs/heads/main:refs/remotes/origin/main") {
		t.Fatalf("commands=%s", commands)
	}
	got, err := os.ReadFile(extensionFile)
	if err != nil || string(got) != "// fresh" {
		t.Fatalf("extension=%q err=%v", got, err)
	}
	fmt.Printf("TEMP_GIT_REFRESH %s\n", got)
}

// package-manager.ts:1831-1862 installs a missing temporary source with the same ref and cleanup behavior as an installed source.
func TestTemporaryGitInstallAndCleanup(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("dependencyFailure=%v", fail), func(t *testing.T) {
			cwd, remote, _, sm := newGitUpdateFixture(t)
			writePackageResource(t, filepath.Join(remote, "package.json"), `{"pi":{"extensions":["./pi-extensions"]}}`)
			writePackageResource(t, filepath.Join(remote, "pi-extensions", "session-breakdown.ts"), "export default function() {};")
			runGitUpdateTest(t, remote, "add", "package.json", "pi-extensions/session-breakdown.ts")
			runGitUpdateTest(t, remote, "commit", "-m", "Package resources")
			runGitUpdateTest(t, remote, "tag", "v1")
			commitGitUpdateFile(t, remote, "// v2", "Second commit")
			t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
			t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
			t.Setenv("GIT_CONFIG_COUNT", "1")
			t.Setenv("GIT_CONFIG_KEY_0", "url.file://"+filepath.ToSlash(remote)+".insteadOf")
			t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/test/extension")
			bin := t.TempDir()
			exit := "0"
			if fail {
				exit = "19"
			}
			writeStubScript(t, filepath.Join(bin, "npm"), "#!/bin/sh\nexit "+exit+"\n")
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			const source = "git:github.com/test/extension@v1"
			digest := sha256.Sum256([]byte("git-github.com-test/extension"))
			cached := filepath.Join(sm.AgentDir(), "tmp", "extensions", "git-github.com", fmt.Sprintf("%x", digest)[:8], "test", "extension")
			configs := collectExtensionConfigs(cwd, sm.AgentDir(), sm, CLIFlags{Extensions: []string{source}, NoExtensions: true}, nil)
			if len(configs) != 1 {
				t.Fatalf("configs = %+v", configs)
			}
			if fail {
				if configs[0].ResolveError() == nil {
					t.Fatal("dependency failure was not surfaced")
				}
				if _, err := os.Stat(cached); !os.IsNotExist(err) {
					t.Fatalf("failed clone retained checkout: %v", err)
				}
				return
			}
			if configs[0].ResolveError() != nil || configs[0].Source != filepath.Join(cached, "pi-extensions", "session-breakdown.ts") {
				t.Fatalf("temporary clone not resolved: %+v", configs)
			}
			assertGitUpdateHead(t, cached, runGitUpdateTest(t, remote, "rev-parse", "v1"), "// v1")
		})
	}
}

// package-manager.ts:1298-1311 refreshes only existing unpinned online checkouts. A failed refresh retains the cached resources, while an offline miss contributes nothing.
func TestTemporaryGitCacheBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, source                string
		offline, missing, wantFetch bool
	}{
		{"offline cache", "git:github.com/test/extension", true, false, false},
		{"offline miss", "git:github.com/test/extension", true, true, false},
		{"pinned cache", "git:github.com/test/extension@v2", false, false, false},
		{"failed refresh", "git:github.com/test/extension", false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd, agentDir := t.TempDir(), t.TempDir()
			t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
			t.Setenv("PIG_OFFLINE", "")
			t.Setenv("PI_OFFLINE", "")
			if tc.offline {
				t.Setenv("PI_OFFLINE", "1")
			}
			digest := sha256.Sum256([]byte("git-github.com-test/extension"))
			cached := filepath.Join(agentDir, "tmp", "extensions", "git-github.com", fmt.Sprintf("%x", digest)[:8], "test", "extension")
			file := filepath.Join(cached, "pi-extensions", "session-breakdown.ts")
			if !tc.missing {
				writePackageResource(t, filepath.Join(cached, "package.json"), `{"pi":{"extensions":["./pi-extensions"]}}`)
				writePackageResource(t, file, "// stale")
			}
			bin, log := t.TempDir(), filepath.Join(t.TempDir(), "commands")
			writeStubScript(t, filepath.Join(bin, "git"), fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >> %q\nexit 1\n", filepath.ToSlash(log)))
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			sm := codingagent.NewSettingsManager(cwd, agentDir)
			configs := collectExtensionConfigs(cwd, agentDir, sm, CLIFlags{Extensions: []string{tc.source}, NoExtensions: true}, nil)
			if tc.missing {
				if len(configs) != 0 {
					t.Fatalf("offline miss returned resources: %+v", configs)
				}
			} else {
				if len(configs) != 1 || configs[0].Source != file || configs[0].ResolveError() != nil {
					t.Fatalf("cached extension not retained: %+v", configs)
				}
				// Upstream resource-loader.ts:435-439 stamps resolved -e entries as CLI resources, not installed Package resources.
				wantInfo := codingagent.PiSourceInfo{Path: file, Source: "cli", Scope: "temporary", Origin: "top-level"}
				if got := configs[0].SourceInfo; got != wantInfo {
					t.Fatalf("provenance = %+v, want %+v", got, wantInfo)
				}
				data, err := os.ReadFile(file)
				if err != nil || string(data) != "// stale" {
					t.Fatalf("cache = %q, %v", data, err)
				}
			}
			_, err := os.Stat(log)
			if tc.wantFetch && err != nil {
				t.Fatalf("refresh was not attempted: %v", err)
			}
			if !tc.wantFetch && !os.IsNotExist(err) {
				t.Fatalf("unexpected git command: %v", err)
			}
		})
	}
}

// Pi package-manager.ts:1283-1307 collects a temporary npm or git -e source only through collectPackageResources (:2153-2203). Unlike a local -e directory (:1346-1351), a root index.ts outside a manifest or conventional extensions/ directory loads nothing.
func TestTemporaryGitSourceLoadsOnlyPackageResources(t *testing.T) {
	gitCache := func(agentDir string) string {
		digest := sha256.Sum256([]byte("git-github.com-test/extension"))
		return filepath.Join(agentDir, "tmp", "extensions", "git-github.com", fmt.Sprintf("%x", digest)[:8], "test", "extension")
	}
	npmCache := func(agentDir string) string { return temporaryNpmCacheFixturePath(agentDir, "review-probe") }
	for _, tc := range []struct {
		name   string
		source string
		cache  func(string) string
		files  []string
		want   string
	}{
		{"git root index only", "git:github.com/test/extension", gitCache, []string{"index.ts"}, ""},
		{"git conventional directory", "git:github.com/test/extension", gitCache, []string{"index.ts", "extensions/probe.ts"}, "extensions/probe.ts"},
		{"npm root index only", "npm:review-probe", npmCache, []string{"index.ts"}, ""},
		{"npm conventional directory", "npm:review-probe", npmCache, []string{"index.ts", "extensions/probe.ts"}, "extensions/probe.ts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd, agentDir := t.TempDir(), t.TempDir()
			t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
			t.Setenv("PIG_OFFLINE", "")
			t.Setenv("PI_OFFLINE", "1")
			cached := tc.cache(agentDir)
			// An installed npm Package always has a package.json; this one has no "pi" manifest.
			writePackageResource(t, filepath.Join(cached, "package.json"), `{"name":"review-probe","version":"1.0.0"}`)
			for _, file := range tc.files {
				writePackageResource(t, filepath.Join(cached, filepath.FromSlash(file)), "export default function () {}\n")
			}
			sm := codingagent.NewSettingsManager(cwd, agentDir)
			configs := collectExtensionConfigs(cwd, agentDir, sm, CLIFlags{Extensions: []string{tc.source}, NoExtensions: true}, nil)
			if tc.want == "" {
				if len(configs) != 0 {
					t.Fatalf("checkout without package resources loaded %+v", configs)
				}
				return
			}
			want := filepath.Join(cached, filepath.FromSlash(tc.want))
			wantInfo := codingagent.PiSourceInfo{Path: want, Source: "cli", Scope: "temporary", Origin: "top-level"}
			if len(configs) != 1 || configs[0].Source != want || configs[0].SourceInfo != wantInfo {
				t.Fatalf("configs = %+v, want only %s with %+v", configs, want, wantInfo)
			}
		})
	}
}
