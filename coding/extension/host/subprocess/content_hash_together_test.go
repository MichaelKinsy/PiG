package subprocess

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// The extensions of a load are hashed together, not one after another: every fill waits here until all of them have started, which a serial loop
// never reaches. Each fill writes only its own config, and the configs keep their order.
func TestFillContentHashesHashesTheExtensionsTogether(t *testing.T) {
	count := min(4, maxConcurrentCellPreparations())
	if count < 2 {
		t.Skip("one preparation slot")
	}
	configs := make([]ExtConfig, count+1)
	for i := range configs {
		configs[i] = ExtConfig{Name: fmt.Sprintf("ext%d", i), Enabled: true}
	}
	// A disabled extension is not hashed.
	configs[count].Enabled = false
	var (
		mu      sync.Mutex
		started int
	)
	allStarted := make(chan struct{})
	h := &Host{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.fillContentHashes(configs, func(cfg *ExtConfig) {
			mu.Lock()
			started++
			if started == count {
				close(allStarted)
			}
			mu.Unlock()
			select {
			case <-allStarted:
			case <-time.After(30 * time.Second):
				t.Errorf("%s: the other extensions were not hashed together with it", cfg.Name)
			}
			cfg.ContentHash = "hash-of-" + cfg.Name
		})
	}()
	<-done
	for i, cfg := range configs {
		want := "hash-of-" + cfg.Name
		if i == count {
			want = ""
		}
		if cfg.ContentHash != want {
			t.Errorf("config %d (%s) has ContentHash %q, want %q", i, cfg.Name, cfg.ContentHash, want)
		}
		if cfg.Name != fmt.Sprintf("ext%d", i) {
			t.Errorf("config %d is %s: the order changed", i, cfg.Name)
		}
	}
}

// goExtensionSources writes count Go extensions that replace the pig SDK, as an extension of the SDK's own examples does.
func goExtensionSources(tb testing.TB, count int) []ExtConfig {
	tb.Helper()
	sdk, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "extensions", "sdk"))
	if err != nil {
		tb.Fatal(err)
	}
	configs := make([]ExtConfig, count)
	for i := range configs {
		name := fmt.Sprintf("hashed%d", i)
		dir := tb.TempDir()
		files := map[string]string{
			"go.mod":  "module " + name + "\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => " + sdk + "\n",
			"main.go": "package main\n\nfunc main() {}\n",
		}
		for file, content := range files {
			if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
				tb.Fatal(err)
			}
		}
		configs[i] = ExtConfig{Name: name, Source: dir, Enabled: true, RuntimeKind: "subprocess", RuntimeLanguage: "go", EntrypointKind: "factory", Factory: "Extension", ModulePath: name}
	}
	return configs
}

func BenchmarkFillContentHashesGoExtensions(b *testing.B) {
	for _, count := range []int{1, 4, 16} {
		for _, mode := range []string{"one at a time", "together"} {
			b.Run(fmt.Sprintf("%d extensions, %s", count, mode), func(b *testing.B) {
				configs := goExtensionSources(b, count)
				h := &Host{}
				b.ReportAllocs()
				for b.Loop() {
					for i := range configs {
						configs[i].ContentHash = ""
					}
					if mode == "together" {
						h.fillContentHashes(configs, fillContentHash)
						continue
					}
					for i := range configs {
						fillContentHash(&configs[i])
					}
				}
			})
		}
	}
}
