package coding

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func BenchmarkModelRegistryMetadata(b *testing.B) {
	services, err := NewServices(ServicesOptions{CWD: b.TempDir(), AgentDir: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if len(services.ModelRuntime().GetModels()) == 0 {
			b.Fatal("empty catalog")
		}
	}
}

func TestModelRegistryMetadataDoesNotResolveCommandKeys(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1932,1979: status and availability do not execute command-backed keys.
	dir := t.TempDir()
	counter := filepath.Join(dir, "counter")
	if err := os.WriteFile(counter, []byte("0"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := `!sh -c 'count=$(cat "` + counter + `"); echo $((count + 1)) > "` + counter + `"; echo key-value'`
	config := `{"providers":{"custom-provider":{"baseUrl":"https://example.test/v1","api":"openai-completions","apiKey":` + strconv.Quote(command) + `,"models":[{"id":"test-model"}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(ServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	_ = services.ModelRuntime().GetModels()
	_ = services.Registry().GetAll()
	_ = services.Registry().RuntimeModels()
	_ = services.Registry().GetAvailable()
	_ = services.Registry().GetProviderAuthStatus("custom-provider")
	data, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "0" {
		t.Fatalf("metadata lookup executed configured command %s times", strings.TrimSpace(string(data)))
	}
}
