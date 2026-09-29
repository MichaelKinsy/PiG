package extensionconformance

import (
	"os"
	"testing"
)

func TestSDKFixtureBuildsHavePackageLifetime(t *testing.T) {
	for _, tc := range []struct {
		name, env string
		build     func(*testing.T) string
	}{
		{"go", "PIG_TEST_CONFORMANCE_SDK_FIXTURE_BIN", buildSDKFixture},
		{"rust", "PIG_TEST_RUST_SDK_FIXTURE_BIN", buildRustSDKFixture},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.env, "")
			var first string
			t.Run("first-consumer", func(t *testing.T) { first = tc.build(t) })
			if _, err := os.Stat(first); err != nil {
				t.Fatalf("fixture must outlive its first consumer: %v", err)
			}
			t.Run("next-consumer", func(t *testing.T) {
				t.Setenv("PATH", t.TempDir())
				if got := tc.build(t); got != first {
					t.Fatalf("fixture rebuilt: %q, want %q", got, first)
				}
			})
		})
	}
}

func TestPackedUIFixturesReuseArtifacts(t *testing.T) {
	for _, language := range []string{"go", "python", "rust"} {
		t.Run(language, func(t *testing.T) {
			for i := range 2 {
				t.Run([]string{"first-consumer", "next-consumer"}[i], func(t *testing.T) {
					h := makePackedUIHarness(t, language)
					t.Cleanup(func() { h.host.Shutdown("test done") })
					if i > 0 && !h.host.LastReloadReport().Cells[0].Cached {
						t.Fatal("identical packed fixture rebuilt for its next consumer")
					}
				})
			}
		})
	}
}
