package extensionconformance

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildSDKFixtureUsesConformancePrebuild(t *testing.T) {
	dir := t.TempDir()
	host := filepath.Join(dir, "host-sdk-fixture")
	conformance := filepath.Join(dir, "conformance-sdk-fixture")
	for _, path := range []string{host, conformance} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PIG_TEST_SDK_FIXTURE_BIN", host)
	t.Setenv("PIG_TEST_CONFORMANCE_SDK_FIXTURE_BIN", conformance)
	if got := buildSDKFixture(t); got != conformance {
		t.Fatalf("buildSDKFixture = %q, want conformance fixture %q", got, conformance)
	}
}
