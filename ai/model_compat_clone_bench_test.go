package ai_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/catalogfixture"
)

// BenchmarkCloneCatalogCompat measures cloneCompat over the compat of every entry of the synthetic catalog of internal/catalogfixture, sized and shaped like the shipped one on 2026-11-16, so a catalog refresh does not move its row.
func BenchmarkCloneCatalogCompat(b *testing.B) {
	compats := catalogfixture.Compats()
	b.ReportAllocs()
	for b.Loop() {
		for _, compat := range compats {
			_ = ai.CloneCompat(compat)
		}
	}
}
