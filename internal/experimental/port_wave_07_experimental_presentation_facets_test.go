package experimental

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"

	"github.com/google/uuid"
)

func TestPortWave07ExperimentalPresentationFacets(t *testing.T) {
	t.Parallel()
	// upstream: packages/coding-agent/test/experimental-presentation-facets.test.ts:42.
	t.Run("restores plugin package selections for later server generations", func(t *testing.T) {
		t.Parallel()
		directory := t.TempDir()
		serverId := uuid.NewString()
		packagePaths := []string{filepath.Join(directory, "first-plugin"), filepath.Join(directory, "second-plugin")}
		steps := []struct{ configured, want []string }{
			{packagePaths, packagePaths},
			{nil, packagePaths},
			{[]string{}, []string{}},
			{nil, []string{}},
		}
		for i, step := range steps {
			got, err := RestoreServerPluginPackageProfile(directory, serverId, step.configured)
			if err != nil {
				t.Fatalf("step %d: %v", i, err)
			}
			if !reflect.DeepEqual(got, step.want) {
				t.Fatalf("step %d: paths = %#v, want %#v", i, got, step.want)
			}
		}
	})
}

// upstream: packages/coding-agent/src/experimental/plugins/package.ts:29 uses non-recursive rm even when force is true.
func TestPortWave07PluginProfileClearRejectsDirectory(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	serverId := uuid.NewString()
	path := filepath.Join(directory, "plugin-packages-"+serverId+".json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreServerPluginPackageProfile(directory, serverId, []string{}); !errors.Is(err, syscall.EISDIR) {
		t.Fatalf("clear directory error = %v, want EISDIR", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatal("profile directory was replaced")
	}
}
