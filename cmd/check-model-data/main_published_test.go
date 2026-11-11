package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/modelgen/modeldata"
)

// TestPublishedModelDataValidates runs the validator over the real published data: the mirror's generated aggregator
// and provider shards with the published package's data files and hidden manifest (structure hash and file hashes).
func TestPublishedModelDataValidates(t *testing.T) {
	mirror := filepath.Join("..", "..", ".upstream", "v"+pigversion.UpstreamVersion, "packages", "ai", "src")
	published := filepath.Join("..", "..", "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent", "node_modules", "@earendil-works", "pi-ai", "dist", "providers", "data")
	for _, path := range []string{filepath.Join(mirror, "models.generated.ts"), filepath.Join(published, modeldata.ModelDataManifestFile)} {
		if _, err := os.Stat(path); err != nil {
			t.Skipf("pinned %s sources are not installed: %v", pigversion.UpstreamVersion, err)
		}
	}
	root := t.TempDir()
	providers := filepath.Join(root, "src", "providers")
	if err := os.MkdirAll(providers, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(providers, "data"), os.DirFS(published)); err != nil {
		t.Fatal(err)
	}
	copyFile := func(from, to string) {
		data, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(to, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	copyFile(filepath.Join(mirror, "models.generated.ts"), filepath.Join(root, "src", "models.generated.ts"))
	shards, err := filepath.Glob(filepath.Join(mirror, "providers", "*.models.ts"))
	if err != nil || len(shards) == 0 {
		t.Fatalf("no provider shards in the mirror: %v", err)
	}
	for _, shard := range shards {
		copyFile(shard, filepath.Join(providers, filepath.Base(shard)))
	}
	if err := modeldata.ValidateGeneratedModelData(root); err != nil {
		t.Fatal(err)
	}
	// check-model-data.ts prints one success line and exits 0 on valid data.
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-root", root}, &stdout, &stderr); code != 0 || stdout.String() != "Generated model data is valid.\n" || stderr.Len() != 0 {
		t.Fatalf("run = %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	// The same data must fail once one model's type/id identity is broken, so the check above is not vacuous.
	data, err := os.ReadFile(filepath.Join(providers, "data", "together.json"))
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(data), `"chat:moonshotai/Kimi-K3"`, `"chat:moonshotai/Kimi-K3-renamed"`, 1)
	if broken == string(data) {
		t.Skip("together.json has no Kimi K3 to break")
	}
	if err := os.WriteFile(filepath.Join(providers, "data", "together.json"), []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := modeldata.ValidateGeneratedModelData(root); err == nil || !strings.Contains(err.Error(), "type/id identity") {
		t.Fatalf("broken identity error = %v", err)
	}
}
