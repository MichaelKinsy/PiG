//go:build !pig_strip_extension_sdk_go

package cli

import (
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/extension/pigsdk"
)

// pig divergence (D272): the analysis stages the Go SDK first, and a config root whose staged SDK cannot be written fails it with the staging error, before any go
// command runs. The config root here is a file, so no directory of the SDK can be made below it.
func TestAnalysisFailsWithTheStagingErrorWhenTheStagedSDKCannotBeWritten(t *testing.T) {
	configRoot := filepath.Join(t.TempDir(), "config")
	writeTestFile(t, configRoot, "a file where the config root should be")
	staging := pigsdk.EnsureSyncedLang(configRoot, "go")
	if staging == nil {
		t.Fatal("the Go SDK staged below a config root that is a file")
	}
	result, err := newExtensionUpgrader(configRoot).analyzeExtension(t.Context(), subprocess.ExtConfig{Name: "demo", Source: t.TempDir(), RuntimeLanguage: "go"})
	if err == nil || err.Error() != staging.Error() || result != nil {
		t.Fatalf("analysis = %v, %v; want no plan and the staging error %q", result, err, staging)
	}
}
