package main

import (
	"os"
	"testing"
)

// Upstream main.ts exports offline mode as PI_OFFLINE=1 and
// PI_SKIP_VERSION_CHECK=1, which Pi extensions read: pi-auto-update skips its
// `pi update` run when PI_OFFLINE is set. PiG's --offline and PIG_OFFLINE must
// reach extensions the same way.
func TestOfflineModeIsExportedToExtensions(t *testing.T) {
	cases := []struct {
		name    string
		flag    bool
		env     map[string]string
		offline bool
	}{
		{name: "flag", flag: true, offline: true},
		{name: "PIG_OFFLINE", env: map[string]string{"PIG_OFFLINE": "1"}, offline: true},
		{name: "PIG_OFFLINE yes", env: map[string]string{"PIG_OFFLINE": "Yes"}, offline: true},
		{name: "PI_OFFLINE true", env: map[string]string{"PI_OFFLINE": "true"}, offline: true},
		{name: "PIG_OFFLINE off", env: map[string]string{"PIG_OFFLINE": "0"}},
		{name: "none"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range []string{"PI_OFFLINE", "PIG_OFFLINE", "PI_SKIP_VERSION_CHECK"} {
				t.Setenv(key, "")
				_ = os.Unsetenv(key)
			}
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			exportOfflineMode(tc.flag)
			if tc.offline {
				if os.Getenv("PI_OFFLINE") != "1" || os.Getenv("PI_SKIP_VERSION_CHECK") != "1" {
					t.Fatalf("PI_OFFLINE=%q PI_SKIP_VERSION_CHECK=%q, want both 1", os.Getenv("PI_OFFLINE"), os.Getenv("PI_SKIP_VERSION_CHECK"))
				}
				return
			}
			if _, set := os.LookupEnv("PI_OFFLINE"); set {
				t.Fatalf("PI_OFFLINE set to %q without offline mode", os.Getenv("PI_OFFLINE"))
			}
		})
	}
}
