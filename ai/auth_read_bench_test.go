package ai

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func newBenchAuthStorage(tb testing.TB) *AuthStorage {
	tb.Helper()
	path := filepath.Join(tb.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"anthropic":{"type":"api_key","key":"k"}}`), 0o600); err != nil {
		tb.Fatal(err)
	}
	storage, err := NewAuthStorage(path)
	if err != nil {
		tb.Fatal(err)
	}
	return storage
}

// A credential read finds auth.json unchanged and answers from the loaded copy: it costs one stat. Startup makes one such read per provider.
func BenchmarkAuthStorageReadUnchanged(b *testing.B) {
	storage := newBenchAuthStorage(b)
	ctx := context.Background()
	if _, err := storage.Read(ctx, "anthropic"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := storage.Read(ctx, "anthropic"); err != nil {
			b.Fatal(err)
		}
	}
}
