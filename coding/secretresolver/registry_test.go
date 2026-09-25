package secretresolver

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestRegistryRejectsMissingAndDuplicateResolvers(t *testing.T) {
	mu.Lock()
	delete(resolvers, "fixture")
	delete(resolvers, "fixture-denied")
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		delete(resolvers, "fixture")
		delete(resolvers, "fixture-denied")
		mu.Unlock()
	})
	if _, err := Resolve(context.Background(), "missing:id"); err == nil || !strings.Contains(err.Error(), "install the package") {
		t.Fatalf("missing resolver error = %v", err)
	}
	if err := Register("fixture", func(_ context.Context, opaque string) ([]byte, error) { return []byte("value-" + opaque), nil }); err != nil {
		t.Fatal(err)
	}
	if err := Register("fixture", func(context.Context, string) ([]byte, error) { return nil, nil }); err == nil {
		t.Fatal("duplicate resolver registered")
	}
	if err := Register("fixture-denied", func(context.Context, string) ([]byte, error) { return nil, fmt.Errorf("SUPER-SECRET resolver detail") }); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(context.Background(), "fixture-denied:key"); err == nil || strings.Contains(err.Error(), "SUPER-SECRET") || !strings.Contains(err.Error(), "denied or failed") {
		t.Fatalf("redacted error = %v", err)
	}
	value, err := Resolve(context.Background(), "fixture:key")
	if err != nil || string(value) != "value-key" {
		t.Fatalf("value=%q err=%v", value, err)
	}
}
