//go:build !pig_strip_self_update

package cli

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestNativeSelfUpdateCallerRetainsProvenPrefix(t *testing.T) {
	bin := t.TempDir()
	manager := filepath.Join(bin, "npm")
	if runtime.GOOS == "windows" {
		manager += ".exe"
	}
	copyTestBinary(t, manager)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	log := filepath.Join(t.TempDir(), "manager.log")
	t.Setenv(managerLogEnv, log)
	prefix := filepath.Join(t.TempDir(), "prefix with spaces")
	installed := filepath.Join(prefix, "lib", "node_modules", "@old-scope", "pi", "bin")
	if err := os.MkdirAll(installed, 0o755); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(installed, "pig")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	copyTestBinary(t, executable)
	server := signedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"version":"9.9.9","packageName":"@new-scope/pi","binaries":{}}`)
	}))
	defer server.Close()
	t.Setenv("PIG_UPDATE_URL", server.URL)
	provenance := &codingagent.SelfUpdateProvenance{Tier: codingagent.TierPackageManager, ExePath: executable, PackageOwner: "npm", PackageName: "@old-scope/pi", PackageDir: filepath.Dir(installed), NpmPrefix: prefix}
	if err := applyPackageManagerUpdate(provenance, nil, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	want := "--prefix " + prefix + " uninstall -g @old-scope/pi\n--prefix " + prefix + " install -g --ignore-scripts --min-release-age=0 @new-scope/pi@9.9.9\n"
	if string(data) != want {
		t.Fatalf("native update argv=%q want=%q", data, want)
	}
}
