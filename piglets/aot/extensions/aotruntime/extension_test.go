package aotruntime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusReportsTheCompilerImagesAndFailures(t *testing.T) {
	w := newWorker(t.TempDir(), "linux-x64", nil)
	status := w.status(context.Background())
	for _, want := range []string{"experimental", "platform: linux-x64", "compiler: not installed", "images: 0, 0 bytes of 1.0 GB", `"extensionsTypescriptRuntime": "aot"`} {
		if !strings.Contains(status, want) {
			t.Errorf("status lacks %q:\n%s", want, status)
		}
	}
	if _, err := w.store.publish(key("a"), fakeBuilt(t, "image", nil)); err != nil {
		t.Fatal(err)
	}
	if err := w.store.recordFailure(key("b"), failure{Reason: "path_sensitive", Detail: "/x reads files"}); err != nil {
		t.Fatal(err)
	}
	if err := w.store.recordFailure(key("c"), failure{Reason: "build_failed", Detail: "exit 1"}); err != nil {
		t.Fatal(err)
	}
	status = w.status(context.Background())
	for range 20 {
		if again := w.status(context.Background()); again != status {
			t.Fatalf("status order changed between calls:\n%s\n---\n%s", status, again)
		}
	}
	if b, c := strings.Index(status, "bbbbbbbbbbbbbbbb"), strings.Index(status, "cccccccccccccccc"); b < 0 || c < b {
		t.Errorf("failures are not listed in key order:\n%s", status)
	}
	for _, want := range []string{"images: 1,", "aaaaaaaaaaaaaaaa", "not compiled in: bbbbbbbbbbbbbbbb: path_sensitive (/x reads files)"} {
		if !strings.Contains(status, want) {
			t.Errorf("status lacks %q:\n%s", want, status)
		}
	}
	unsupported := newWorker(t.TempDir(), "windows-x64", nil).status(context.Background())
	if !strings.Contains(unsupported, "windows-x64 is not supported") {
		t.Errorf("unsupported status:\n%s", unsupported)
	}
}

func TestCommandActions(t *testing.T) {
	w := newWorker(t.TempDir(), "linux-x64", nil)
	ctx := context.Background()
	if _, err := runAction(ctx, "frobnicate", w); err == nil || !strings.Contains(err.Error(), "Usage:") {
		t.Errorf("unknown action = %v", err)
	}
	if message, err := runAction(ctx, "budget", w); err != nil || !strings.Contains(message, "1.0 GB") {
		t.Errorf("budget = %q, %v", message, err)
	}
	if _, err := runAction(ctx, "budget lots", w); err == nil {
		t.Error("an invalid budget was accepted")
	}
	if message, err := runAction(ctx, "budget 2GB", w); err != nil || !strings.Contains(message, "2GB") {
		t.Errorf("set budget = %q, %v", message, err)
	}
	if got, _ := w.store.cacheBudget(); got != 2<<30 {
		t.Errorf("budget after set = %d", got)
	}
	if _, err := runAction(ctx, "verify", w); err == nil {
		t.Error("verify of a missing compiler succeeded")
	}
	if _, err := w.store.publish(key("a"), fakeBuilt(t, "image", nil)); err != nil {
		t.Fatal(err)
	}
	if message, err := runAction(ctx, "remove", w); err != nil || !strings.Contains(message, "Removed") && !strings.Contains(message, "nothing to remove") {
		t.Errorf("remove = %q, %v", message, err)
	}
	if _, err := os.Stat(w.store.manifestPath()); err == nil {
		t.Error("remove left the manifest")
	}
	unsupported := newWorker(t.TempDir(), "windows-x64", nil)
	if _, err := runAction(ctx, "install", unsupported); err == nil || !strings.Contains(err.Error(), "does not support windows-x64") {
		t.Errorf("install on windows = %v", err)
	}
}

// A Piglet Binary build names a fused member by its directory, the Piglet entry selects it by name, and the member registers its own name: the build and
// the runtime refuse the member unless the three are one string ("lock extension ...: selected origin is unavailable" at build,
// "register:name_mismatch" at start). pig piglet build of a Piglet that lists this extension depends on it.
func TestExtensionNameIsItsDirectoryAndItsPigletEntry(t *testing.T) {
	name := Extension().Name()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(dir); got != name {
		t.Errorf("the extension registers %q but its directory is %q", name, got)
	}
	manifest, err := os.ReadFile("../../pig-aot.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), "\n  - name: "+name+"\n") {
		t.Errorf("pig-aot.yaml has no extensions entry named %q:\n%s", name, manifest)
	}
}
