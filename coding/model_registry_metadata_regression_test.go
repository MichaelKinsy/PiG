package coding

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/benchalloc"
)

// BenchmarkModelRegistryMetadata lists the models of a registry built from the live catalog. Its allocs/op is allocations per 1,000 listed models, measured over the loop by runtime.ReadMemStats (internal/benchalloc), so a refresh that adds entries does not move the row while a model that costs more to list does.
func BenchmarkModelRegistryMetadata(b *testing.B) {
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: b.TempDir(), AgentDir: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	listed := len(services.ModelRuntime().GetModels())
	if listed == 0 {
		b.Fatal("empty catalog")
	}
	b.Run("per-1000-entries", func(b *testing.B) {
		benchalloc.Per1000Entries(b, listed, func() {
			if len(services.ModelRuntime().GetModels()) != listed {
				b.Fatal("catalog changed size")
			}
		})
	})
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
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
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
