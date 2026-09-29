package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi package-manager.ts:1271-1294 skips a source whenever offline installation would be required, including a stale or malformed cache, while retaining compatible cached Packages.
func TestTemporaryNpmOfflineCacheResolution(t *testing.T) {
	for _, tc := range []struct {
		name, source, packageName, manifest string
		wantCache                           bool
	}{
		{"missing", "npm:review-missing", "review-missing", "", false},
		{"scoped missing", "npm:@scope/review-missing", "@scope/review-missing", "", false},
		{"matching pin", "npm:example@1.0.0", "example", `{"version":"1.0.0","pi":{"extensions":["extension.ts"]}}`, true},
		{"mismatched pin", "npm:example@2.0.0", "example", `{"version":"1.0.0","pi":{"extensions":["extension.ts"]}}`, false},
		{"matching range", "npm:example@^1.0.0", "example", `{"version":"1.2.0","pi":{"extensions":["extension.ts"]}}`, true},
		{"mismatched range", "npm:example@^2.0.0", "example", `{"version":"1.2.0","pi":{"extensions":["extension.ts"]}}`, false},
		{"tag cache", "npm:example@latest", "example", `{"version":"1.0.0","pi":{"extensions":["extension.ts"]}}`, true},
		{"unpinned cache", "npm:example", "example", `{"version":"1.0.0","pi":{"extensions":["extension.ts"]}}`, true},
		{"missing version", "npm:example", "example", `{"pi":{"extensions":["extension.ts"]}}`, false},
		{"malformed manifest", "npm:example", "example", `{invalid`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PI_OFFLINE", "1")
			t.Setenv("PIG_OFFLINE", "")
			cwd, agentDir := t.TempDir(), t.TempDir()
			sm := codingagent.NewSettingsManager(cwd, agentDir)
			// Any attempted npm execution fails rather than reaching an external registry.
			if err := sm.SetNpmCommand([]string{filepath.Join(t.TempDir(), "must-not-run")}); err != nil {
				t.Fatal(err)
			}
			cached := temporaryNpmCacheFixturePath(agentDir, tc.packageName)
			if tc.manifest != "" {
				writePackageResource(t, filepath.Join(cached, "package.json"), tc.manifest)
				writePackageResource(t, filepath.Join(cached, "extension.ts"), "export default function() {};")
			}
			resolved, err := resolveCLIExtensionSource(cwd, agentDir, sm, tc.source, nil)
			want := ""
			if tc.wantCache {
				want = cached
			}
			if err != nil || resolved != want {
				t.Errorf("offline resolver=%q, %v; want %q, nil", resolved, err, want)
			}
			// A skipped Package must not become an unresolved -e config, and must not discard a valid sibling.
			local := filepath.Join(cwd, "local.ts")
			writePackageResource(t, local, "export default function() {};")
			configs := collectExtensionConfigs(cwd, agentDir, sm, CLIFlags{Extensions: []string{tc.source, local}, NoExtensions: true}, nil)
			wantSources := []string{local}
			if tc.wantCache {
				wantSources = []string{filepath.Join(cached, "extension.ts"), local}
			}
			if len(configs) != len(wantSources) {
				t.Fatalf("offline collection=%+v, want sources=%q", configs, wantSources)
			}
			for i, source := range wantSources {
				if configs[i].Source != source || configs[i].ResolveError() != nil {
					t.Errorf("config[%d]=%+v error=%v, want source=%q", i, configs[i], configs[i].ResolveError(), source)
				}
			}
			if tc.manifest != "" {
				data, err := os.ReadFile(filepath.Join(cached, "package.json"))
				if err != nil || string(data) != tc.manifest {
					t.Fatalf("offline resolution modified cache: %q, %v", data, err)
				}
			}
		})
	}
}

// Pi package-manager.ts:getTemporaryDir hashes "npm-" for the shared temporary npm prefix.
func temporaryNpmCacheFixturePath(agentDir, name string) string {
	digest := sha256.Sum256([]byte("npm-"))
	return filepath.Join(agentDir, "tmp", "extensions", "npm", fmt.Sprintf("%x", digest[:4]), "node_modules", filepath.FromSlash(name))
}

func TestRPCOfflineMissingTemporaryNpmExtensionIsSkipped(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	p := startRPCProcessAt(t, root, []string{
		"HOME=" + root, "PIG_HOME=" + filepath.Join(root, "pig"),
		"PIG_CODING_AGENT_DIR=" + agentDir, "PI_CODING_AGENT_DIR=" + agentDir,
		"PI_OFFLINE=1", "PIG_OFFLINE=1",
	}, "--offline", "--no-session", "--no-extensions", "-e", "npm:review-missing", "--model", "anthropic/claude-sonnet-4-5")
	p.send(`{"id":"state","type":"get_state"}`)
	p.await("state after optional offline npm cache miss", func(record rpcRecord) bool {
		if record["type"] != "response" || record["id"] != "state" {
			return false
		}
		if record["success"] != true {
			t.Fatalf("get_state failed: %+v\n%s", record, p.stderr.String())
		}
		return true
	})
	p.closeInput()
	p.waitForExit("offline npm cache miss")
	if _, err := os.Stat(temporaryNpmCacheFixturePath(agentDir, "review-missing")); !os.IsNotExist(err) {
		t.Fatalf("offline startup installed a missing Package: %v", err)
	}
}

func BenchmarkTemporaryNpmOfflineCacheMiss(b *testing.B) {
	b.Setenv("PI_OFFLINE", "1")
	b.Setenv("PIG_OFFLINE", "")
	cwd, agentDir := b.TempDir(), b.TempDir()
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.SetNpmCommand([]string{filepath.Join(b.TempDir(), "must-not-run")}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		resolved, err := resolveCLIExtensionSource(cwd, agentDir, sm, "npm:review-missing", nil)
		if err != nil || resolved != "" {
			b.Fatalf("offline resolver=%q, %v", resolved, err)
		}
	}
}
