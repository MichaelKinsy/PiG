package installresolver

import (
	"errors"
	"io"
	"testing"
)

func resetForTest(t *testing.T) {
	t.Helper()
	registry.Lock()
	oldResolvers := registry.pigletSource
	oldSchemes := registry.sourceSchemes
	oldInstaller := registry.installer
	oldMaterializer := registry.materializer
	registry.pigletSource = nil
	registry.sourceSchemes = nil
	registry.installer = nil
	registry.materializer = nil
	registry.Unlock()
	t.Cleanup(func() {
		registry.Lock()
		registry.pigletSource = oldResolvers
		registry.sourceSchemes = oldSchemes
		registry.installer = oldInstaller
		registry.materializer = oldMaterializer
		registry.Unlock()
	})
}

func TestRegisterSourceScheme(t *testing.T) {
	resetForTest(t)
	if err := RegisterSourceScheme("marketplace"); err != nil {
		t.Fatalf("register: %v", err)
	}
	if !SupportsSourceScheme("marketplace") {
		t.Fatal("registered scheme not supported")
	}
	if SupportsSourceScheme("unknown") {
		t.Fatal("unknown scheme reported supported")
	}
	if err := RegisterSourceScheme("marketplace"); err == nil {
		t.Fatal("duplicate scheme accepted")
	}
	for _, invalid := range []string{"", "Upper", "-bad", "bad_underscore"} {
		if err := RegisterSourceScheme(invalid); err == nil {
			t.Fatalf("invalid scheme %q accepted", invalid)
		}
	}
}

func TestPigletSourceResolverUsesExactRegisteredScheme(t *testing.T) {
	resetForTest(t)
	resolver := func(cwd, locator string) (string, error) {
		if cwd != "/cwd" || locator != "team/review" {
			t.Fatalf("resolver input = %q/%q", cwd, locator)
		}
		return "/materialized/review.yaml", nil
	}
	if err := RegisterPigletSourceResolver("marketplace", resolver); err == nil {
		t.Fatal("resolver registered before source scheme")
	}
	if err := RegisterSourceScheme("marketplace"); err != nil {
		t.Fatal(err)
	}
	if err := RegisterPigletSourceResolver("marketplace", resolver); err != nil {
		t.Fatal(err)
	}
	if err := RegisterPigletSourceResolver("marketplace", resolver); err == nil {
		t.Fatal("duplicate resolver accepted")
	}
	root, err := ResolvePigletSource("/cwd", "marketplace:team/review")
	if err != nil || root != "/materialized/review.yaml" {
		t.Fatalf("resolve = %q, %v", root, err)
	}
	if _, err := ResolvePigletSource("/cwd", "unknown:thing"); err == nil {
		t.Fatal("unknown scheme resolved")
	}
}

func TestMaterializerCallback(t *testing.T) {
	resetForTest(t)
	if _, err := Materialize("/cwd", "src", "user", io.Discard, io.Discard); err == nil {
		t.Fatal("Materialize without a registered callback must fail")
	}
	want := errors.New("sentinel")
	SetMaterializer(func(_, _, _ string, _, _ io.Writer) (string, error) { return "", want })
	if _, err := Materialize("/cwd", "src", "user", io.Discard, io.Discard); !errors.Is(err, want) {
		t.Fatalf("Materialize = %v, want sentinel", err)
	}
}

func TestInstallerCallback(t *testing.T) {
	resetForTest(t)
	if err := Install("/cwd", "src", "user", io.Discard, io.Discard); err == nil {
		t.Fatal("Install without a registered installer must fail")
	}
	want := errors.New("sentinel")
	SetInstaller(func(_, _, _ string, _, _ io.Writer) error { return want })
	if err := Install("/cwd", "src", "user", io.Discard, io.Discard); !errors.Is(err, want) {
		t.Fatalf("Install = %v, want sentinel", err)
	}
}
