package schema_test

// Ports the benchmarks of packages/schema/test/bench: speed (realistic schemas, a valid and an invalid value each), setup (compiling schemas the compiler has not seen, and their first checks) and memory (the heap that distinct compiled schemas retain). The comparison with other libraries (candidates.ts, conformance.ts), the bundle size (size.ts) and the no-eval run have no Go counterpart: Go has no eval and no bundles, and the conformance of this package is TestConformanceSuite.
// Run: go test ./schema -run '^$' -bench . -benchmem

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/schema"
)

// TestCorpusMatchesPi checks the corpus generator against the digest of JSON.stringify(corpus(count, seed)) under Node.
func TestCorpusMatchesPi(t *testing.T) {
	golden := readJSON(t, filepath.Join(fixtureRoot, "errors-golden.json")).(*schema.Object).Value("corpus").(*schema.Object)
	for _, run := range []struct {
		count int
		seed  int32
	}{{200, 42}, {100, 7}, {1000, 99}} {
		items := make([]any, 0, run.count)
		for _, item := range corpus(run.count, run.seed) {
			items = append(items, chordjson.ObjectOf("schema", item.schema, "value", item.value))
		}
		text, err := chordjson.Marshal(items)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(text)
		key := fmt.Sprintf("%d:%d", run.count, run.seed)
		if got, want := hex.EncodeToString(sum[:]), golden.Value(key); got != want {
			t.Errorf("corpus(%s) digest %s, Pi's %v", key, got, want)
		}
	}
}

func TestCorpusValuesAreValid(t *testing.T) {
	for i, item := range corpus(300, 5) {
		validator, err := schema.Compile(item.schema, schema.CompileOptions{})
		if err != nil {
			t.Fatalf("item %d: %v", i, err)
		}
		if !(checked{validator, t}).Check(item.value) {
			t.Fatalf("item %d: %v rejects its value: %v", i, jsonText(t, item.schema), checked{validator, t}.Errors(item.value))
		}
	}
}

// BenchmarkCheck is the speed benchmark: ns per check of each case, valid and invalid.
func BenchmarkCheck(b *testing.B) {
	golden := readJSON(b, filepath.Join(fixtureRoot, "errors-golden.json")).(*schema.Object)
	for _, c := range golden.Value("cases").([]any) {
		row := c.(*schema.Object)
		validator, err := schema.Compile(row.Value("schema"), schema.CompileOptions{Draft: schema.Draft202012})
		if err != nil {
			b.Fatal(err)
		}
		for _, input := range []struct {
			name, value string
			valid       bool
		}{{"valid", "validValue", true}, {"invalid", "invalidValue", false}} {
			value := row.Value(input.value)
			if (checked{validator, b}).Check(value) != input.valid {
				b.Fatalf("%s (%s) is wrong", row.Value("name"), input.name)
			}
			b.Run(row.Value("name").(string)+"/"+input.name, func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					_, _ = validator.Check(value)
				}
			})
		}
	}
}

// BenchmarkSetup is the setup benchmark: compile a schema not seen before, then its first check.
func BenchmarkSetup(b *testing.B) {
	for _, item := range corpus(200, 7) {
		validator, err := schema.Compile(item.schema, schema.CompileOptions{})
		if err != nil {
			b.Fatal(err)
		}
		for range 10 {
			_, _ = validator.Check(item.value)
		}
	}
	items := corpus(1000, 99)
	b.Run("compile", func(b *testing.B) {
		b.ReportAllocs()
		for i := range b.N {
			if _, err := schema.Compile(items[i%len(items)].schema, schema.CompileOptions{}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("compile and first check", func(b *testing.B) {
		b.ReportAllocs()
		for i := range b.N {
			item := items[i%len(items)]
			validator, err := schema.Compile(item.schema, schema.CompileOptions{})
			if err != nil {
				b.Fatal(err)
			}
			_, _ = validator.Check(item.value)
		}
	})
}

// BenchmarkMemory is the memory benchmark: the heap 3,200 distinct compiled schemas retain after compiling and after checking each value 3 times.
func BenchmarkMemory(b *testing.B) {
	const count = 3200
	heap := func() uint64 {
		var stats runtime.MemStats
		for range 4 {
			runtime.GC()
		}
		runtime.ReadMemStats(&stats)
		return stats.HeapAlloc
	}
	// Warm the library's own code on another corpus, so its one-time tables and caches are not counted.
	for _, item := range corpus(100, 7) {
		validator, err := schema.Compile(item.schema, schema.CompileOptions{})
		if err != nil {
			b.Fatal(err)
		}
		for range 4 {
			_, _ = validator.Check(item.value)
		}
	}
	for range b.N {
		items := corpus(count, 42)
		base := heap()
		prepared := make([]*schema.Validator, len(items))
		for i, item := range items {
			validator, err := schema.Compile(item.schema, schema.CompileOptions{})
			if err != nil {
				b.Fatal(err)
			}
			prepared[i] = validator
		}
		compiled := heap()
		for i, item := range items {
			for range 3 {
				_, _ = prepared[i].Check(item.value)
			}
		}
		checked := heap()
		b.ReportMetric(float64(compiled-base)/1048576, "MB-compiled")
		b.ReportMetric(float64(checked-base)/1048576, "MB-checked")
		runtime.KeepAlive(items)
		runtime.KeepAlive(prepared)
	}
}
