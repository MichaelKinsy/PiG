package subprocess

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
)

// This package does not link pigsdk, so its tests register fixed SDK bundles to resolve staged SDK directories.
func init() {
	hash := func() (string, error) { return "0123456789abcdef", nil }
	pigsdklock.RegisterSDK("go", "sdk", hash)
	pigsdklock.RegisterSDK("python", "sdk-py", hash)
	pigsdklock.RegisterSDK("rust", "sdk-rs", hash)
}

func stagedSDKDir(t testing.TB, configRoot, lang string) string {
	t.Helper()
	dir, err := pigsdklock.StagedSDKDir(configRoot, lang)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
