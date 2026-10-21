package subprocess

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// The collector reads the SDK fingerprint marker of a published entry and expires the entry when the marker names an SDK no staged
// copy has, so a marker that a start is still writing must never be readable half written. The build records the marker by
// replacing it, never by writing through the path.
func TestBuilderRecordsTheSDKFingerprintByReplacingTheMarker(t *testing.T) {
	configRoot := t.TempDir()
	sdkDir := stagedSDKDir(t, configRoot, "go")
	if err := os.MkdirAll(sdkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdkDir, "go.mod"), []byte("module github.com/MichaelKinsy/PiG/extensions/sdk\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdkDir, "sdk.go"), []byte("package sdk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte("module extension\ngo 1.26\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package main\nimport _ \"github.com/MichaelKinsy/PiG/extensions/sdk\"\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cacheDir := t.TempDir()
	builder := NewBuilderWithConfigRoot(cacheDir, configRoot)
	stagedSDK, err := builder.resolveStagedSDK(srcDir, "go")
	if err != nil || stagedSDK == "" {
		t.Fatalf("resolveStagedSDK = %q, %v", stagedSDK, err)
	}
	hash, err := hashSourceDir(srcDir, "go")
	if err != nil {
		t.Fatal(err)
	}
	if hash, err = hashWithStagedSDK(hash, stagedSDK, "go"); err != nil {
		t.Fatal(err)
	}
	entryDir := filepath.Join(cacheDir, "extension-"+hash)
	artifactName := extensionArtifactName(runtime.GOOS, "go")
	// An entry published without a marker, as a peer's build is for the moment between its publication and its marker.
	if _, err := runtimecell.PublishArtifact(context.Background(), entryDir, artifactName, hash, "go", func(scratch string) (string, error) {
		binaryPath := filepath.Join(scratch, artifactName)
		return binaryPath, os.WriteFile(binaryPath, []byte("test executable"), 0o755)
	}); err != nil {
		t.Fatal(err)
	}
	// A dangling link stands where the marker goes: a write through the path creates its target, a replacement does not.
	marker := filepath.Join(entryDir, sdkFingerprintFile)
	through := filepath.Join(t.TempDir(), "through")
	testenv.Symlink(t, through, marker)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := builder.BuildContext(ctx, "extension", srcDir)
	if err != nil || !result.Cached {
		t.Fatalf("warm build = %+v, %v; want the published entry", result, err)
	}
	if _, err := os.Stat(through); !os.IsNotExist(err) {
		t.Fatalf("the marker was written in place through its path (stat of the target: %v)", err)
	}
	want, err := StagedSDKFingerprint(stagedSDK, "go")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(marker)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("marker = %v, %v; want a regular file", info, err)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != want {
		t.Fatalf("marker = %q, %v; want %q", got, err, want)
	}
}
