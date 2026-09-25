package subprocess

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestPackedNodeMembersUseNativeTransportAndSharedProcess(t *testing.T) {
	nodeCellRequireNode(t)
	root := filepath.Join(t.TempDir(), "member space%#é")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	var configs []ExtConfig
	for _, name := range []string{"native-one", "native-two"} {
		entry := filepath.Join(root, name+".mjs")
		nodeCellTrivialExtension(t, entry, name)
		configs = append(configs, ExtConfig{Name: name, Source: entry, Enabled: true})
	}
	host := NewHostWithConfigRoot(root, filepath.Join(root, "config"))
	t.Cleanup(func() { host.Shutdown("test done") })
	loaded, errs := host.LoadAll(t.Context(), configs)
	if len(errs) != 0 || len(loaded) != len(configs) {
		t.Fatalf("LoadAll = %d extensions, %v; want %d", len(loaded), errs, len(configs))
	}
	var pid int
	for _, ext := range host.Extensions() {
		tool, ok := ext.Tools[ext.Name]
		if !ok {
			t.Fatalf("missing tool for %s", ext.Name)
		}
		result, err := tool.Definition.Execute(t.Context(), "native-call-"+ext.Name, json.RawMessage(`{}`), nil)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(result)
		if err != nil || !strings.Contains(string(data), ext.Name) {
			t.Fatalf("tool %s = %s, %v", ext.Name, data, err)
		}
		host.mu.Lock()
		member := host.exts[ext.Name]
		address, memberPID := member.sockPath, member.proc.Pid
		host.mu.Unlock()
		if runtime.GOOS == "windows" && !strings.HasPrefix(address, `\\.\pipe\`) {
			t.Fatalf("Node member %s received %q, want native named pipe", ext.Name, address)
		}
		if pid != 0 && memberPID != pid {
			t.Fatalf("members use different processes: %d and %d", pid, memberPID)
		}
		pid = memberPID
	}
}

// Packed and isolated Node cells use the same direct-node launcher and URL
// rules. A POSIX shell artifact cannot launch a cell on native Windows.
func TestPackedNodeLauncherUsesDirectNodeOnEveryPlatform(t *testing.T) {
	root := filepath.Join(t.TempDir(), "space ! percent% hash# unicode-é")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	exts := []nodeExtension{{Name: "one", Entry: filepath.Join(root, "one.mjs")}, {Name: "two", Entry: filepath.Join(root, "two.mjs")}}
	cell, err := buildNodePackedCell(t.Context(), root, "packed:node", exts)
	if err != nil {
		t.Fatal(err)
	}
	if !usesNodeRuntime(cell.BinaryPath, "") {
		t.Error("packed launcher not recognized as Node; Windows members would receive Unix sockets")
	}
	loaderURL, err := nodeFileURL(filepath.Join(filepath.Dir(cell.BinaryPath), "runtime", "register-loader.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"node", "--import", loaderURL, filepath.Join(filepath.Dir(cell.BinaryPath), "runtime", "cell.mjs"), filepath.Join(filepath.Dir(cell.BinaryPath), "manifest.json"), exts[0].Entry, exts[1].Entry}
	for _, platform := range []string{"linux", "darwin", "windows"} {
		cmd := buildExtCommandForGOOS(t.Context(), cell.BinaryPath, "", platform, func(name string) (string, error) { return name, nil })
		if !reflect.DeepEqual(cmd.Args, want) {
			t.Errorf("%s command = %q, want %q", platform, cmd.Args, want)
		}
	}
	if !strings.HasPrefix(loaderURL, "file://") || !strings.Contains(loaderURL, "%23") || !strings.Contains(loaderURL, "%25") {
		t.Fatalf("loader module specifier was not URL encoded: %q", loaderURL)
	}
	warm, err := buildNodePackedCell(t.Context(), root, "packed:node", exts)
	if err != nil || warm == nil || !warm.Cached {
		t.Fatalf("warm packed cell = %+v, %v", warm, err)
	}
}
