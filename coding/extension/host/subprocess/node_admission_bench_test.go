package subprocess

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

func BenchmarkNodeAdmissionStartup(b *testing.B) {
	nodeCellRequireNode(b)
	root, cache := b.TempDir(), b.TempDir()
	var nodes []ExtConfig
	for i := range 3 {
		name := fmt.Sprintf("bench%d", i)
		entry := filepath.Join(root, name+".mjs")
		if err := os.WriteFile(entry, []byte(`export default function(pi) { pi.events.on("bench",()=>{}); }`), 0o644); err != nil {
			b.Fatal(err)
		}
		nodes = append(nodes, ExtConfig{Name: name, Source: entry, Enabled: true})
	}
	native := filepath.Join(root, "native")
	if err := os.Mkdir(native, 0o755); err != nil {
		b.Fatal(err)
	}
	sdk, err := filepath.Abs("../../../../extensions/sdk")
	if err != nil {
		b.Fatal(err)
	}
	mod := fmt.Sprintf("module example.com/bench\n\ngo 1.26.0\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v%s\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n", pigversion.PigVersion, filepath.ToSlash(sdk))
	if err := os.WriteFile(filepath.Join(native, "go.mod"), []byte(mod), 0o644); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(native, "extension.go"), []byte(`package bench
import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
func Extension()*sdk.Extension{return sdk.New("native")}
`), 0o644); err != nil {
		b.Fatal(err)
	}
	mixed := []ExtConfig{nodes[0], packedFactoryConfig("native", native, "example.com/bench", "bench"), nodes[2]}
	for _, tc := range []struct {
		name    string
		configs []ExtConfig
	}{{"one-node", nodes[:1]}, {"three-node", nodes}, {"node-go-node", mixed}} {
		b.Run(tc.name, func(b *testing.B) {
			run := func() {
				h := NewHostWithConfigRoot(root, cache)
				_, errs := h.LoadAll(b.Context(), tc.configs)
				h.Shutdown("benchmark complete")
				if len(errs) > 0 {
					b.Fatal(errs)
				}
			}
			run()
			b.ReportAllocs()
			for b.Loop() {
				run()
			}
		})
	}
}
