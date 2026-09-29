package coding

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func BenchmarkSessionScopedModelsGetter(b *testing.B) {
	for _, size := range []int{0, 64, 1024} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			s := &Session{}
			models := make([]extension.ScopedModel, size)
			for i := range models {
				models[i] = extension.ScopedModel{Model: &ai.Model{ID: fmt.Sprint(i)}, ThinkingLevel: "high"}
			}
			s.SetScopedModels(models)
			r := inproc.NewRunner(nil, ".")
			r.BindScopedModels(s.ScopedModels)
			ctx := r.CreateCommandContext()
			b.ReportAllocs()
			for b.Loop() {
				got, err := ctx.ScopedModels()
				if err != nil || len(got) != size {
					b.Fatal(got, err)
				}
			}
		})
	}
}
