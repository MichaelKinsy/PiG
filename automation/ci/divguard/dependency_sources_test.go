package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func dependencyProofFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	mirror := filepath.Join(root, ".upstream", "current")
	source := []byte("function buildConnector() { const timeout = 10e3; return timeout; }\n")
	digest := sha256.Sum256(source)
	records := []map[string]string{{"reference": "node_modules/undici/lib/core/connect.js", "manifest": "packages/coding-agent/package.json", "package": "undici", "version": "8.10.2", "source": "test/parity/dependency-sources/connect.js", "sha256": hex.EncodeToString(digest[:]), "integrity": "sha512-" + strings.Repeat("A", 86) + "=="}}
	raw, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		".upstream/current/packages/coding-agent/package.json": []byte(`{"dependencies":{"undici":"8.10.2"}}`),
		"test/parity/dependency-sources/connect.js":            source, "test/parity/dependency-sources.json": raw,
	}
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root, mirror
}

func TestDependencyLiteralProofRequiresPinnedIntactSource(t *testing.T) {
	root, mirror := dependencyProofFixture(t)
	environment, err := loadEnv(root, mirror)
	if err != nil {
		t.Fatal(err)
	}
	source := []byte("package ai\nimport \"time\"\n// upstream: node_modules/undici/lib/core/connect.js:buildConnector\nconst connectTimeout = 10 * time.Second\n")
	hits, problems, err := scanSource("ai/transport.go", source, environment, []check{magicLiteral})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 || len(problems) != 0 {
		t.Fatalf("verified dependency rejected: hits=%v problems=%v", hits, problems)
	}
	// A matching dependency cannot allow another duration merely because it is registered.
	wrong := []byte(strings.ReplaceAll(string(source), "10 * time.Second", "11 * time.Second"))
	hits, _, err = scanSource("ai/transport.go", wrong, environment, []check{magicLiteral})
	if err != nil || len(hits) != 1 {
		t.Fatalf("wrong value passed: hits=%v error=%v", hits, err)
	}
	for _, tc := range []struct{ name, path, body string }{
		{"changed source", "test/parity/dependency-sources/connect.js", "function buildConnector() { return 10001; }"},
		{"changed pin", ".upstream/current/packages/coding-agent/package.json", `{"dependencies":{"undici":"8.10.3"}}`},
		{"unpinned range", ".upstream/current/packages/coding-agent/package.json", `{"dependencies":{"undici":"^8.10.2"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, mirror := dependencyProofFixture(t)
			if err := os.WriteFile(filepath.Join(root, tc.path), []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadEnv(root, mirror); err == nil {
				t.Fatal("stale dependency proof accepted")
			}
		})
	}
}

// A transitive dependency is admitted through the root lockfile entry, whose version and archive integrity must both match the record.
func TestDependencyLiteralProofAcceptsALockfilePin(t *testing.T) {
	const integrity = "sha512-CsOeKq/9kA3y6VJHt+/+VTCtBaxJ4OTFpgrjIUhPpDIKxBci1k2bJaQASF2h/ELWrulGp+t97DZ0mevfAD8idg=="
	fixture := func(t *testing.T, lockfile string) (string, string) {
		root, mirror := dependencyProofFixture(t)
		source := []byte("const MAXIMUM_RETRY_DELAY = 20 * 1000;\n")
		digest := sha256.Sum256(source)
		raw, err := json.Marshal([]map[string]string{{"reference": "node_modules/@smithy/core/dist-cjs/submodules/retry/index.js", "manifest": "package-lock.json", "package": "@smithy/core", "version": "3.33.3",
			"source": "test/parity/dependency-sources/retry.js", "sha256": hex.EncodeToString(digest[:]), "integrity": integrity}})
		if err != nil {
			t.Fatal(err)
		}
		for name, body := range map[string][]byte{"test/parity/dependency-sources/retry.js": source, "test/parity/dependency-sources.json": raw, ".upstream/current/package-lock.json": []byte(lockfile)} {
			if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), body, 0600); err != nil {
				t.Fatal(err)
			}
		}
		return root, mirror
	}
	locked := `{"packages":{"node_modules/@smithy/core":{"version":"3.33.3","integrity":"` + integrity + `"}}}`
	environment, err := loadEnv(fixture(t, locked))
	if err != nil {
		t.Fatal(err)
	}
	source := []byte("package ai\n// upstream: node_modules/@smithy/core/dist-cjs/submodules/retry/index.js:MAXIMUM_RETRY_DELAY\nconst maxRetryDelay = 20 * 1000\n")
	hits, problems, err := scanSource("ai/retry.go", source, environment, []check{magicLiteral})
	if err != nil || len(hits) != 0 || len(problems) != 0 {
		t.Fatalf("verified lockfile dependency rejected: hits=%v problems=%v error=%v", hits, problems, err)
	}
	for name, lockfile := range map[string]string{
		"other version":   strings.Replace(locked, "3.33.3", "3.35.1", 1),
		"other integrity": strings.Replace(locked, "sha512-Cs", "sha512-Ds", 1),
		"not locked":      `{"packages":{}}`,
	} {
		if _, err := loadEnv(fixture(t, lockfile)); err == nil {
			t.Fatalf("%s: stale lockfile proof accepted", name)
		}
	}
}
