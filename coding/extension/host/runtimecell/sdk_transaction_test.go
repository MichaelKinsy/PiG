package runtimecell

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
)

func TestGoPackedCellBuildWaitsForSDKStageLock(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("PIG_HOME", configRoot)
	t.Setenv("PIG_SDK_GO_ROOT", "")
	sdkRoot := filepath.Join(configRoot, "state", "pigsdk", "sdk")
	sdkSource, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "extensions", "sdk"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(sdkRoot, os.DirFS(sdkSource)); err != nil {
		t.Fatal(err)
	}
	extensionRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(extensionRoot, "go.mod"), []byte("module example.com/extension\ngo 1.26\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extensionRoot, "extension.go"), []byte("package extension\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\nfunc Extension() *sdk.Extension { return sdk.New(\"extension\") }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	release, err := pigsdklock.AcquireStage(context.Background(), configRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = BuildGoPackedCell(ctx, filepath.Join(configRoot, "cache"), "packed-go", []GoExtension{{
		Name: "extension", Root: extensionRoot, ModulePath: "example.com/extension", Package: "example.com/extension", Factory: "Extension", Hash: "source",
	}})
	if err == nil || !strings.Contains(err.Error(), "SDK transaction lock") {
		t.Fatalf("packed build error = %v, want SDK transaction lock wait failure", err)
	}
}

func TestGoPackedCellBuildWaitsWhileSDKRootIsBeingReplaced(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("PIG_HOME", configRoot)
	t.Setenv("PIG_SDK_GO_ROOT", "")
	t.Setenv("PIG_SOURCE_ROOT", "")
	t.Setenv("HOME", t.TempDir())

	sdkRoot := filepath.Join(configRoot, "state", "pigsdk", "sdk")
	if err := os.MkdirAll(sdkRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	extensionRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(extensionRoot, "go.mod"), []byte("module example.com/extension\ngo 1.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	release, err := pigsdklock.AcquireStage(context.Background(), configRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	if err := os.RemoveAll(sdkRoot); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = BuildGoPackedCell(ctx, filepath.Join(configRoot, "cache"), "packed-go", []GoExtension{{
		Name: "extension", Root: extensionRoot, ModulePath: "example.com/extension", Package: "example.com/extension", Factory: "Extension", Hash: "source",
	}})
	if err == nil || !strings.Contains(err.Error(), "SDK transaction lock") {
		t.Fatalf("packed build returned before acquiring transaction lock: %v", err)
	}
}

// publishTestCellEntry seeds a complete published entry, as a previous startup
// that built the cell would have left it.
func publishTestCellEntry(t *testing.T, cellDir, artifact string) string {
	t.Helper()
	digest := filepath.Base(cellDir)
	language := filepath.Base(filepath.Dir(cellDir))
	entry, err := PublishArtifact(context.Background(), cellDir, artifact, digest, language, func(scratch string) (string, error) {
		artifactPath := filepath.Join(scratch, artifact)
		return artifactPath, os.WriteFile(artifactPath, []byte("#!/bin/sh\n"), 0o755)
	})
	if err != nil {
		t.Fatal(err)
	}
	return entry.ArtifactPath
}

// The owner's startup blocked for the whole lock hold when another process
// held the staged-SDK transaction lock, although the SDK was current and the
// packed cell was already built. A warm cell must load without the lease; a
// changed SDK must still wait for it.
func TestGoPackedCellWarmEntryDoesNotWaitForSDKStageLock(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("PIG_HOME", configRoot)
	t.Setenv("PIG_SDK_GO_ROOT", "")
	sdkRoot := filepath.Join(configRoot, "state", "pigsdk", "sdk")
	sdkSource, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "extensions", "sdk"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(sdkRoot, os.DirFS(sdkSource)); err != nil {
		t.Fatal(err)
	}
	extensionRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(extensionRoot, "go.mod"), []byte("module example.com/extension\ngo 1.26\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	extensions := []GoExtension{{
		Name: "extension", Root: extensionRoot, ModulePath: "example.com/extension", Package: "example.com/extension", Factory: "Extension", Hash: "source",
	}}
	normalized, err := normalizeGoExtensions(extensions, true)
	if err != nil {
		t.Fatal(err)
	}
	cacheRoot := filepath.Join(configRoot, "cache")
	hash := goPackedCellHash(cacheRoot, "packed-go", normalized, sdkRoot)
	artifact := publishTestCellEntry(t, filepath.Join(cacheRoot, "cells", "go", hash), packedRunnerName(runtime.GOOS, "go"))
	if entry, valid, err := CurrentGoPackedCellEntry(cacheRoot, "packed-go", extensions); err != nil || !valid || entry != filepath.Dir(artifact) {
		t.Fatalf("current Go cell = %q, %t, %v; want published %s", entry, valid, err, artifact)
	}

	release, err := pigsdklock.AcquireStage(context.Background(), configRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cell, err := BuildGoPackedCell(ctx, cacheRoot, "packed-go", extensions)
	if err != nil {
		t.Fatalf("warm packed cell waited for the SDK stage lock: %v", err)
	}
	if !cell.Cached || cell.BinaryPath != artifact || cell.Hash != hash {
		t.Fatalf("warm packed cell = %+v, want cached %s", cell, artifact)
	}

	if err := os.WriteFile(filepath.Join(sdkRoot, "restaged.go"), []byte("package sdk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changedCtx, changedCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer changedCancel()
	if _, err := BuildGoPackedCell(changedCtx, cacheRoot, "packed-go", extensions); err == nil || !strings.Contains(err.Error(), "SDK transaction lock") {
		t.Fatalf("packed cell for a changed SDK = %v, want SDK transaction lock wait", err)
	}
}

func TestRustPackedCellWarmEntryDoesNotWaitForSDKStageLock(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("PIG_HOME", configRoot)
	t.Setenv("PIG_SDK_RS_ROOT", "")
	sdkRoot := filepath.Join(configRoot, "state", "pigsdk", "sdk-rs")
	if err := os.MkdirAll(sdkRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdkRoot, "Cargo.toml"), []byte("[package]\nname = \"pig-sdk\"\nversion = \"0.0.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	extensionRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(extensionRoot, "Cargo.toml"), []byte("[package]\nname = \"packed_a\"\nversion = \"0.1.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	extensions := []RustExtension{{Name: "a-ext", Root: extensionRoot, Package: "packed_a", Factory: "new_extension", Hash: "ha"}}
	normalized, err := normalizeRustExtensions(extensions, true)
	if err != nil {
		t.Fatal(err)
	}
	cacheRoot := filepath.Join(configRoot, "cache")
	hash := rustPackedCellHash(cacheRoot, "packed-rust", normalized, sdkRoot)
	artifact := publishTestCellEntry(t, filepath.Join(cacheRoot, "cells", "rust", hash), packedRunnerName(runtime.GOOS, "rust"))
	if entry, valid, err := CurrentRustPackedCellEntry(cacheRoot, "packed-rust", extensions); err != nil || !valid || entry != filepath.Dir(artifact) {
		t.Fatalf("current Rust cell = %q, %t, %v; want published %s", entry, valid, err, artifact)
	}
	if resolved, err := findRustSDKRoot(normalized); err != nil || resolved != sdkRoot {
		t.Fatalf("resolved Rust SDK root = %q, %v; want %q", resolved, err, sdkRoot)
	}
	if _, ok := reuseRustPackedCell(cacheRoot, "packed-rust", normalized); !ok {
		t.Fatal("freshly published Rust cell is not reusable before the SDK lock is held")
	}

	release, err := pigsdklock.AcquireStage(context.Background(), configRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cell, err := BuildRustPackedCell(ctx, cacheRoot, "packed-rust", extensions)
	if err != nil {
		t.Fatalf("warm Rust packed cell waited for the SDK stage lock: %v", err)
	}
	if !cell.Cached || cell.BinaryPath != artifact {
		t.Fatalf("warm Rust packed cell = %+v, want cached %s", cell, artifact)
	}
}
