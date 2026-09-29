package jsonparse

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkSyntaxError(b *testing.B) {
	for _, size := range []int{0, 4096, 51200} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			input := []byte(`{"text":"` + strings.Repeat("x", size) + `",}`)
			b.ReportAllocs()
			b.SetBytes(int64(len(input)))
			for b.Loop() {
				if SyntaxError(input) == nil {
					b.Fatal("missing diagnostic")
				}
			}
		})
	}
}
