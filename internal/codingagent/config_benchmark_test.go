package codingagent

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func BenchmarkNativeSelfUpdateOwnership(b *testing.B) {
	prefix := b.TempDir()
	root := filepath.Join(prefix, "lib", "node_modules")
	bin := filepath.Join(root, "@earendil-works", "pi-coding-agent", "dist")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		b.Fatal(err)
	}
	executable := filepath.Join(bin, "pig")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	if err := os.WriteFile(executable, []byte("native fixture"), 0o755); err != nil {
		b.Fatal(err)
	}
	b.Setenv("PIG_INSTALL_TIER", "")
	runner := fakeCmdRunner{outputs: map[string]string{"npm root -g": root}}
	target := SelfUpdatePackageTarget{PackageName: "@earendil-works/pi-coding-agent", InstallSpec: "@earendil-works/pi-coding-agent@1.2.3"}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		provenance, err := resolveSelfUpdateTierOn(runtime.GOOS, executable, runner)
		if err != nil || provenance.Tier != TierPackageManager {
			b.Fatalf("ownership=%+v err=%v", provenance, err)
		}
		if command := provenance.GetSelfUpdateCommand(nil, target); command == nil {
			b.Fatal("owner command missing")
		}
	}
}
