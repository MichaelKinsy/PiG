package coding

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkModelAvailabilitySnapshot(b *testing.B) {
	for _, count := range []int{1, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			dir := b.TempDir()
			models := make([]map[string]any, count)
			for i := range count {
				models[i] = map[string]any{"id": fmt.Sprintf("model-%04d", i), "name": "Metadata Model"}
			}
			config := map[string]any{"providers": map[string]any{"availability-bench": map[string]any{"api": "openai-completions", "baseUrl": "https://faux.invalid/v1", "apiKey": "bench-key", "models": models}}}
			data, err := json.Marshal(config)
			if err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "models.json"), data, 0o600); err != nil {
				b.Fatal(err)
			}
			services, err := NewServices(ServicesOptions{AgentDir: dir, CWD: b.TempDir()})
			if err != nil {
				b.Fatal(err)
			}
			defer services.Close()
			runtime := services.ModelRuntime()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				available, err := runtime.GetAvailable(b.Context(), "")
				if err != nil {
					b.Fatal(err)
				}
				selected := 0
				for _, model := range available {
					if model.ProviderMeta.ProviderID == "availability-bench" {
						selected++
					}
				}
				if selected != count {
					b.Fatalf("available=%d want input count%d", selected, count)
				}
			}
		})
	}
}
